package vdpa

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/k8snetworkplumbingwg/govdpa/pkg/kvdpa"
	"sigs.k8s.io/controller-runtime/pkg/log"

	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/govdpa"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

type vdpa struct {
	kernel  types.KernelInterface
	vdpaLib govdpa.GoVdpaLib
}

func New(k types.KernelInterface, vdpaLib govdpa.GoVdpaLib) types.VdpaInterface {
	return &vdpa{kernel: k, vdpaLib: vdpaLib}
}

// CreateVDPADevice creates VDPA device for VF with required type,
// pciAddr - PCI address of the VF
// vdpaType - type of the VDPA device to create: virtio of vhost
func (v *vdpa) CreateVDPADevice(pciAddr, vdpaType string) error {
	log.Log.V(2).Info("CreateVDPADevice(): create VDPA device for VF",
		"device", pciAddr, "vdpaType", vdpaType)
	expectedDriver := vdpaTypeToDriver(vdpaType)
	if expectedDriver == "" {
		return fmt.Errorf("unknown VDPA device type: %s", vdpaType)
	}
	expectedVDPAName := generateVDPADevName(pciAddr)
	_, err := v.vdpaLib.GetVdpaDevice(expectedVDPAName)
	if err != nil {
		if !errors.Is(err, syscall.ENODEV) {
			log.Log.Error(err, "CreateVDPADevice(): fail to check if VDPA device exist",
				"device", pciAddr, "vdpaDev", expectedVDPAName)
			return err
		}
		if err := v.vdpaLib.AddVdpaDevice("pci/"+pciAddr, expectedVDPAName); err != nil {
			log.Log.Error(err, "CreateVDPADevice(): fail to create VDPA device",
				"device", pciAddr, "vdpaDev", expectedVDPAName)
			return err
		}
	}
	err = v.kernel.BindDriverByBusAndDevice(constants.BusVdpa, expectedVDPAName, expectedDriver)
	if err != nil {
		log.Log.Error(err, "CreateVDPADevice(): fail to bind VDPA device to the driver",
			"device", pciAddr, "vdpaDev", expectedVDPAName, "driver", expectedDriver)
		return err
	}
	return nil
}

// DeleteVDPADevice removes VDPA device for provided pci address
// pciAddr - PCI address of the VF
func (v *vdpa) DeleteVDPADevice(pciAddr string) error {
	log.Log.V(2).Info("DeleteVDPADevice(): delete VDPA device for VF",
		"device", pciAddr)
	expectedVDPAName := generateVDPADevName(pciAddr)
	if err := v.vdpaLib.DeleteVdpaDevice(expectedVDPAName); err != nil {
		if errors.Is(err, syscall.ENODEV) {
			log.Log.V(2).Info("DeleteVDPADevice(): VDPA device not found",
				"device", pciAddr, "name", expectedVDPAName)
			return nil
		}
		log.Log.Error(err, "DeleteVDPADevice(): fail to remove VDPA device",
			"device", pciAddr, "name", expectedVDPAName)
		return err
	}
	return nil
}

// DiscoverVDPAType returns type of existing VDPA device for VF,
// returns empty string if VDPA device not found or unknown driver is in use
// pciAddr - PCI address of the VF
func (v *vdpa) DiscoverVDPAType(pciAddr string) string {
	expectedVDPADevName := generateVDPADevName(pciAddr)
	vdpaDev, err := v.vdpaLib.GetVdpaDevice(expectedVDPADevName)
	if err != nil {
		if errors.Is(err, syscall.ENODEV) {
			log.Log.V(2).Info("discoverVDPAType(): VDPA device for VF not found", "device", pciAddr)
			return ""
		}
		log.Log.Error(err, "getVfInfo(): unable to get VF VDPA devices", "device", pciAddr)
		return ""
	}
	driverName := vdpaDev.Driver()
	if driverName == "" {
		log.Log.V(2).Info("discoverVDPAType(): VDPA device has no driver", "device", pciAddr)
		return ""
	}
	vdpaType := vdpaDriverToType(driverName)
	if vdpaType == "" {
		log.Log.Error(nil, "getVfInfo(): WARNING: unknown VDPA device type for VF, ignore",
			"device", pciAddr, "driver", driverName)
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
		return kvdpa.VhostVdpaDriver
	case constants.VdpaTypeVirtio:
		return kvdpa.VirtioVdpaDriver
	default:
		return ""
	}
}

// vdpa driver name to type conversion
func vdpaDriverToType(driver string) string {
	switch driver {
	case kvdpa.VhostVdpaDriver:
		return constants.VdpaTypeVhost
	case kvdpa.VirtioVdpaDriver:
		return constants.VdpaTypeVirtio
	default:
		return ""
	}
}
