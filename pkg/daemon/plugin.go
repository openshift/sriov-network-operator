package daemon

import (
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func (dn *NodeReconciler) loadPlugins(ns *sriovnetworkv1.SriovNetworkNodeState, disabledPlugins []string) error {
	funcLog := log.Log.WithName("loadPlugins").WithValues("platform", vars.PlatformType, "orchestrator", vars.ClusterType)
	funcLog.Info("loading plugins", "disabled", disabledPlugins)

	mainPlugin, additionalPlugins, err := dn.platformInterface.GetPlugins(ns)
	if err != nil {
		funcLog.Error(err, "Failed to load plugins", "platform", vars.PlatformType, "orchestrator", vars.ClusterType)
		return err
	}

	// check for the main plugin disabled
	// TODO: we really want to allow the disable of the main plugin?
	if !isPluginDisabled(mainPlugin.Name(), disabledPlugins) {
		dn.mainPlugin = mainPlugin
	}

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
