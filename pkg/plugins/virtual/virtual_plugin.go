package virtual

import (
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	consts "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
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
		LoadVfioDriver: unloaded,
		helpers:        helper,
	}, nil
}

// Name returns the name of the plugin
func (p *VirtualPlugin) Name() string {
	return p.PluginName
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is created or updated, return if need dain and/or reboot node
func (p *VirtualPlugin) OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error) {
	log.Log.Info("virtual plugin OnNodeStateChange()")
	p.DesireState = new

	if p.LoadVfioDriver != loaded {
		if needVfioDriver(new) {
			p.LoadVfioDriver = loading
		}
	}

	return p.needDrainNode(new.Spec, new.Status), false, nil
}

func (p *VirtualPlugin) needDrainNode(desired sriovnetworkv1.SriovNetworkNodeStateSpec, current sriovnetworkv1.SriovNetworkNodeStateStatus) bool {
	log.Log.V(2).Info("virtual plugin needDrainNode()", "current", current, "desired", desired)
	for _, ifaceStatus := range current.Interfaces {
		configured := false
		for _, iface := range desired.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true
				break
			}
		}

		// if the interface is not configured, but it was before by the sriov operator, we need to drain the node
		if !configured {
			// load the PF info
			pfStatus, exist, err := p.helpers.LoadPfsStatus(ifaceStatus.PciAddress)
			if err != nil {
				log.Log.Error(err, "virtual plugin needDrainNode(): failed to load info about PF status for pci device",
					"address", ifaceStatus.PciAddress)
				continue
			}

			if !exist {
				log.Log.Info("virtual plugin needDrainNode(): PF name with pci address has VFs configured but they weren't created by the sriov operator. Skipping drain",
					"name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			}

			if pfStatus.ExternallyManaged {
				log.Log.Info("virtual plugin needDrainNode(): PF name with pci address was externally created. Skipping drain",
					"name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			}

			log.Log.V(2).Info("virtual plugin needDrainNode(): need drain since interface needs to be reset",
				"interface", ifaceStatus)
			return true
		}
	}
	return false
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
		if equality.Semantic.DeepEqual(p.LastState.Spec.Interfaces, p.DesireState.Spec.Interfaces) {
			log.Log.Info("virtual plugin Apply(): nothing to apply")
			return nil
		}
	}
	exit, err := p.helpers.Chroot(consts.Host)
	if err != nil {
		return err
	}
	defer exit()
	if err := p.helpers.ConfigSriovDevicesVirtual(p.helpers, p.DesireState.Spec.Interfaces, p.DesireState.Status.Interfaces); err != nil {
		return err
	}
	p.LastState = &sriovnetworkv1.SriovNetworkNodeState{}
	*p.LastState = *p.DesireState

	return nil
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
