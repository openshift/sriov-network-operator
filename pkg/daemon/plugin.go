package daemon

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	genericplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	intelplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/intel"
	k8splugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/k8s"
	mellanoxplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/mellanox"
	virtualplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/virtual"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var VendorPluginMap = map[string]func(helpers helper.HostHelpersInterface) (plugin.VendorPlugin, error){
	"8086": intelplugin.NewIntelPlugin,
	"15b3": mellanoxplugin.NewMellanoxPlugin,
}

var (
	GenericPlugin     = genericplugin.NewGenericPlugin
	GenericPluginName = genericplugin.PluginName
	VirtualPlugin     = virtualplugin.NewVirtualPlugin
	VirtualPluginName = virtualplugin.PluginName
	K8sPlugin         = k8splugin.NewK8sPlugin
)

func enablePlugins(ns *sriovnetworkv1.SriovNetworkNodeState, helpers helper.HostHelpersInterface) (map[string]plugin.VendorPlugin, error) {
	log.Log.Info("enableVendorPlugins(): enabling plugins")
	enabledPlugins := map[string]plugin.VendorPlugin{}

	if vars.PlatformType == consts.VirtualOpenStack {
		virtualPlugin, err := VirtualPlugin(helpers)
		if err != nil {
			log.Log.Error(err, "enableVendorPlugins(): failed to load the virtual plugin")
			return nil, err
		}
		enabledPlugins[virtualPlugin.Name()] = virtualPlugin
	} else {
		enabledVendorPlugins, err := registerVendorPlugins(ns, helpers)
		if err != nil {
			return nil, err
		}
		enabledPlugins = enabledVendorPlugins

		if vars.ClusterType != consts.ClusterTypeOpenshift {
			k8sPlugin, err := K8sPlugin(helpers)
			if err != nil {
				log.Log.Error(err, "enableVendorPlugins(): failed to load the k8s plugin")
				return nil, err
			}
			enabledPlugins[k8sPlugin.Name()] = k8sPlugin
		}
		genericPlugin, err := GenericPlugin(helpers)
		if err != nil {
			log.Log.Error(err, "enableVendorPlugins(): failed to load the generic plugin")
			return nil, err
		}
		enabledPlugins[genericPlugin.Name()] = genericPlugin
	}

	pluginList := make([]string, 0, len(enabledPlugins))
	for pluginName := range enabledPlugins {
		pluginList = append(pluginList, pluginName)
	}
	log.Log.Info("enableVendorPlugins(): enabled plugins", "plugins", pluginList)
	return enabledPlugins, nil
}

func registerVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState, helpers helper.HostHelpersInterface) (map[string]plugin.VendorPlugin, error) {
	vendorPlugins := map[string]plugin.VendorPlugin{}

	for _, iface := range ns.Status.Interfaces {
		if val, ok := VendorPluginMap[iface.Vendor]; ok {
			plug, err := val(helpers)
			if err != nil {
				log.Log.Error(err, "registerVendorPlugins(): failed to load plugin", "plugin-name", plug.Name())
				return vendorPlugins, fmt.Errorf("registerVendorPlugins(): failed to load the %s plugin error: %v", plug.Name(), err)
			}
			if _, ok := vendorPlugins[plug.Name()]; !ok {
				vendorPlugins[plug.Name()] = plug
			}
		}
	}

	return vendorPlugins, nil
}
