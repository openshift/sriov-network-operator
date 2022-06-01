package daemon

import (
	"fmt"

	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	genericplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	intelplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/intel"
	k8splugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/k8s"
	mellanoxplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/mellanox"
	virtualplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/virtual"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var VendorPluginMap = map[string]func() (plugin.VendorPlugin, error){
	"8086": intelplugin.NewIntelPlugin,
	"15b3": mellanoxplugin.NewMellanoxPlugin,
}

var (
	GenericPlugin     = genericplugin.NewGenericPlugin
	GenericPluginName = genericplugin.PluginName
	VirtualPlugin     = virtualplugin.NewVirtualPlugin
	K8sPlugin         = k8splugin.NewK8sPlugin
)

func enablePlugins(platform utils.PlatformType, ns *sriovnetworkv1.SriovNetworkNodeState) (map[string]plugin.VendorPlugin, error) {
	glog.Infof("enableVendorPlugins(): enabling plugins")
	enabledPlugins := map[string]plugin.VendorPlugin{}

	if platform == utils.VirtualOpenStack {
		virtualPlugin, err := VirtualPlugin()
		if err != nil {
			glog.Errorf("enableVendorPlugins(): failed to load the virtual plugin error: %v", err)
			return nil, err
		}
		enabledPlugins[virtualPlugin.Name()] = virtualPlugin
	} else {
		enabledVendorPlugins, err := registerVendorPlugins(ns)
		if err != nil {
			return nil, err
		}
		enabledPlugins = enabledVendorPlugins

		if utils.ClusterType != utils.ClusterTypeOpenshift {
			k8sPlugin, err := K8sPlugin()
			if err != nil {
				glog.Errorf("enableVendorPlugins(): failed to load the k8s plugin error: %v", err)
				return nil, err
			}
			enabledPlugins[k8sPlugin.Name()] = k8sPlugin
		}
		genericPlugin, err := GenericPlugin()
		if err != nil {
			glog.Errorf("enableVendorPlugins(): failed to load the generic plugin error: %v", err)
			return nil, err
		}
		enabledPlugins[genericPlugin.Name()] = genericPlugin
	}

	pluginList := make([]string, 0, len(enabledPlugins))
	for pluginName := range enabledPlugins {
		pluginList = append(pluginList, pluginName)
	}
	glog.Infof("enableVendorPlugins(): enabled plugins %s", pluginList)
	return enabledPlugins, nil
}

func registerVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) (map[string]plugin.VendorPlugin, error) {
	vendorPlugins := map[string]plugin.VendorPlugin{}

	for _, iface := range ns.Status.Interfaces {
		if val, ok := VendorPluginMap[iface.Vendor]; ok {
			plug, err := val()
			if err != nil {
				glog.Errorf("registerVendorPlugins(): failed to load the %s plugin error: %v", plug.Name(), err)
				return vendorPlugins, fmt.Errorf("registerVendorPlugins(): failed to load the %s plugin error: %v", plug.Name(), err)
			}
			vendorPlugins[plug.Name()] = plug
		}
	}

	return vendorPlugins, nil
}
