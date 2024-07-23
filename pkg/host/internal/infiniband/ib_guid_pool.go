package infiniband

import (
	"fmt"
	"net"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

// ibGUIDPool is an interface that returns the GUID, allocated for a specific VF id of the specific PF
type ibGUIDPool interface {
	// GetVFGUID returns the GUID, allocated for a specific VF id of the specific PF
	// If no guid pool exists for the given pfPciAddr, returns an error
	// If no guids are available for the given VF id, returns an error
	GetVFGUID(pfPciAddr string, vfID int) (net.HardwareAddr, error)
}

type ibGUIDPoolImpl struct {
	guidConfigs map[string]ibPfGUIDConfig
}

// newIbGUIDPool returns an instance of ibGUIDPool
func newIbGUIDPool(configPath string, netlinkLib netlink.NetlinkLib, networkHelper types.NetworkInterface) (ibGUIDPool, error) {
	// All validation for the config file is done in the getIbGUIDConfig function
	configs, err := getIbGUIDConfig(configPath, netlinkLib, networkHelper)
	if err != nil {
		return nil, fmt.Errorf("failed to create ib guid pool: %w", err)
	}

	return &ibGUIDPoolImpl{guidConfigs: configs}, nil
}

// GetVFGUID returns the GUID, allocated for a specific VF id of the specific PF
// If no guid pool exists for the given pfPciAddr, returns an error
// If no guids are available for the given VF id, returns an error
func (p *ibGUIDPoolImpl) GetVFGUID(pfPciAddr string, vfID int) (net.HardwareAddr, error) {
	config, exists := p.guidConfigs[pfPciAddr]
	if !exists {
		return nil, fmt.Errorf("no guid pool for pci address: %s", pfPciAddr)
	}

	if len(config.GUIDs) != 0 {
		if vfID >= len(config.GUIDs) {
			return nil, fmt.Errorf("no guid allocation found for VF id: %d on pf %s", vfID, pfPciAddr)
		}

		guid := config.GUIDs[vfID]

		return guid.HardwareAddr(), nil
	}

	if config.GUIDRange != nil {
		nextGUID := config.GUIDRange.Start + GUID(vfID)
		if nextGUID > config.GUIDRange.End {
			return nil, fmt.Errorf("no guid allocation found for VF id: %d on pf %s", vfID, pfPciAddr)
		}

		return nextGUID.HardwareAddr(), nil
	}

	return nil, fmt.Errorf("no guid pool for pci address: %s", pfPciAddr)
}
