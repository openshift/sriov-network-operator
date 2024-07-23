package infiniband

import (
	"errors"
	"fmt"
	"io/fs"
	"net"

	"github.com/vishvananda/netlink"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	netlinkLibPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

// New creates and returns an InfinibandInterface object, that handles IB VF GUID configuration
func New(netlinkLib netlinkLibPkg.NetlinkLib, kernelHelper types.KernelInterface, networkHelper types.NetworkInterface) (types.InfinibandInterface, error) {
	guidPool, err := newIbGUIDPool(consts.InfinibandGUIDConfigFilePath, netlinkLib, networkHelper)
	if err != nil {
		// if config file doesn't exist, fallback to the random GUID generation
		if errors.Is(err, fs.ErrNotExist) {
			log.Log.Info("infiniband.New(): ib guid config doesn't exist, continuing without it", "config path", consts.InfinibandGUIDConfigFilePath)
			return &infiniband{guidPool: nil, netlinkLib: netlinkLib, kernelHelper: kernelHelper}, nil
		}

		return nil, fmt.Errorf("failed to create the ib guid pool: %w", err)
	}

	return &infiniband{guidPool: guidPool, netlinkLib: netlinkLib, kernelHelper: kernelHelper}, nil
}

type infiniband struct {
	guidPool     ibGUIDPool
	netlinkLib   netlinkLibPkg.NetlinkLib
	kernelHelper types.KernelInterface
}

// ConfigureVfGUID configures and sets a GUID for an IB VF device
func (i *infiniband) ConfigureVfGUID(vfAddr string, pfAddr string, vfID int, pfLink netlink.Link) error {
	log.Log.Info("ConfigureVfGUID(): configure vf guid", "vfAddr", vfAddr, "pfAddr", pfAddr, "vfID", vfID)

	guid := generateRandomGUID()

	if i.guidPool != nil {
		guidFromPool, err := i.guidPool.GetVFGUID(pfAddr, vfID)
		if err != nil {
			log.Log.Info("ConfigureVfGUID(): failed to get GUID from IB GUID pool", "address", vfAddr, "error", err)
			return err
		}
		guid = guidFromPool
	}
	log.Log.Info("ConfigureVfGUID(): set vf guid", "address", vfAddr, "guid", guid)

	return i.applyVfGUIDToInterface(guid, vfAddr, vfID, pfLink)
}

func (i *infiniband) applyVfGUIDToInterface(guid net.HardwareAddr, vfAddr string, vfID int, pfLink netlink.Link) error {
	if err := i.netlinkLib.LinkSetVfNodeGUID(pfLink, vfID, guid); err != nil {
		return err
	}
	if err := i.netlinkLib.LinkSetVfPortGUID(pfLink, vfID, guid); err != nil {
		return err
	}

	return nil
}
