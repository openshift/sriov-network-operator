package generic

import (
	"errors"
	"fmt"
	"syscall"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	hostTypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
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

type KargStateMapType map[string]bool

type GenericPlugin struct {
	PluginName              string
	SpecVersion             string
	DesireState             *sriovnetworkv1.SriovNetworkNodeState
	DriverStateMap          DriverStateMapType
	DesiredKernelArgs       KargStateMapType
	helpers                 helper.HostHelpersInterface
	skipVFConfiguration     bool
	skipBridgeConfiguration bool
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

// WithSkipBridgeConfiguration configures generic_plugin to skip configuration of the managed bridges
func WithSkipBridgeConfiguration() Option {
	return func(c *genericPluginOptions) {
		c.skipBridgeConfiguration = true
	}
}

type genericPluginOptions struct {
	skipVFConfiguration     bool
	skipBridgeConfiguration bool
}

const scriptsPath = "bindata/scripts/kargs.sh"

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

	// To maintain backward compatibility we don't remove the intel_iommu, iommu and pcirealloc
	// kernel args if they are configured
	kargs, err := helpers.GetCurrentKernelArgs()
	if err != nil {
		return nil, err
	}
	desiredKernelArgs := KargStateMapType{
		consts.KernelArgPciRealloc:       helpers.IsKernelArgsSet(kargs, consts.KernelArgPciRealloc),
		consts.KernelArgIntelIommu:       helpers.IsKernelArgsSet(kargs, consts.KernelArgIntelIommu),
		consts.KernelArgIommuPt:          helpers.IsKernelArgsSet(kargs, consts.KernelArgIommuPt),
		consts.KernelArgIommuPassthrough: helpers.IsKernelArgsSet(kargs, consts.KernelArgIommuPassthrough),
		consts.KernelArgRdmaShared:       false,
		consts.KernelArgRdmaExclusive:    false,
	}

	return &GenericPlugin{
		PluginName:              PluginName,
		SpecVersion:             "1.0",
		DriverStateMap:          driverStateMap,
		DesiredKernelArgs:       desiredKernelArgs,
		helpers:                 helpers,
		skipVFConfiguration:     cfg.skipVFConfiguration,
		skipBridgeConfiguration: cfg.skipBridgeConfiguration,
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

	needDrain = p.needDrainNode(new.Spec, new.Status)
	needReboot, err = p.needRebootNode(new)
	if err != nil {
		return needDrain, needReboot, err
	}

	if needReboot {
		needDrain = true
	}
	return
}

// CheckStatusChanges verify whether SriovNetworkNodeState CR status present changes on configured VFs.
func (p *GenericPlugin) CheckStatusChanges(current *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	log.Log.Info("generic-plugin CheckStatusChanges()")

	for _, iface := range current.Spec.Interfaces {
		found := false
		for _, ifaceStatus := range current.Status.Interfaces {
			// TODO: remove the check for ExternallyManaged - https://github.com/k8snetworkplumbingwg/sriov-network-operator/issues/632
			if iface.PciAddress == ifaceStatus.PciAddress && !iface.ExternallyManaged {
				found = true
				if sriovnetworkv1.NeedToUpdateSriov(&iface, &ifaceStatus) {
					log.Log.Info("CheckStatusChanges(): status changed for interface", "address", iface.PciAddress)
					return true, nil
				}
				break
			}
		}
		if !found {
			log.Log.Info("CheckStatusChanges(): no status found for interface", "address", iface.PciAddress)
		}
	}

	if p.shouldConfigureBridges() {
		if sriovnetworkv1.NeedToUpdateBridges(&current.Spec.Bridges, &current.Status.Bridges) {
			log.Log.Info("CheckStatusChanges(): bridge configuration needs to be updated")
			return true, nil
		}
	}

	shouldUpdate, err := p.shouldUpdateKernelArgs()
	if err != nil {
		log.Log.Error(err, "generic-plugin CheckStatusChanges(): failed to verify missing kernel arguments")
		return false, err
	}

	return shouldUpdate, nil
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
			p.enableDesiredKernelArgs(consts.KernelArgPciRealloc)
		}
		return err
	}

	if p.shouldConfigureBridges() {
		if err := p.helpers.ConfigureBridges(p.DesireState.Spec.Bridges, p.DesireState.Status.Bridges); err != nil {
			return err
		}
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

// editKernelArg Tries to add the kernel args via ostree or grubby.
func editKernelArg(helper helper.HostHelpersInterface, mode, karg string) error {
	log.Log.Info("generic plugin editKernelArg()", "mode", mode, "karg", karg)
	_, _, err := helper.RunCommand("/bin/sh", scriptsPath, mode, karg)
	if err != nil {
		// if grubby is not there log and assume kernel args are set correctly.
		if utils.IsCommandNotFound(err) {
			log.Log.Error(err, "generic plugin editKernelArg(): grubby or ostree command not found. Please ensure that kernel arg are correct",
				"kargs", karg)
			return nil
		}
		log.Log.Error(err, "generic plugin editKernelArg(): fail to edit kernel arg", "karg", karg)
		return err
	}
	return nil
}

// enableDesiredKernelArgs Should be called to mark a kernel arg as enabled.
func (p *GenericPlugin) enableDesiredKernelArgs(karg string) {
	log.Log.Info("generic plugin enableDesiredKernelArgs(): enable kernel arg", "karg", karg)
	p.DesiredKernelArgs[karg] = true
}

// disableDesiredKernelArgs Should be called to mark a kernel arg as disabled.
func (p *GenericPlugin) disableDesiredKernelArgs(karg string) {
	log.Log.Info("generic plugin disableDesiredKernelArgs(): disable kernel arg", "karg", karg)
	p.DesiredKernelArgs[karg] = false
}

// shouldUpdateKernelArgs returns true if the DesiredKernelArgs state is not equal to the running kernel args in the system
func (p *GenericPlugin) shouldUpdateKernelArgs() (bool, error) {
	kargs, err := p.helpers.GetCurrentKernelArgs()
	if err != nil {
		return false, err
	}

	for karg, kargState := range p.DesiredKernelArgs {
		if kargState && !p.helpers.IsKernelArgsSet(kargs, karg) {
			return true, nil
		}

		if !kargState && p.helpers.IsKernelArgsSet(kargs, karg) {
			return true, nil
		}
	}
	return false, nil
}

// syncDesiredKernelArgs should be called to set all the kernel arguments. Returns bool if node update is needed.
func (p *GenericPlugin) syncDesiredKernelArgs() (bool, error) {
	kargs, err := p.helpers.GetCurrentKernelArgs()
	if err != nil {
		return false, err
	}

	needReboot := false
	for karg, kargState := range p.DesiredKernelArgs {
		if kargState {
			err = editKernelArg(p.helpers, "add", karg)
			if err != nil {
				log.Log.Error(err, "generic-plugin syncDesiredKernelArgs(): fail to set kernel arg", "karg", karg)
				return false, err
			}

			if !p.helpers.IsKernelArgsSet(kargs, karg) {
				needReboot = true
			}
		} else {
			err = editKernelArg(p.helpers, "remove", karg)
			if err != nil {
				log.Log.Error(err, "generic-plugin syncDesiredKernelArgs(): fail to remove kernel arg", "karg", karg)
				return false, err
			}

			if p.helpers.IsKernelArgsSet(kargs, karg) {
				needReboot = true
			}
		}
	}
	return needReboot, nil
}

func (p *GenericPlugin) needDrainNode(desired sriovnetworkv1.SriovNetworkNodeStateSpec, current sriovnetworkv1.SriovNetworkNodeStateStatus) bool {
	log.Log.V(2).Info("generic plugin needDrainNode()", "current", current, "desired", desired)

	if p.needToUpdateVFs(desired, current) {
		return true
	}

	if p.shouldConfigureBridges() {
		if sriovnetworkv1.NeedToUpdateBridges(&desired.Bridges, &current.Bridges) {
			log.Log.V(2).Info("generic plugin needDrainNode(): need drain since bridge configuration needs to be updated")
			return true
		}
	}
	return false
}

func (p *GenericPlugin) needToUpdateVFs(desired sriovnetworkv1.SriovNetworkNodeStateSpec, current sriovnetworkv1.SriovNetworkNodeStateStatus) bool {
	for _, ifaceStatus := range current.Interfaces {
		configured := false
		for _, iface := range desired.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true
				if ifaceStatus.NumVfs == 0 {
					log.Log.V(2).Info("generic plugin needToUpdateVFs(): no need drain, for PCI address, current NumVfs is 0",
						"address", iface.PciAddress)
					break
				}
				if sriovnetworkv1.NeedToUpdateSriov(&iface, &ifaceStatus) {
					log.Log.V(2).Info("generic plugin needToUpdateVFs(): need drain, for PCI address request update",
						"address", iface.PciAddress)
					return true
				}
				log.Log.V(2).Info("generic plugin needToUpdateVFs(): no need drain,for PCI address",
					"address", iface.PciAddress, "expected-vfs", iface.NumVfs, "current-vfs", ifaceStatus.NumVfs)
			}
		}
		if !configured && ifaceStatus.NumVfs > 0 {
			// load the PF info
			pfStatus, exist, err := p.helpers.LoadPfsStatus(ifaceStatus.PciAddress)
			if err != nil {
				log.Log.Error(err, "generic plugin needToUpdateVFs(): failed to load info about PF status for pci device",
					"address", ifaceStatus.PciAddress)
				continue
			}

			if !exist {
				log.Log.Info("generic plugin needToUpdateVFs(): PF name with pci address has VFs configured but they weren't created by the sriov operator. Skipping drain",
					"name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			}

			if pfStatus.ExternallyManaged {
				log.Log.Info("generic plugin needToUpdateVFs(): PF name with pci address was externally created. Skipping drain",
					"name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			}

			log.Log.V(2).Info("generic plugin needToUpdateVFs(): need drain since interface needs to be reset",
				"interface", ifaceStatus)
			return true
		}
	}
	return false
}

func (p *GenericPlugin) shouldConfigureBridges() bool {
	return vars.ManageSoftwareBridges && !p.skipBridgeConfiguration
}

func (p *GenericPlugin) addVfioDesiredKernelArg(state *sriovnetworkv1.SriovNetworkNodeState) {
	driverState := p.DriverStateMap[Vfio]

	kernelArgFnByCPUVendor := map[hostTypes.CPUVendor]func(){
		hostTypes.CPUVendorIntel: func() {
			p.enableDesiredKernelArgs(consts.KernelArgIntelIommu)
			p.enableDesiredKernelArgs(consts.KernelArgIommuPt)
		},
		hostTypes.CPUVendorAMD: func() {
			p.enableDesiredKernelArgs(consts.KernelArgIommuPt)
		},
		hostTypes.CPUVendorARM: func() {
			p.enableDesiredKernelArgs(consts.KernelArgIommuPassthrough)
		},
	}

	if !driverState.DriverLoaded && driverState.NeedDriverFunc(state, driverState) {
		cpuVendor, err := p.helpers.GetCPUVendor()
		if err != nil {
			log.Log.Error(err, "can't get CPU vendor, falling back to Intel")
			cpuVendor = hostTypes.CPUVendorIntel
		}

		addKernelArgFn := kernelArgFnByCPUVendor[cpuVendor]
		if addKernelArgFn != nil {
			addKernelArgFn()
		}
	}
}

func (p *GenericPlugin) configRdmaKernelArg(state *sriovnetworkv1.SriovNetworkNodeState) error {
	if state.Spec.System.RdmaMode == "" {
		p.disableDesiredKernelArgs(consts.KernelArgRdmaExclusive)
		p.disableDesiredKernelArgs(consts.KernelArgRdmaShared)
	} else if state.Spec.System.RdmaMode == "shared" {
		p.enableDesiredKernelArgs(consts.KernelArgRdmaShared)
		p.disableDesiredKernelArgs(consts.KernelArgRdmaExclusive)
	} else if state.Spec.System.RdmaMode == "exclusive" {
		p.enableDesiredKernelArgs(consts.KernelArgRdmaExclusive)
		p.disableDesiredKernelArgs(consts.KernelArgRdmaShared)
	} else {
		err := fmt.Errorf("unexpected rdma mode: %s", state.Spec.System.RdmaMode)
		log.Log.Error(err, "generic-plugin configRdmaKernelArg(): failed to configure kernel arguments for rdma")
		return err
	}

	return p.helpers.SetRDMASubsystem(state.Spec.System.RdmaMode)
}

func (p *GenericPlugin) needRebootNode(state *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	needReboot := false

	p.addVfioDesiredKernelArg(state)
	err := p.configRdmaKernelArg(state)
	if err != nil {
		return false, err
	}

	needReboot, err = p.syncDesiredKernelArgs()
	if err != nil {
		log.Log.Error(err, "generic-plugin needRebootNode(): failed to set the desired kernel arguments")
		return false, err
	}
	if needReboot {
		log.Log.V(2).Info("generic-plugin needRebootNode(): need reboot for updating kernel arguments")
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
