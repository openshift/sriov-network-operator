package vdpa

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/vishvananda/netlink"
	"sigs.k8s.io/controller-runtime/pkg/log"

	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	netlinkLibPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

const (
	VhostVdpaDriver  = "vhost_vdpa"
	VirtioVdpaDriver = "virtio_vdpa"
)

type vdpa struct {
	kernel     types.KernelInterface
	netlinkLib netlinkLibPkg.NetlinkLib
}

func New(k types.KernelInterface, netlinkLib netlinkLibPkg.NetlinkLib) types.VdpaInterface {
	return &vdpa{kernel: k, netlinkLib: netlinkLib}
}

// CreateVDPADevice creates VDPA device for VF with required type,
// pciAddr - PCI address of the VF
// vdpaType - type of the VDPA device to create: virtio of vhost
func (v *vdpa) CreateVDPADevice(pciAddr, vdpaType string) error {
	expectedVDPAName := generateVDPADevName(pciAddr)
	funcLog := log.Log.WithValues("device", pciAddr, "vdpaType", vdpaType, "name", expectedVDPAName)
	funcLog.V(2).Info("CreateVDPADevice(): create VDPA device for VF")
	expectedDriver := vdpaTypeToDriver(vdpaType)
	if expectedDriver == "" {
		return fmt.Errorf("unknown VDPA device type: %s", vdpaType)
	}
	_, err := v.netlinkLib.VDPAGetDevByName(expectedVDPAName)
	if err != nil {
		if !errors.Is(err, syscall.ENODEV) {
			funcLog.Error(err, "CreateVDPADevice(): fail to check if VDPA device exist")
			return err
		}
		// first try to create VDPA device with MaxVQP parameter set to 32 to exactly match HW offloading use-case with the
		// old swtichdev implementation. Create device without MaxVQP parameter if it is not supported.
		if err := v.netlinkLib.VDPANewDev(expectedVDPAName, constants.BusPci, pciAddr, netlink.VDPANewDevParams{MaxVQP: 32}); err != nil {
			if !errors.Is(err, syscall.ENOTSUP) {
				funcLog.Error(err, "CreateVDPADevice(): fail to create VDPA device with MaxVQP parameter")
				return err
			}
			funcLog.V(2).Info("failed to create VDPA device with MaxVQP parameter, try without it")
			if err := v.netlinkLib.VDPANewDev(expectedVDPAName, constants.BusPci, pciAddr, netlink.VDPANewDevParams{}); err != nil {
				funcLog.Error(err, "CreateVDPADevice(): fail to create VDPA device without MaxVQP parameter")
				return err
			}
		}
	}
	err = v.kernel.BindDriverByBusAndDevice(constants.BusVdpa, expectedVDPAName, expectedDriver)
	if err != nil {
		funcLog.Error(err, "CreateVDPADevice(): fail to bind VDPA device to the driver")
		return err
	}
	return nil
}

// DeleteVDPADevice removes VDPA device for provided pci address
// pciAddr - PCI address of the VF
func (v *vdpa) DeleteVDPADevice(pciAddr string) error {
	expectedVDPAName := generateVDPADevName(pciAddr)
	funcLog := log.Log.WithValues("device", pciAddr, "name", expectedVDPAName)
	funcLog.V(2).Info("DeleteVDPADevice(): delete VDPA device for VF")

	if err := v.netlinkLib.VDPADelDev(expectedVDPAName); err != nil {
		if errors.Is(err, syscall.ENODEV) {
			funcLog.V(2).Info("DeleteVDPADevice(): VDPA device not found")
			return nil
		}
		if errors.Is(err, syscall.ENOENT) {
			funcLog.V(2).Info("DeleteVDPADevice(): VDPA module is not loaded")
			return nil
		}
		funcLog.Error(err, "DeleteVDPADevice(): fail to remove VDPA device")
		return err
	}
	return nil
}

// DiscoverVDPAType returns type of existing VDPA device for VF,
// returns empty string if VDPA device not found or unknown driver is in use
// pciAddr - PCI address of the VF
func (v *vdpa) DiscoverVDPAType(pciAddr string) string {
	expectedVDPAName := generateVDPADevName(pciAddr)
	funcLog := log.Log.WithValues("device", pciAddr, "name", expectedVDPAName)
	_, err := v.netlinkLib.VDPAGetDevByName(expectedVDPAName)
	if err != nil {
		if errors.Is(err, syscall.ENODEV) {
			return ""
		}
		if errors.Is(err, syscall.ENOENT) {
			funcLog.V(2).Info("DiscoverVDPAType(): VDPA module is not loaded")
			return ""
		}
		funcLog.Error(err, "DiscoverVDPAType(): unable to get VF VDPA devices")
		return ""
	}
	driverName, err := v.kernel.GetDriverByBusAndDevice(constants.BusVdpa, expectedVDPAName)
	if err != nil {
		funcLog.Error(err, "DiscoverVDPAType(): unable to get driver info for VF VDPA devices")
		return ""
	}
	if driverName == "" {
		funcLog.V(2).Info("DiscoverVDPAType(): VDPA device has no driver")
		return ""
	}
	vdpaType := vdpaDriverToType(driverName)
	if vdpaType == "" {
		funcLog.Error(nil, "DiscoverVDPAType(): WARNING: unknown VDPA device type for VF, ignore")
	}
	return vdpaType
}

// generates predictable name for VDPA device, example: vpda:0000:03:00.1
func generateVDPADevName(pciAddr string) string {
	return "vdpa:" + pciAddr
}

// vdpa type to driver name conversion
func vdpaTypeToDriver(vdpaType string) string {
	switch vdpaType {
	case constants.VdpaTypeVhost:
		return VhostVdpaDriver
	case constants.VdpaTypeVirtio:
		return VirtioVdpaDriver
	default:
		return ""
	}
}

// vdpa driver name to type conversion
func vdpaDriverToType(driver string) string {
	switch driver {
	case VhostVdpaDriver:
		return constants.VdpaTypeVhost
	case VirtioVdpaDriver:
		return constants.VdpaTypeVirtio
	default:
		return ""
	}
}
