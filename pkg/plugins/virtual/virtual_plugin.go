package virtual

import (
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	consts "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var PluginName = "virtual"

// VirtualPlugin Plugin type to use on a virtual platform
type VirtualPlugin struct {
	PluginName     string
	SpecVersion    string
	DesireState    *sriovnetworkv1.SriovNetworkNodeState
	LastState      *sriovnetworkv1.SriovNetworkNodeState
	LoadVfioDriver uint
	helpers        helper.HostHelpersInterface
}

const (
	unloaded = iota
	loading
	loaded
)

// Initialize our plugin and set up initial values
func NewVirtualPlugin(helper helper.HostHelpersInterface) (plugin.VendorPlugin, error) {
	return &VirtualPlugin{
		PluginName:     PluginName,
		SpecVersion:    "1.0",
		LoadVfioDriver: unloaded,
		helpers:        helper,
	}, nil
}

// Name returns the name of the plugin
func (p *VirtualPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *VirtualPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is created or updated, return if need dain and/or reboot node
func (p *VirtualPlugin) OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	log.Log.Info("virtual plugin OnNodeStateChange()")
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

// OnNodeStatusChange verify whether SriovNetworkNodeState CR status present changes on configured VFs.
func (p *VirtualPlugin) CheckStatusChanges(*sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	return false, nil
}

// Apply config change
func (p *VirtualPlugin) Apply() error {
	log.Log.Info("virtual plugin Apply()", "desired-state", p.DesireState.Spec)

	if p.LoadVfioDriver == loading {
		// In virtual deployments of Kubernetes where the underlying virtualization platform does not support a virtualized iommu
		// the VFIO PCI driver needs to be loaded with a special flag.
		// This is the case for OpenStack deployments where the underlying virtualization platform is KVM.
		// NOTE: if VFIO was already loaded for some reason, we will not try to load it again with the new options.
		kernelArgs := "enable_unsafe_noiommu_mode=1"
		if err := p.helpers.LoadKernelModule("vfio", kernelArgs); err != nil {
			log.Log.Error(err, "virtual plugin Apply(): fail to load vfio kmod")
			return err
		}

		if err := p.helpers.LoadKernelModule("vfio_pci"); err != nil {
			log.Log.Error(err, "virtual plugin Apply(): fail to load vfio_pci kmod")
			return err
		}
		p.LoadVfioDriver = loaded
	}

	if p.LastState != nil {
		log.Log.Info("virtual plugin Apply()", "last-state", p.LastState.Spec)
		if reflect.DeepEqual(p.LastState.Spec.Interfaces, p.DesireState.Spec.Interfaces) {
			log.Log.Info("virtual plugin Apply(): nothing to apply")
			return nil
		}
	}
	exit, err := p.helpers.Chroot(consts.Host)
	if err != nil {
		return err
	}
	defer exit()
	if err := syncNodeStateVirtual(p.DesireState, p.helpers); err != nil {
		return err
	}
	p.LastState = &sriovnetworkv1.SriovNetworkNodeState{}
	*p.LastState = *p.DesireState

	return nil
}

func (p *VirtualPlugin) SetSystemdFlag() {
}

func (p *VirtualPlugin) IsSystemService() bool {
	return false
}

func needVfioDriver(state *sriovnetworkv1.SriovNetworkNodeState) bool {
	for _, iface := range state.Spec.Interfaces {
		for i := range iface.VfGroups {
			if iface.VfGroups[i].DeviceType == consts.DeviceTypeVfioPci {
				return true
			}
		}
	}
	return false
}

// syncNodeStateVirtual attempt to update the node state to match the desired state in virtual platforms
func syncNodeStateVirtual(newState *sriovnetworkv1.SriovNetworkNodeState, helpers helper.HostHelpersInterface) error {
	var err error
	for _, ifaceStatus := range newState.Status.Interfaces {
		for _, iface := range newState.Spec.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				if !needUpdateVirtual(&iface, &ifaceStatus) {
					log.Log.V(2).Info("syncNodeStateVirtual(): no need update interface", "address", iface.PciAddress)
					break
				}
				if err = helpers.ConfigSriovDeviceVirtual(&iface); err != nil {
					log.Log.Error(err, "syncNodeStateVirtual(): fail to config sriov interface", "address", iface.PciAddress)
					return err
				}
				break
			}
		}
	}
	return nil
}

func needUpdateVirtual(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) bool {
	// The device MTU is set by the platform
	// The NumVfs is always 1
	if iface.NumVfs > 0 {
		for _, vf := range ifaceStatus.VFs {
			ingroup := false
			for _, group := range iface.VfGroups {
				if sriovnetworkv1.IndexInRange(vf.VfID, group.VfRange) {
					ingroup = true
					if group.DeviceType != consts.DeviceTypeNetDevice {
						if group.DeviceType != vf.Driver {
							log.Log.V(2).Info("needUpdateVirtual(): Driver needs update",
								"desired", group.DeviceType, "current", vf.Driver)
							return true
						}
					} else {
						if sriovnetworkv1.StringInArray(vf.Driver, vars.DpdkDrivers) {
							log.Log.V(2).Info("needUpdateVirtual(): Driver needs update",
								"desired", group.DeviceType, "current", vf.Driver)
							return true
						}
					}
					break
				}
			}
			if !ingroup && sriovnetworkv1.StringInArray(vf.Driver, vars.DpdkDrivers) {
				// VF which has DPDK driver loaded but not in any group, needs to be reset to default driver.
				return true
			}
		}
	}
	return false
}
