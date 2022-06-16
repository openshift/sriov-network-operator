package main

import (
	"bytes"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/golang/glog"
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

type GenericPlugin struct {
	PluginName     string
	SpecVersion    string
	DesireState    *sriovnetworkv1.SriovNetworkNodeState
	LastState      *sriovnetworkv1.SriovNetworkNodeState
	LoadVfioDriver uint
}

const scriptsPath = "bindata/scripts/enable-kargs.sh"

const (
	unloaded = iota
	loading
	loaded
)

var Plugin GenericPlugin

// Initialize our plugin and set up initial values
func init() {
	Plugin = GenericPlugin{
		PluginName:     "generic_plugin",
		SpecVersion:    "1.0",
		LoadVfioDriver: unloaded,
	}
}

// Name returns the name of the plugin
func (p *GenericPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *GenericPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *GenericPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("generic-plugin OnNodeStateAdd()")
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = state

	needDrain = needDrainNode(state.Spec.Interfaces, state.Status.Interfaces)
	needReboot = needRebootNode(state, &p.LoadVfioDriver)

	if needReboot {
		needDrain = true
	}
	return
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *GenericPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("generic-plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = new

	needDrain = needDrainNode(new.Spec.Interfaces, new.Status.Interfaces)
	needReboot = needRebootNode(new, &p.LoadVfioDriver)

	if needReboot {
		needDrain = true
	}
	return
}

// Apply config change
func (p *GenericPlugin) Apply() error {
	glog.Infof("generic-plugin Apply(): desiredState=%v", p.DesireState.Spec)
	if p.LoadVfioDriver == loading {
		if err := utils.LoadKernelModule("vfio_pci"); err != nil {
			glog.Errorf("generic-plugin Apply(): fail to load vfio_pci kmod: %v", err)
			return err
		}
		p.LoadVfioDriver = loaded
	}

	if p.LastState != nil {
		glog.Infof("generic-plugin Apply(): lastStat=%v", p.LastState.Spec)
		if reflect.DeepEqual(p.LastState.Spec.Interfaces, p.DesireState.Spec.Interfaces) {
			glog.Info("generic-plugin Apply(): nothing to apply")
			return nil
		}
	}
	exit, err := utils.Chroot("/host")
	if err != nil {
		return err
	}
	defer exit()
	if err := utils.SyncNodeState(p.DesireState); err != nil {
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

func tryEnableIommuInKernelArgs() (bool, error) {
	glog.Info("generic-plugin tryEnableIommuInKernelArgs()")
	args := [2]string{"intel_iommu=on", "iommu=pt"}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/sh", scriptsPath, args[0], args[1])
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// if grubby is not there log and assume kernel args are set correctly.
		if isCommandNotFound(err) {
			glog.Error("generic-plugin tryEnableIommuInKernelArgs(): grubby command not found. Please ensure that kernel args intel_iommu=on iommu=pt are set")
			return false, nil
		}
		glog.Errorf("generic-plugin tryEnableIommuInKernelArgs(): fail to enable iommu %s: %v", args, err)
		return false, err
	}

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i > 0 {
			glog.Infof("generic-plugin tryEnableIommuInKernelArgs(): need to reboot node")
			return true, nil
		}
	}
	return false, err
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
	glog.V(2).Infof("generic-plugin needDrainNode(): current state '%+v', desired state '%+v'", current, desired)
	needDrain = false
	for _, ifaceStatus := range current {
		configured := false
		for _, iface := range desired {
			if iface.PciAddress == ifaceStatus.PciAddress {
				// TODO: no need to perform further checks if ifaceStatus.NumVfs equals to 0
				// once https://github.com/kubernetes/kubernetes/issues/109595 will be fixed
				configured = true
				if iface.NumVfs != ifaceStatus.NumVfs {
					glog.V(2).Infof("generic-plugin needDrainNode(): need drain, expect NumVfs %v, current NumVfs %v", iface.NumVfs, ifaceStatus.NumVfs)
					needDrain = true
					return
				}
				if iface.Mtu != 0 && iface.Mtu != ifaceStatus.Mtu {
					glog.V(2).Infof("generic-plugin needDrainNode(): need drain, expect MTU %v, current MTU %v", iface.Mtu, ifaceStatus.Mtu)
					needDrain = true
					return
				}
				glog.V(2).Infof("generic-plugin needDrainNode(): no need drain, expect NumVfs %v, current NumVfs %v", iface.NumVfs, ifaceStatus.NumVfs)
			}
		}
		if !configured && ifaceStatus.NumVfs > 0 {
			glog.V(2).Infof("generic-plugin needDrainNode(): need drain, %v needs to be reset", ifaceStatus)
			needDrain = true
			return
		}
	}
	return
}

func needRebootNode(state *sriovnetworkv1.SriovNetworkNodeState, loadVfioDriver *uint) (needReboot bool) {
	needReboot = false
	if *loadVfioDriver != loaded {
		if needVfioDriver(state) {
			*loadVfioDriver = loading
			update, err := tryEnableIommuInKernelArgs()
			if err != nil {
				glog.Errorf("generic-plugin needRebootNode():fail to enable iommu in kernel args: %v", err)
			}
			if update {
				glog.V(2).Infof("generic-plugin needRebootNode(): need reboot for enabling iommu kernel args")
			}
			needReboot = needReboot || update
		}
	}

	update, err := utils.WriteSwitchdevConfFile(state)
	if err != nil {
		glog.Errorf("generic-plugin needRebootNode(): fail to write switchdev device config file")
	}
	if update {
		glog.V(2).Infof("generic-plugin needRebootNode(): need reboot for updating switchdev device configuration")
	}
	needReboot = needReboot || update
	return
}
