package daemon

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func (dn *NodeReconciler) loadPlugins(ns *sriovnetworkv1.SriovNetworkNodeState, disabledPlugins []string) error {
	funcLog := log.Log.WithName("loadPlugins").WithValues("platform", vars.PlatformType, "orchestrator", vars.ClusterType)
	funcLog.Info("loading plugins", "disabled", disabledPlugins)

	mainPlugin, additionalPlugins, err := dn.platformInterface.GetVendorPlugins(ns)
	if err != nil {
		funcLog.Error(err, "Failed to load plugins", "platform", vars.PlatformType, "orchestrator", vars.ClusterType)
		return err
	}

	// Check if the main plugin is disabled - this is not allowed
	if isPluginDisabled(mainPlugin.Name(), disabledPlugins) {
		return fmt.Errorf("main plugin %s cannot be disabled", mainPlugin.Name())
	}
	dn.mainPlugin = mainPlugin

	for _, plugin := range additionalPlugins {
		if !isPluginDisabled(plugin.Name(), disabledPlugins) {
			dn.additionalPlugins = append(dn.additionalPlugins, plugin)
		}
	}

	additionalPluginsName := make([]string, len(dn.additionalPlugins))
	for idx, plugin := range dn.additionalPlugins {
		additionalPluginsName[idx] = plugin.Name()
	}

	log.Log.Info("loaded plugins", "mainPlugin", dn.mainPlugin.Name(), "additionalPlugins", additionalPluginsName)
	return nil
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
