package intel

import (
	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
)

var PluginName = "intel_plugin"

type IntelPlugin struct {
	PluginName  string
	SpecVersion string
	DesireState *sriovnetworkv1.SriovNetworkNodeState
	LastState   *sriovnetworkv1.SriovNetworkNodeState
}

func NewIntelPlugin() (plugin.VendorPlugin, error) {
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

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *IntelPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("intel-plugin OnNodeStateAdd()")
	return false, false, nil
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *IntelPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("intel-plugin OnNodeStateChange()")
	return false, false, nil
}

// Apply config change
func (p *IntelPlugin) Apply() error {
	glog.Info("intel-plugin Apply()")
	return nil
}
