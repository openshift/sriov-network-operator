package intel

import (
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
)

var PluginName = "intel"

type IntelPlugin struct {
	PluginName  string
	SpecVersion string
	DesireState *sriovnetworkv1.SriovNetworkNodeState
	LastState   *sriovnetworkv1.SriovNetworkNodeState
}

func NewIntelPlugin(helpers helper.HostHelpersInterface) (plugin.VendorPlugin, error) {
	return &IntelPlugin{
		PluginName:  PluginName,
		SpecVersion: "1.0",
	}, nil
}

// Name returns the name of the plugin
func (p *IntelPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *IntelPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is created or updated, return if need dain and/or reboot node
func (p *IntelPlugin) OnNodeStateChange(*sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error) {
	log.Log.Info("intel-plugin OnNodeStateChange()")
	return false, false, nil
}

// OnNodeStatusChange verify whether SriovNetworkNodeState CR status present changes on configured VFs.
func (p *IntelPlugin) CheckStatusChanges(*sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	return false, nil
}

// Apply config change
func (p *IntelPlugin) Apply() error {
	log.Log.Info("intel plugin Apply()")
	return nil
}
