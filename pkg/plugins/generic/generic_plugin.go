package generic

import (
	"bytes"
	"errors"
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
	VhostVdpa
)

// driver name
const (
	vfioPciDriver    = "vfio_pci"
	virtioVdpaDriver = "virtio_vdpa"
	vhostVdpaDriver  = "vhost_vdpa"
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
	PluginName        string
	SpecVersion       string
	DesireState       *sriovnetworkv1.SriovNetworkNodeState
	LastState         *sriovnetworkv1.SriovNetworkNodeState
	DriverStateMap    DriverStateMapType
	DesiredKernelArgs map[string]bool
	RunningOnHost     bool
	HostManager       host.HostManagerInterface
	StoreManager      utils.StoreManagerInterface
}

const scriptsPath = "bindata/scripts/enable-kargs.sh"

// Initialize our plugin and set up initial values
func NewGenericPlugin(runningOnHost bool, hostManager host.HostManagerInterface, storeManager utils.StoreManagerInterface) (plugin.VendorPlugin, error) {
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
	driverStateMap[VhostVdpa] = &DriverState{
		DriverName:     vhostVdpaDriver,
		DeviceType:     constants.DeviceTypeNetDevice,
		VdpaType:       constants.VdpaTypeVhost,
		NeedDriverFunc: needDriverCheckVdpaType,
		DriverLoaded:   false,
	}
	return &GenericPlugin{
		PluginName:        PluginName,
		SpecVersion:       "1.0",
		DriverStateMap:    driverStateMap,
		DesiredKernelArgs: make(map[string]bool),
		RunningOnHost:     runningOnHost,
		HostManager:       hostManager,
		StoreManager:      storeManager,
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

	needDrain = p.needDrainNode(new.Spec.Interfaces, new.Status.Interfaces)
	needReboot, err = p.needRebootNode(new)
	if err != nil {
		return needDrain, needReboot, err
	}

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
		// Catch the "cannot allocate memory" error and try to use PCI realloc
		if errors.Is(err, syscall.ENOMEM) {
			p.addToDesiredKernelArgs(utils.KernelArgPciRealloc)
		}
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

// setKernelArg Tries to add the kernel args via ostree or grubby.
func setKernelArg(karg string) (bool, error) {
	glog.Info("generic-plugin setKernelArg()")
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/sh", scriptsPath, karg)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// if grubby is not there log and assume kernel args are set correctly.
		if isCommandNotFound(err) {
			glog.Errorf("generic-plugin setKernelArg(): grubby or ostree command not found. Please ensure that kernel arg %s are set", karg)
			return false, nil
		}
		glog.Errorf("generic-plugin setKernelArg(): fail to enable kernel arg %s: %v", karg, err)
		return false, err
	}

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i > 0 {
			glog.Infof("generic-plugin setKernelArg(): need to reboot node for kernel arg %s", karg)
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

// addToDesiredKernelArgs Should be called to queue a kernel arg to be added to the node.
func (p *GenericPlugin) addToDesiredKernelArgs(karg string) {
	if _, ok := p.DesiredKernelArgs[karg]; !ok {
		glog.Infof("generic-plugin addToDesiredKernelArgs(): Adding %s to desired kernel arg", karg)
		p.DesiredKernelArgs[karg] = false
	}
}

// syncDesiredKernelArgs Should be called to set all the kernel arguments. Returns bool if node update is needed.
func (p *GenericPlugin) syncDesiredKernelArgs() (bool, error) {
	needReboot := false
	if len(p.DesiredKernelArgs) == 0 {
		return false, nil
	}
	kargs, err := utils.GetCurrentKernelArgs(false)
	if err != nil {
		return false, err
	}
	for desiredKarg, attempted := range p.DesiredKernelArgs {
		set := utils.IsKernelArgsSet(kargs, desiredKarg)
		if !set {
			if attempted {
				glog.V(2).Infof("generic-plugin syncDesiredKernelArgs(): previously attempted to set kernel arg %s", desiredKarg)
			}
			// There is a case when we try to set the kernel argument here, the daemon could decide to not reboot because
			// the daemon encountered a potentially one-time error. However we always want to make sure that the kernel
			// argument is set once the daemon goes through node state sync again.
			update, err := setKernelArg(desiredKarg)
			if err != nil {
				glog.Errorf("generic-plugin syncDesiredKernelArgs(): fail to set kernel arg %s: %v", desiredKarg, err)
				return false, err
			}
			if update {
				needReboot = true
				glog.V(2).Infof("generic-plugin syncDesiredKernelArgs(): need reboot for setting kernel arg %s", desiredKarg)
			}
			p.DesiredKernelArgs[desiredKarg] = true
		}
	}
	return needReboot, nil
}

func (p *GenericPlugin) needDrainNode(desired sriovnetworkv1.Interfaces, current sriovnetworkv1.InterfaceExts) (needDrain bool) {
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
			// load the PF info
			pfStatus, exist, err := p.StoreManager.LoadPfsStatus(ifaceStatus.PciAddress)
			if err != nil {
				glog.Errorf("generic-plugin needDrainNode(): failed to load info about PF status for pci address %s: %v", ifaceStatus.PciAddress, err)
				continue
			}

			if !exist {
				glog.Infof("generic-plugin needDrainNode(): PF name %s with pci address %s has VFs configured but they weren't created by the sriov operator. Skipping drain",
					ifaceStatus.Name,
					ifaceStatus.PciAddress)
				continue
			}

			if pfStatus.ExternallyManaged {
				glog.Infof("generic-plugin needDrainNode()(): PF name %s with pci address %s was externally created. Skipping drain",
					ifaceStatus.Name,
					ifaceStatus.PciAddress)
				continue
			}

			glog.V(2).Infof("generic-plugin needDrainNode(): need drain, %v needs to be reset", ifaceStatus)
			needDrain = true
			return
		}
	}
	return
}

func (p *GenericPlugin) addVfioDesiredKernelArg(state *sriovnetworkv1.SriovNetworkNodeState) {
	driverState := p.DriverStateMap[Vfio]
	if !driverState.DriverLoaded && driverState.NeedDriverFunc(state, driverState) {
		p.addToDesiredKernelArgs(utils.KernelArgIntelIommu)
		p.addToDesiredKernelArgs(utils.KernelArgIommuPt)
	}
}

func (p *GenericPlugin) needRebootNode(state *sriovnetworkv1.SriovNetworkNodeState) (needReboot bool, err error) {
	needReboot = false
	p.addVfioDesiredKernelArg(state)

	updateNode, err := p.syncDesiredKernelArgs()
	if err != nil {
		glog.Errorf("generic-plugin needRebootNode(): failed to set the desired kernel arguments")
		return false, err
	}
	if updateNode {
		glog.V(2).Infof("generic-plugin needRebootNode(): need reboot for updating kernel arguments")
		needReboot = true
	}

	updateNode, err = utils.WriteSwitchdevConfFile(state)
	if err != nil {
		glog.Errorf("generic-plugin needRebootNode(): fail to write switchdev device config file")
		return false, err
	}
	if updateNode {
		glog.V(2).Infof("generic-plugin needRebootNode(): need reboot for updating switchdev device configuration")
		needReboot = true
	}

	return needReboot, nil
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
