package bridge

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/bridge/ovs"
	ovsStorePkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/bridge/ovs/store"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

type bridge struct {
	ovs ovs.Interface
}

// New return default implementation of the BridgeInterface
func New() types.BridgeInterface {
	return &bridge{
		ovs: ovs.New(ovsStorePkg.New()),
	}
}

// DiscoverBridges returns information about managed bridges on the host
func (b *bridge) DiscoverBridges() (sriovnetworkv1.Bridges, error) {
	log.Log.V(2).Info("DiscoverBridges(): discover managed bridges")
	discoveredOVSBridges, err := b.ovs.GetOVSBridges(context.Background())
	if err != nil {
		log.Log.Error(err, "DiscoverBridges(): failed to discover managed OVS bridges")
		return sriovnetworkv1.Bridges{}, err
	}
	return sriovnetworkv1.Bridges{OVS: discoveredOVSBridges}, nil
}

// ConfigureBridge configure managed bridges for the host
func (b *bridge) ConfigureBridges(bridgesSpec sriovnetworkv1.Bridges, bridgesStatus sriovnetworkv1.Bridges) error {
	log.Log.V(1).Info("ConfigureBridges(): configure bridges")
	if len(bridgesSpec.OVS) == 0 && len(bridgesStatus.OVS) == 0 {
		// there are no reported OVS bridges in the status and the spec doesn't contains bridges.
		// no need to validated configuration
		log.Log.V(2).Info("ConfigureBridges(): configuration is not required")
		return nil
	}
	for _, curBr := range bridgesStatus.OVS {
		found := false
		for _, desiredBr := range bridgesSpec.OVS {
			if curBr.Name == desiredBr.Name {
				found = true
				break
			}
		}
		if !found {
			if err := b.ovs.RemoveOVSBridge(context.Background(), curBr.Name); err != nil {
				log.Log.Error(err, "ConfigureBridges(): failed to remove OVS bridge", "bridge", curBr.Name)
				return err
			}
		}
	}
	// create bridges, existing bridges will be updated only if the new config doesn't match current config
	for i := range bridgesSpec.OVS {
		desiredBr := bridgesSpec.OVS[i]
		if err := b.ovs.CreateOVSBridge(context.Background(), &desiredBr); err != nil {
			log.Log.Error(err, "ConfigureBridges(): failed to create OVS bridge", "bridge", desiredBr.Name)
			return err
		}
	}
	return nil
}

// DetachInterfaceFromManagedBridge detach interface from a managed bridge,
// this step is required before applying some configurations to PF, e.g. changing of eSwitch mode.
// The function detach interface from managed bridges only.
func (b *bridge) DetachInterfaceFromManagedBridge(pciAddr string) error {
	log.Log.V(1).Info("DetachInterfaceFromManagedBridge(): detach interface", "pciAddr", pciAddr)
	if err := b.ovs.RemoveInterfaceFromOVSBridge(context.Background(), pciAddr); err != nil {
		log.Log.Error(err, "DetachInterfaceFromManagedBridge(): failed to detach interface from OVS bridge", "pciAddr", pciAddr)
		return err
	}
	return nil
}
