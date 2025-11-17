package baremetal

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	genericplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	intelplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/intel"
	k8splugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/k8s"
	mellanoxplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/mellanox"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// VendorPluginMap maps PCI vendor IDs to their corresponding plugin constructor functions.
// Supported vendors:
//   - "8086": Intel
//   - "15b3": Mellanox/NVIDIA
var VendorPluginMap = map[string]func(helpers helper.HostHelpersInterface) (plugin.VendorPlugin, error){
	"8086": intelplugin.NewIntelPlugin,
	"15b3": mellanoxplugin.NewMellanoxPlugin,
}

// Baremetal implements the platform.Interface for bare metal platforms.
// It handles SR-IOV device discovery and plugin loading for physical hardware.
type Baremetal struct {
	hostHelpers helper.HostHelpersInterface
}

// New creates a new Baremetal platform instance.
// Returns a configured Baremetal platform or an error if initialization fails.
func New(hostHelpers helper.HostHelpersInterface) (*Baremetal, error) {
	return &Baremetal{
		hostHelpers: hostHelpers,
	}, nil
}

// Init initializes the baremetal platform.
// For baremetal, no special initialization is required.
func (bm *Baremetal) Init() error {
	return nil
}

// Name returns the name of the baremetal platform.
func (bm *Baremetal) Name() string {
	return "Baremetal"
}

// DiscoverSriovDevices discovers all SR-IOV capable devices on the baremetal host.
func (bm *Baremetal) DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	return bm.hostHelpers.DiscoverSriovDevices(bm.hostHelpers)
}

// DiscoverBridges discovers software bridges on the baremetal host.
// The caller should check vars.ManageSoftwareBridges before calling this method.
func (bm *Baremetal) DiscoverBridges() (sriovnetworkv1.Bridges, error) {
	return bm.hostHelpers.DiscoverBridges()
}

// GetVendorPlugins loads and returns the appropriate plugins for the baremetal platform.
// Returns the generic plugin as the main plugin, and vendor-specific plugins (Intel, Mellanox)
// plus the k8s plugin (for Kubernetes clusters) as additional plugins.
func (bm *Baremetal) GetVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) (plugin.VendorPlugin, []plugin.VendorPlugin, error) {
	additionalPlugins, err := bm.loadVendorPlugins(ns)
	if err != nil {
		return nil, nil, err
	}

	clusterOrchestrator, err := orchestrator.New(vars.ClusterType)
	if err != nil {
		return nil, nil, err
	}

	if clusterOrchestrator.ClusterType() == consts.ClusterTypeKubernetes {
		k8sPlugin, err := k8splugin.NewK8sPlugin(bm.hostHelpers)
		if err != nil {
			return nil, nil, err
		}
		additionalPlugins = append(additionalPlugins, k8sPlugin)
	}

	genericPlugin, err := genericplugin.NewGenericPlugin(bm.hostHelpers)
	if err != nil {
		return nil, nil, err
	}

	return genericPlugin, additionalPlugins, nil
}

// SystemdGetVendorPlugin returns the appropriate plugin for systemd mode based on the phase.
// For PhasePre, returns a generic plugin that skips VF and bridge configuration.
// For PhasePost, returns a full generic plugin.
func (bm *Baremetal) SystemdGetVendorPlugin(phase string) (plugin.VendorPlugin, error) {
	switch phase {
	case consts.PhasePre:
		return genericplugin.NewGenericPlugin(bm.hostHelpers,
			genericplugin.WithSkipVFConfiguration(),
			genericplugin.WithSkipBridgeConfiguration())
	case consts.PhasePost:
		return genericplugin.NewGenericPlugin(bm.hostHelpers)
	default:
		return nil, fmt.Errorf("invalid phase %s", phase)
	}
}

// loadVendorPlugins loads vendor-specific plugins based on the detected device vendors.
// Scans the node state interfaces and loads the appropriate vendor plugin for each unique vendor.
func (bm *Baremetal) loadVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) ([]plugin.VendorPlugin, error) {
	loadedPluginsMap := map[string]plugin.VendorPlugin{}

	for _, iface := range ns.Status.Interfaces {
		if val, ok := VendorPluginMap[iface.Vendor]; ok {
			plug, err := val(bm.hostHelpers)
			if err != nil {
				if plug != nil {
					log.Log.Error(err, "loadVendorPlugins(): failed to load plugin", "plugin-name", plug.Name())
				}
				return nil, fmt.Errorf("loadVendorPlugins(): failed to load vendor plugin for vendorID %s error: %v", iface.Vendor, err)
			}

			if _, ok := loadedPluginsMap[plug.Name()]; !ok {
				loadedPluginsMap[plug.Name()] = plug
			}
		}
	}

	vendorPlugins := []plugin.VendorPlugin{}
	for _, val := range loadedPluginsMap {
		vendorPlugins = append(vendorPlugins, val)
	}
	return vendorPlugins, nil
}
