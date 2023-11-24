package virtual

import (
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var PluginName = "virtual_plugin"

// VirtualPlugin Plugin type to use on a virtual platform
type VirtualPlugin struct {
	PluginName     string
	SpecVersion    string
	DesireState    *sriovnetworkv1.SriovNetworkNodeState
	LastState      *sriovnetworkv1.SriovNetworkNodeState
	LoadVfioDriver uint
	RunningOnHost  bool
	HostManager    host.HostManagerInterface
}

const (
	unloaded = iota
	loading
	loaded
)

// Initialize our plugin and set up initial values
func NewVirtualPlugin(runningOnHost bool) (plugin.VendorPlugin, error) {
	return &VirtualPlugin{
		PluginName:     PluginName,
		SpecVersion:    "1.0",
		LoadVfioDriver: unloaded,
		RunningOnHost:  runningOnHost,
		HostManager:    host.NewHostManager(runningOnHost),
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
	log.Log.Info("virtual-plugin OnNodeStateChange()")
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
	log.Log.Info("virtual-plugin Apply()", "desired-state", p.DesireState.Spec)

	if p.LoadVfioDriver == loading {
		// In virtual deployments of Kubernetes where the underlying virtualization platform does not support a virtualized iommu
		// the VFIO PCI driver needs to be loaded with a special flag.
		// This is the case for OpenStack deployments where the underlying virtualization platform is KVM.
		// NOTE: if VFIO was already loaded for some reason, we will not try to load it again with the new options.
		kernelArgs := "enable_unsafe_noiommu_mode=1"
		if err := p.HostManager.LoadKernelModule("vfio", kernelArgs); err != nil {
			log.Log.Error(err, "virtual-plugin Apply(): fail to load vfio kmod")
			return err
		}

		if err := p.HostManager.LoadKernelModule("vfio_pci"); err != nil {
			log.Log.Error(err, "virtual-plugin Apply(): fail to load vfio_pci kmod")
			return err
		}
		p.LoadVfioDriver = loaded
	}

	if p.LastState != nil {
		log.Log.Info("virtual-plugin Apply()", "last-state", p.LastState.Spec)
		if reflect.DeepEqual(p.LastState.Spec.Interfaces, p.DesireState.Spec.Interfaces) {
			log.Log.Info("virtual-plugin Apply(): nothing to apply")
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

func (p *VirtualPlugin) SetSystemdFlag() {
}

func (p *VirtualPlugin) IsSystemService() bool {
	return false
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
