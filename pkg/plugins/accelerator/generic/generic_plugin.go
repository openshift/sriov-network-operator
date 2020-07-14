package main

import (
	"github.com/golang/glog"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

type GenericAcceleratorPlugin struct {
	PluginName  string
	SpecVersion string
	DesireState *sriovnetworkv1.SriovAcceleratorNodeState
	LastState   *sriovnetworkv1.SriovAcceleratorNodeState
}

var Plugin GenericAcceleratorPlugin

// Initialize our plugin and set up initial values
func init() {
	Plugin = GenericAcceleratorPlugin{
		PluginName:  "generic_accelerator_plugin",
		SpecVersion: "1.0",
	}
}

// Name returns the name of the plugin
func (p *GenericAcceleratorPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *GenericAcceleratorPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovAcceleratorNodeState CR is created, return if need dain and/or reboot node
func (p *GenericAcceleratorPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovAcceleratorNodeState) (bool, bool, error) {
	glog.Info("generic-accelerator-plugin OnNodeStateAdd()")
	return false, false, nil
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *GenericAcceleratorPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovAcceleratorNodeState) (bool, bool, error) {
	glog.Info("generic-accelerator-plugin OnNodeStateChange()")
	return false, false, nil
}

// Apply config change
func (p *GenericAcceleratorPlugin) Apply() error {
	glog.Infof("generic-accelerator-plugin Apply()")
	return nil
}
