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

func loadPlugins(ns *sriovnetworkv1.SriovNetworkNodeState, helpers helper.HostHelpersInterface, disabledPlugins []string) (map[string]plugin.VendorPlugin, error) {
	log.Log.Info("loadPlugins(): loading plugins")
	loadedPlugins := map[string]plugin.VendorPlugin{}

	if vars.PlatformType == consts.VirtualOpenStack {
		virtualPlugin, err := VirtualPlugin(helpers)
		if err != nil {
			log.Log.Error(err, "loadPlugins(): failed to load the virtual plugin")
			return nil, err
		}
		pluginName := virtualPlugin.Name()
		if !isPluginDisabled(pluginName, disabledPlugins) {
			loadedPlugins[pluginName] = virtualPlugin
		}
	} else {
		loadedVendorPlugins, err := loadVendorPlugins(ns, helpers, disabledPlugins)
		if err != nil {
			return nil, err
		}
		loadedPlugins = loadedVendorPlugins

		if vars.ClusterType != consts.ClusterTypeOpenshift {
			k8sPlugin, err := K8sPlugin(helpers)
			if err != nil {
				log.Log.Error(err, "loadPlugins(): failed to load the k8s plugin")
				return nil, err
			}

			pluginName := k8sPlugin.Name()
			if !isPluginDisabled(pluginName, disabledPlugins) {
				loadedPlugins[pluginName] = k8sPlugin
			}
		}
		genericPlugin, err := GenericPlugin(helpers)
		if err != nil {
			log.Log.Error(err, "loadPlugins(): failed to load the generic plugin")
			return nil, err
		}
		pluginName := genericPlugin.Name()
		if !isPluginDisabled(pluginName, disabledPlugins) {
			loadedPlugins[pluginName] = genericPlugin
		}
	}

	pluginList := make([]string, 0, len(loadedPlugins))
	for pluginName := range loadedPlugins {
		pluginList = append(pluginList, pluginName)
	}
	log.Log.Info("loadPlugins(): loaded plugins", "plugins", pluginList)
	return loadedPlugins, nil
}

func loadVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState, helpers helper.HostHelpersInterface, disabledPlugins []string) (map[string]plugin.VendorPlugin, error) {
	vendorPlugins := map[string]plugin.VendorPlugin{}

	for _, iface := range ns.Status.Interfaces {
		if val, ok := VendorPluginMap[iface.Vendor]; ok {
			plug, err := val(helpers)
			if err != nil {
				log.Log.Error(err, "loadVendorPlugins(): failed to load plugin", "plugin-name", plug.Name())
				return vendorPlugins, fmt.Errorf("loadVendorPlugins(): failed to load the %s plugin error: %v", plug.Name(), err)
			}

			pluginName := plug.Name()
			if _, ok := vendorPlugins[pluginName]; !ok {
				if !isPluginDisabled(pluginName, disabledPlugins) {
					vendorPlugins[plug.Name()] = plug
				}
			}
		}
	}

	return vendorPlugins, nil
}

func isPluginDisabled(pluginName string, disabledPlugins []string) bool {
	for _, p := range disabledPlugins {
		if p == pluginName {
			log.Log.V(2).Info("plugin is disabled", "name", pluginName)
			return true
		}
	}
	return false
}
