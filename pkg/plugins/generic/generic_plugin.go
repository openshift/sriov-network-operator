package generic

import (
	"bytes"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var PluginName = "generic_plugin"

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

// Initialize our plugin and set up initial values
func NewGenericPlugin() (plugin.VendorPlugin, error) {
	return &GenericPlugin{
		PluginName:     PluginName,
		SpecVersion:    "1.0",
		LoadVfioDriver: unloaded,
	}, nil
}

// Name returns the name of the plugin
func (p *GenericPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *GenericPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is created or updated, return if need dain and/or reboot node
func (p *GenericPlugin) OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
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

	// Create a map with all the PFs we will need to configure
	// we need to create it here before we access the host file system using the chroot function
	// because the skipConfigVf needs the mstconfig package that exist only inside the sriov-config-daemon file system
	pfsToSkip, err := utils.GetPfsToSkip(p.DesireState)
	if err != nil {
		return err
	}

	exit, err := utils.Chroot("/host")
	if err != nil {
		return err
	}
	defer exit()
	if err := utils.SyncNodeState(p.DesireState, pfsToSkip); err != nil {
		return err
	}
	p.LastState = &sriovnetworkv1.SriovNetworkNodeState{}
	*p.LastState = *p.DesireState
	return nil
}

func needVfioDriver(state *sriovnetworkv1.SriovNetworkNodeState) bool {
	for _, iface := range state.Spec.Interfaces {
		for i := range iface.VfGroups {
			if iface.VfGroups[i].DeviceType == constants.DeviceTypeVfioPci {
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
				if utils.NeedUpdate(&iface, &ifaceStatus) {
					glog.V(2).Infof("generic-plugin needDrainNode(): need drain, PF %s request update", iface.PciAddress)
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
