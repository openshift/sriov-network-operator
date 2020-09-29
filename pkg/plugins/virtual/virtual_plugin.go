package main

import (
	"os/exec"
	"reflect"
	"syscall"

	"github.com/golang/glog"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/api/v1"
	"github.com/openshift/sriov-network-operator/pkg/utils"
)

// VirtualPlugin Plugin type to use on a virtual platform
type VirtualPlugin struct {
	PluginName     string
	SpecVersion    string
	DesireState    *sriovnetworkv1.SriovNetworkNodeState
	LastState      *sriovnetworkv1.SriovNetworkNodeState
	LoadVfioDriver uint
}

const (
	unloaded = iota
	loading
	loaded
)

// Plugin VirtualPlugin type
var Plugin VirtualPlugin

// Initialize our plugin and set up initial values
func init() {
	Plugin = VirtualPlugin{
		PluginName:     "virtual_plugin",
		SpecVersion:    "1.0",
		LoadVfioDriver: unloaded,
	}
}

// Name returns the name of the plugin
func (p *VirtualPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *VirtualPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *VirtualPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("virtual-plugin OnNodeStateAdd()")
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = state

	needDrain = needDrainNode(state.Spec.Interfaces, state.Status.Interfaces)

	if p.LoadVfioDriver != loaded {
		if needVfioDriver(state) {
			p.LoadVfioDriver = loading
		}
	}
	return
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *VirtualPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("virtual-plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = new

	if p.LoadVfioDriver != loaded {
		if needVfioDriver(new) {
			p.LoadVfioDriver = loading
		}
	}

	return
}

// Apply config change
func (p *VirtualPlugin) Apply() error {
	glog.Infof("virtual-plugin Apply(): desiredState=%v", p.DesireState.Spec)

	if p.LoadVfioDriver == loading {
		if err := utils.LoadKernelModule("vfio_pci"); err != nil {
			glog.Errorf("virtual-plugin Apply(): fail to load vfio_pci kmod: %v", err)
			return err
		}
		p.LoadVfioDriver = loaded
	}

	if p.LastState != nil {
		glog.Infof("virtual-plugin Apply(): lastStat=%v", p.LastState.Spec)
		if reflect.DeepEqual(p.LastState.Spec.Interfaces, p.DesireState.Spec.Interfaces) {
			glog.Info("virtual-plugin Apply(): nothing to apply")
			return nil
		}
	}
	exit, err := utils.Chroot("/host")
	if err != nil {
		return err
	}
	defer exit()
	if err := utils.SyncNodeStateVirtual(p.DesireState); err != nil {
		return err
	}
	p.LastState = &sriovnetworkv1.SriovNetworkNodeState{}
	*p.LastState = *p.DesireState

	return nil
}

func needVfioDriver(state *sriovnetworkv1.SriovNetworkNodeState) bool {
	for _, iface := range state.Spec.Interfaces {
		for i := range iface.VfGroups {
			if iface.VfGroups[i].DeviceType == "vfio-pci" {
				return true
			}
		}
	}
	return false
}

func isCommandNotFound(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.ExitStatus() == 127 {
			return true
		}
	}
	return false
}

func needDrainNode(desired sriovnetworkv1.Interfaces, current sriovnetworkv1.InterfaceExts) (needDrain bool) {
	needDrain = false
	return
}
