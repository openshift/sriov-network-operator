package main

import (
	"github.com/golang/glog"
	"github.com/pliurh/sriov-network-operator/pkg/utils"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"

)

type IntelPlugin struct {
	PluginName  string
	SpecVersion string
	DesireState *sriovnetworkv1.SriovNetworkNodeState
	LastState *sriovnetworkv1.SriovNetworkNodeState
	LoadVfioDriver bool
}

var Plugin IntelPlugin

// Initialize our plugin and set up initial values
func init() {
	Plugin = IntelPlugin{
		PluginName:  "intel_plugin",
		SpecVersion: "1.0",
	}
}

// Name returns the name of the plugin
func (p *IntelPlugin) Name() string {
	return p.PluginName
}

// SpecVersion returns the version of the spec expected by the plugin
func (p *IntelPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *IntelPlugin)OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("intel-plugin OnNodeStateAdd()")
	needDrain = true
	needReboot = false
	err = nil
	/////////////////////////////////////////////////
	// TODO: enable "intel_iommu" in kernel parameter, and request to reboot
	/////////////////////////////////////////////////
	p.LoadVfioDriver = needVfioDriver(state)
	return
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *IntelPlugin)OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("intel-plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false
	err = nil

	p.LoadVfioDriver = needVfioDriver(new)
	return
}

// Apply config change
func (p *IntelPlugin)Apply() error {
	glog.Info("intel-plugin Apply()")
	if p.LoadVfioDriver {
		if err := utils.LoadKernelModule("vfio_pci"); err != nil {
			glog.Errorf("Apply(): fail to load vfio_pci kmod: %v", err)
			return err
		}
	}
	return nil
}

func needVfioDriver(state *sriovnetworkv1.SriovNetworkNodeState) bool {
	for _, iface := range state.Spec.Interfaces {
		if iface.DeviceType == "vfio-pci" {
			return true
		}
	}
	return false
}
