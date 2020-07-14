package main

import (
	"reflect"

	"github.com/golang/glog"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	"github.com/openshift/sriov-network-operator/pkg/plugins"
	"github.com/openshift/sriov-network-operator/pkg/utils"
)

type GenericAcceleratorPlugin struct {
	PluginName     string
	SpecVersion    string
	DesireState    *sriovnetworkv1.SriovAcceleratorNodeState
	LastState      *sriovnetworkv1.SriovAcceleratorNodeState
	LoadVfioDriver uint
	LoadPfDriver   uint
}

const (
	unloaded = iota
	loading
	loaded
)

var Plugin GenericAcceleratorPlugin

// Initialize our plugin and set up initial values
func init() {
	Plugin = GenericAcceleratorPlugin{
		PluginName:     "intel_fec_accelerator_plugin",
		SpecVersion:    "1.0",
		LoadVfioDriver: unloaded,
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
	glog.Info("intel-fec-accelerator-plugin OnNodeStateAdd()")
	return p.validateIfDrainOrRebootNeeded(state, state)
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *GenericAcceleratorPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovAcceleratorNodeState) (bool, bool, error) {
	glog.Info("intel-fec-accelerator-plugin OnNodeStateChange()")
	return p.validateIfDrainOrRebootNeeded(new, new)
}

func (p *GenericAcceleratorPlugin) validateIfDrainOrRebootNeeded(current, desired *sriovnetworkv1.SriovAcceleratorNodeState) (needDrain bool, needReboot bool, err error) {
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = desired

	needDrain = needDrainNode(current.Spec.Cards, desired.Status.Accelerators)

	if p.LoadVfioDriver != loaded {
		if needToLoadDrivers(desired) {
			p.LoadVfioDriver = loading
			if needReboot, err = plugins.TryEnableIommuInKernelArgs(); err != nil {
				glog.Errorf("intel-fec-accelerator-plugin OnNodeStateAdd():fail to enable iommu in kernel args: %v", err)
				return
			}
		}
		if needReboot {
			needDrain = true
		}
	}

	if p.LoadPfDriver != loaded {
		p.LoadPfDriver = loading
	}
	return
}

// Apply config change
func (p *GenericAcceleratorPlugin) Apply() error {
	glog.Infof("intel-fec-accelerator-plugin Apply(): desiredState=%v", p.DesireState.Spec)
	if p.LoadVfioDriver == loading {
		if err := utils.LoadKernelModule("vfio_pci"); err != nil {
			glog.Errorf("intel-fec-accelerator-plugin Apply(): fail to load vfio_pci kmod: %v", err)
			return err
		}
		p.LoadVfioDriver = loaded
	}

	if p.LastState != nil {
		glog.Infof("intel-fec-accelerator-plugin Apply(): lastStat=%v", p.LastState.Spec)
		if reflect.DeepEqual(p.LastState.Spec.Cards, p.DesireState.Spec.Cards) {
			glog.Info("intel-fec-accelerator-plugin Apply(): nothing to apply")
			return nil
		}
	}

	if err := utils.SyncAcceleratorNodeState(p.DesireState); err != nil {
		return err
	}
	p.LastState = &sriovnetworkv1.SriovAcceleratorNodeState{}
	*p.LastState = *p.DesireState
	return nil
}

func needToLoadDrivers(current *sriovnetworkv1.SriovAcceleratorNodeState) bool {
	// If there is a card in the spec we need to load the vfio all the accelerators VFs use vfio
	return len(current.Spec.Cards) > 0
}

func needDrainNode(desired sriovnetworkv1.Cards, current sriovnetworkv1.AcceleratorExts) (needDrain bool) {
	needDrain = false
	for _, accelStatus := range current {
		configured := false
		for _, accel := range desired {
			if accel.PciAddress == accelStatus.PciAddress {
				configured = true
				if accel.NumVfs != accelStatus.NumVfs {
					needDrain = true
					return
				}
				if accel.Config != accelStatus.Config {
					needDrain = true
					return
				}
			}
		}
		if !configured && accelStatus.NumVfs > 0 {
			needDrain = true
			return
		}
	}
	return
}
