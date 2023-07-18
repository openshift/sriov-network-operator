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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var PluginName = "generic_plugin"

// driver id
const (
	Vfio = iota
	VirtioVdpa
)

// driver name
const (
	vfioPciDriver    = "vfio_pci"
	virtioVdpaDriver = "virtio_vdpa"
)

// function type for determining if a given driver has to be loaded in the kernel
type needDriver func(state *sriovnetworkv1.SriovNetworkNodeState, driverState *DriverState) bool

type DriverState struct {
	DriverName     string
	DeviceType     string
	VdpaType       string
	NeedDriverFunc needDriver
	DriverLoaded   bool
}

type DriverStateMapType map[uint]*DriverState

type GenericPlugin struct {
	PluginName     string
	SpecVersion    string
	DesireState    *sriovnetworkv1.SriovNetworkNodeState
	LastState      *sriovnetworkv1.SriovNetworkNodeState
	DriverStateMap DriverStateMapType
	RunningOnHost  bool
	HostManager    host.HostManagerInterface
}

const scriptsPath = "bindata/scripts/enable-kargs.sh"

// Initialize our plugin and set up initial values
func NewGenericPlugin(runningOnHost bool) (plugin.VendorPlugin, error) {
	driverStateMap := make(map[uint]*DriverState)
	driverStateMap[Vfio] = &DriverState{
		DriverName:     vfioPciDriver,
		DeviceType:     constants.DeviceTypeVfioPci,
		VdpaType:       "",
		NeedDriverFunc: needDriverCheckDeviceType,
		DriverLoaded:   false,
	}
	driverStateMap[VirtioVdpa] = &DriverState{
		DriverName:     virtioVdpaDriver,
		DeviceType:     constants.DeviceTypeNetDevice,
		VdpaType:       constants.VdpaTypeVirtio,
		NeedDriverFunc: needDriverCheckVdpaType,
		DriverLoaded:   false,
	}

	return &GenericPlugin{
		PluginName:     PluginName,
		SpecVersion:    "1.0",
		DriverStateMap: driverStateMap,
		RunningOnHost:  runningOnHost,
		HostManager:    host.NewHostManager(runningOnHost),
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

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is created or updated, return if need drain and/or reboot node
func (p *GenericPlugin) OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("generic-plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = new

	needDrain = needDrainNode(new.Spec.Interfaces, new.Status.Interfaces)
	needReboot = needRebootNode(new, p.DriverStateMap)

	if needReboot {
		needDrain = true
	}
	return
}

func (p *GenericPlugin) syncDriverState() error {
	for _, driverState := range p.DriverStateMap {
		if !driverState.DriverLoaded && driverState.NeedDriverFunc(p.DesireState, driverState) {
			glog.V(2).Infof("loading driver %s", driverState.DriverName)
			if err := p.HostManager.LoadKernelModule(driverState.DriverName); err != nil {
				glog.Errorf("generic-plugin syncDriverState(): fail to load %s kmod: %v", driverState.DriverName, err)
				return err
			}
			driverState.DriverLoaded = true
		}
	}
	return nil
}

// Apply config change
func (p *GenericPlugin) Apply() error {
	glog.Infof("generic-plugin Apply(): desiredState=%v", p.DesireState.Spec)

	if p.LastState != nil {
		glog.Infof("generic-plugin Apply(): lastStat=%v", p.LastState.Spec)
		if reflect.DeepEqual(p.LastState.Spec.Interfaces, p.DesireState.Spec.Interfaces) {
			glog.Info("generic-plugin Apply(): nothing to apply")
			return nil
		}
	}

	if err := p.syncDriverState(); err != nil {
		return err
	}

	// Create a map with all the PFs we will need to configure
	// we need to create it here before we access the host file system using the chroot function
	// because the skipConfigVf needs the mstconfig package that exist only inside the sriov-config-daemon file system
	pfsToSkip, err := utils.GetPfsToSkip(p.DesireState)
	if err != nil {
		return err
	}

	// When calling from systemd do not try to chroot
	if !p.RunningOnHost {
		exit, err := utils.Chroot("/host")
		if err != nil {
			return err
		}
		defer exit()
	}

	if err := utils.SyncNodeState(p.DesireState, pfsToSkip); err != nil {
		return err
	}
	p.LastState = &sriovnetworkv1.SriovNetworkNodeState{}
	*p.LastState = *p.DesireState
	return nil
}

func needDriverCheckDeviceType(state *sriovnetworkv1.SriovNetworkNodeState, driverState *DriverState) bool {
	for _, iface := range state.Spec.Interfaces {
		for i := range iface.VfGroups {
			if iface.VfGroups[i].DeviceType == driverState.DeviceType {
				return true
			}
		}
	}
	return false
}

func needDriverCheckVdpaType(state *sriovnetworkv1.SriovNetworkNodeState, driverState *DriverState) bool {
	for _, iface := range state.Spec.Interfaces {
		for i := range iface.VfGroups {
			if iface.VfGroups[i].VdpaType == driverState.VdpaType {
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
				configured = true
				if ifaceStatus.NumVfs == 0 {
					glog.V(2).Infof("generic-plugin needDrainNode(): no need drain, for PCI address %s current NumVfs is 0", iface.PciAddress)
					break
				}
				if utils.NeedUpdate(&iface, &ifaceStatus) {
					glog.V(2).Infof("generic-plugin needDrainNode(): need drain, for PCI address %s request update", iface.PciAddress)
					needDrain = true
					return
				}
				glog.V(2).Infof("generic-plugin needDrainNode(): no need drain,for PCI address %s expect NumVfs %v, current NumVfs %v", iface.PciAddress, iface.NumVfs, ifaceStatus.NumVfs)
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

func needRebootIfVfio(state *sriovnetworkv1.SriovNetworkNodeState, driverMap DriverStateMapType) (needReboot bool) {
	driverState := driverMap[Vfio]
	if !driverState.DriverLoaded && driverState.NeedDriverFunc(state, driverState) {
		var err error
		needReboot, err = tryEnableIommuInKernelArgs()
		if err != nil {
			glog.Errorf("generic-plugin needRebootNode():fail to enable iommu in kernel args: %v", err)
		}
		if needReboot {
			glog.V(2).Infof("generic-plugin needRebootNode(): need reboot for enabling iommu kernel args")
		}
	}
	return needReboot
}

func needRebootNode(state *sriovnetworkv1.SriovNetworkNodeState, driverMap DriverStateMapType) (needReboot bool) {
	needReboot = needRebootIfVfio(state, driverMap)
	updateNode, err := utils.WriteSwitchdevConfFile(state)

	if err != nil {
		glog.Errorf("generic-plugin needRebootNode(): fail to write switchdev device config file")
	}
	if updateNode {
		glog.V(2).Infof("generic-plugin needRebootNode(): need reboot for updating switchdev device configuration")
	}
	needReboot = needReboot || updateNode
	return
}

// ////////////// for testing purposes only ///////////////////////
func (p *GenericPlugin) getDriverStateMap() DriverStateMapType {
	return p.DriverStateMap
}

func (p *GenericPlugin) loadDriverForTests(state *sriovnetworkv1.SriovNetworkNodeState) {
	for _, driverState := range p.DriverStateMap {
		if !driverState.DriverLoaded && driverState.NeedDriverFunc(state, driverState) {
			driverState.DriverLoaded = true
		}
	}
}

//////////////////////////////////////////////////////////////////
