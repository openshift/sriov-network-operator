package generic

import (
	"bytes"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var PluginName = "generic"

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
	PluginName          string
	SpecVersion         string
	DesireState         *sriovnetworkv1.SriovNetworkNodeState
	DriverStateMap      DriverStateMapType
	DesiredKernelArgs   map[string]bool
	helpers             helper.HostHelpersInterface
	skipVFConfiguration bool
}

type Option = func(c *genericPluginOptions)

// WithSkipVFConfiguration configures generic plugin to skip configuration of the VFs.
// In this case PFs will be configured and VFs are created only, VF configuration phase is skipped.
// VFs on the PF (if the PF is not ExternallyManaged) will have no driver after the plugin execution completes.
func WithSkipVFConfiguration() Option {
	return func(c *genericPluginOptions) {
		c.skipVFConfiguration = true
	}
}

type genericPluginOptions struct {
	skipVFConfiguration bool
}

const scriptsPath = "bindata/scripts/enable-kargs.sh"

// Initialize our plugin and set up initial values
func NewGenericPlugin(helpers helper.HostHelpersInterface, options ...Option) (plugin.VendorPlugin, error) {
	cfg := &genericPluginOptions{}
	for _, o := range options {
		o(cfg)
	}
	driverStateMap := make(map[uint]*DriverState)
	driverStateMap[Vfio] = &DriverState{
		DriverName:     vfioPciDriver,
		DeviceType:     consts.DeviceTypeVfioPci,
		VdpaType:       "",
		NeedDriverFunc: needDriverCheckDeviceType,
		DriverLoaded:   false,
	}
	driverStateMap[VirtioVdpa] = &DriverState{
		DriverName:     virtioVdpaDriver,
		DeviceType:     consts.DeviceTypeNetDevice,
		VdpaType:       consts.VdpaTypeVirtio,
		NeedDriverFunc: needDriverCheckVdpaType,
		DriverLoaded:   false,
	}
	driverStateMap[VhostVdpa] = &DriverState{
		DriverName:     vhostVdpaDriver,
		DeviceType:     consts.DeviceTypeNetDevice,
		VdpaType:       consts.VdpaTypeVhost,
		NeedDriverFunc: needDriverCheckVdpaType,
		DriverLoaded:   false,
	}
	return &GenericPlugin{
		PluginName:          PluginName,
		SpecVersion:         "1.0",
		DriverStateMap:      driverStateMap,
		DesiredKernelArgs:   make(map[string]bool),
		helpers:             helpers,
		skipVFConfiguration: cfg.skipVFConfiguration,
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
	log.Log.Info("generic plugin OnNodeStateChange()")
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
			log.Log.V(2).Info("loading driver", "name", driverState.DriverName)
			if err := p.helpers.LoadKernelModule(driverState.DriverName); err != nil {
				log.Log.Error(err, "generic plugin syncDriverState(): fail to load kmod", "name", driverState.DriverName)
				return err
			}
			driverState.DriverLoaded = true
		}
	}
	return nil
}

// Apply config change
func (p *GenericPlugin) Apply() error {
	log.Log.Info("generic plugin Apply()", "desiredState", p.DesireState.Spec)

	if err := p.syncDriverState(); err != nil {
		return err
	}

	// When calling from systemd do not try to chroot
	if !vars.UsingSystemdMode {
		exit, err := p.helpers.Chroot(consts.Host)
		if err != nil {
			return err
		}
		defer exit()
	}

	if err := p.helpers.ConfigSriovInterfaces(p.helpers, p.DesireState.Spec.Interfaces,
		p.DesireState.Status.Interfaces, p.skipVFConfiguration); err != nil {
		// Catch the "cannot allocate memory" error and try to use PCI realloc
		if errors.Is(err, syscall.ENOMEM) {
			p.addToDesiredKernelArgs(consts.KernelArgPciRealloc)
		}
		return err
	}

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
	log.Log.Info("generic plugin setKernelArg()")
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/sh", scriptsPath, karg)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// if grubby is not there log and assume kernel args are set correctly.
		if utils.IsCommandNotFound(err) {
			log.Log.Error(err, "generic plugin setKernelArg(): grubby or ostree command not found. Please ensure that kernel arg are set",
				"kargs", karg)
			return false, nil
		}
		log.Log.Error(err, "generic plugin setKernelArg(): fail to enable kernel arg", "karg", karg)
		return false, err
	}

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i > 0 {
			log.Log.Info("generic plugin setKernelArg(): need to reboot node for kernel arg", "karg", karg)
			return true, nil
		}
	}
	return false, err
}

// addToDesiredKernelArgs Should be called to queue a kernel arg to be added to the node.
func (p *GenericPlugin) addToDesiredKernelArgs(karg string) {
	if _, ok := p.DesiredKernelArgs[karg]; !ok {
		log.Log.Info("generic plugin addToDesiredKernelArgs(): Adding to desired kernel arg", "karg", karg)
		p.DesiredKernelArgs[karg] = false
	}
}

// syncDesiredKernelArgs Should be called to set all the kernel arguments. Returns bool if node update is needed.
func (p *GenericPlugin) syncDesiredKernelArgs() (bool, error) {
	needReboot := false
	if len(p.DesiredKernelArgs) == 0 {
		return false, nil
	}
	kargs, err := p.helpers.GetCurrentKernelArgs()
	if err != nil {
		return false, err
	}
	for desiredKarg, attempted := range p.DesiredKernelArgs {
		set := p.helpers.IsKernelArgsSet(kargs, desiredKarg)
		if !set {
			if attempted {
				log.Log.V(2).Info("generic plugin syncDesiredKernelArgs(): previously attempted to set kernel arg",
					"karg", desiredKarg)
			}
			// There is a case when we try to set the kernel argument here, the daemon could decide to not reboot because
			// the daemon encountered a potentially one-time error. However we always want to make sure that the kernel
			// argument is set once the daemon goes through node state sync again.
			update, err := setKernelArg(desiredKarg)
			if err != nil {
				log.Log.Error(err, "generic plugin syncDesiredKernelArgs(): fail to set kernel arg", "karg", desiredKarg)
				return false, err
			}
			if update {
				needReboot = true
				log.Log.V(2).Info("generic plugin syncDesiredKernelArgs(): need reboot for setting kernel arg", "karg", desiredKarg)
			}
			p.DesiredKernelArgs[desiredKarg] = true
		}
	}
	return needReboot, nil
}

func (p *GenericPlugin) needDrainNode(desired sriovnetworkv1.Interfaces, current sriovnetworkv1.InterfaceExts) (needDrain bool) {
	log.Log.V(2).Info("generic plugin needDrainNode()", "current", current, "desired", desired)

	needDrain = false
	for _, ifaceStatus := range current {
		configured := false
		for _, iface := range desired {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true
				if ifaceStatus.NumVfs == 0 {
					log.Log.V(2).Info("generic plugin needDrainNode(): no need drain, for PCI address, current NumVfs is 0",
						"address", iface.PciAddress)
					break
				}
				if sriovnetworkv1.NeedToUpdateSriov(&iface, &ifaceStatus) {
					log.Log.V(2).Info("generic plugin needDrainNode(): need drain, for PCI address request update",
						"address", iface.PciAddress)
					needDrain = true
					return
				}
				log.Log.V(2).Info("generic plugin needDrainNode(): no need drain,for PCI address",
					"address", iface.PciAddress, "expected-vfs", iface.NumVfs, "current-vfs", ifaceStatus.NumVfs)
			}
		}
		if !configured && ifaceStatus.NumVfs > 0 {
			// load the PF info
			pfStatus, exist, err := p.helpers.LoadPfsStatus(ifaceStatus.PciAddress)
			if err != nil {
				log.Log.Error(err, "generic plugin needDrainNode(): failed to load info about PF status for pci device",
					"address", ifaceStatus.PciAddress)
				continue
			}

			if !exist {
				log.Log.Info("generic plugin needDrainNode(): PF name with pci address has VFs configured but they weren't created by the sriov operator. Skipping drain",
					"name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			}

			if pfStatus.ExternallyManaged {
				log.Log.Info("generic plugin needDrainNode()(): PF name with pci address was externally created. Skipping drain",
					"name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			}

			log.Log.V(2).Info("generic plugin needDrainNode(): need drain since interface needs to be reset",
				"interface", ifaceStatus)
			needDrain = true
			return
		}
	}
	return
}

func (p *GenericPlugin) addVfioDesiredKernelArg(state *sriovnetworkv1.SriovNetworkNodeState) {
	driverState := p.DriverStateMap[Vfio]
	if !driverState.DriverLoaded && driverState.NeedDriverFunc(state, driverState) {
		p.addToDesiredKernelArgs(consts.KernelArgIntelIommu)
		p.addToDesiredKernelArgs(consts.KernelArgIommuPt)
	}
}

func (p *GenericPlugin) needRebootNode(state *sriovnetworkv1.SriovNetworkNodeState) (needReboot bool, err error) {
	needReboot = false
	p.addVfioDesiredKernelArg(state)

	updateNode, err := p.syncDesiredKernelArgs()
	if err != nil {
		log.Log.Error(err, "generic plugin needRebootNode(): failed to set the desired kernel arguments")
		return false, err
	}
	if updateNode {
		log.Log.V(2).Info("generic plugin needRebootNode(): need reboot for updating kernel arguments")
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
