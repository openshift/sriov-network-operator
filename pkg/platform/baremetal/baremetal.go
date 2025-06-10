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

var VendorPluginMap = map[string]func(helpers helper.HostHelpersInterface) (plugin.VendorPlugin, error){
	"8086": intelplugin.NewIntelPlugin,
	"15b3": mellanoxplugin.NewMellanoxPlugin,
}

type Baremetal struct {
	hostHelpers helper.HostHelpersInterface
}

func New(hostHelpers helper.HostHelpersInterface) (*Baremetal, error) {
	return &Baremetal{
		hostHelpers: hostHelpers,
	}, nil
}

func (bm *Baremetal) Init() error {
	return nil
}

func (bm *Baremetal) GetHostHelpers() helper.HostHelpersInterface {
	return bm.hostHelpers
}

func (bm *Baremetal) DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	return bm.hostHelpers.DiscoverSriovDevices(bm.hostHelpers)
}

func (bm *Baremetal) DiscoverBridges() (sriovnetworkv1.Bridges, error) {
	if vars.ManageSoftwareBridges {
		return bm.hostHelpers.DiscoverBridges()
	}
	return sriovnetworkv1.Bridges{}, nil
}

func (bm *Baremetal) GetPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) (plugin.VendorPlugin, []plugin.VendorPlugin, error) {
	additionalPlugins, err := bm.loadVendorPlugins(ns)
	if err != nil {
		return nil, nil, err
	}

	clusterOrchestrator, err := orchestrator.New()
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

func (bm *Baremetal) SystemdGetPlugin(phase string) (plugin.VendorPlugin, error) {
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

func (bm *Baremetal) loadVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) ([]plugin.VendorPlugin, error) {
	vendorPluginsMap := map[string]plugin.VendorPlugin{}

	for _, iface := range ns.Status.Interfaces {
		if val, ok := VendorPluginMap[iface.Vendor]; ok {
			plug, err := val(bm.hostHelpers)
			if err != nil {
				if plug != nil {
					log.Log.Error(err, "loadVendorPlugins(): failed to load plugin", "plugin-name", plug.Name())
				}
				return nil, fmt.Errorf("loadVendorPlugins(): failed to load vendor plugin for vendorID %s error: %v", iface.Vendor, err)
			}

			if _, ok := vendorPluginsMap[plug.Name()]; !ok {
				vendorPluginsMap[plug.Name()] = plug
			}
		}
	}

	vendorPlugins := []plugin.VendorPlugin{}
	for _, val := range vendorPluginsMap {
		vendorPlugins = append(vendorPlugins, val)
	}
	return vendorPlugins, nil
}
