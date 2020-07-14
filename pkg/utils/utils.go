package utils

import (
	"math/rand"
	"net"
	"os"
	"os/exec"
	"syscall"

	"github.com/golang/glog"
	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"
	"github.com/jaypipes/ghw"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

const (
	sysBusPciDevices      = "/sys/bus/pci/devices"
	sysBusPciDrivers      = "/sys/bus/pci/drivers"
	sysBusPciDriversProbe = "/sys/bus/pci/drivers_probe"
	sysClassNet           = "/sys/class/net"
	netClass              = 0x02
	accelClass            = 0x12
	numVfsFile            = "sriov_numvfs"
	scriptsPath           = "bindata/scripts/load-kmod.sh"
)

func tryGetInterfaceName(pciAddr string) string {
	names, err := dputils.GetNetNames(pciAddr)
	if err != nil || len(names) < 1 {
		return ""
	}
	glog.V(2).Infof("tryGetInterfaceName(): name is %s", names[0])
	return names[0]
}

func getAccelVfInfo(pciAddr string, devices []*ghw.PCIDevice) sriovnetworkv1.AcceleratorVirtualFunction {
	driver, err := dputils.GetDriverName(pciAddr)
	if err != nil {
		glog.Warningf("getNetworkVfInfo(): unable to parse device driver for device %s %q", pciAddr, err)
	}
	id, err := dputils.GetVFID(pciAddr)
	if err != nil {
		glog.Warningf("getNetworkVfInfo(): unable to get VF index for device %s %q", pciAddr, err)
	}
	vf := sriovnetworkv1.AcceleratorVirtualFunction{
		PciAddress: pciAddr,
		Driver:     driver,
		VfID:       id,
	}

	for _, device := range devices {
		if pciAddr == device.Address {
			vf.Vendor = device.Vendor.ID
			vf.DeviceID = device.Product.ID
			break
		}
		continue
	}
	return vf
}

func LoadKernelModule(name string) error {
	glog.Infof("LoadKernelModule(): try to load kernel module %s", name)
	cmd := exec.Command("/bin/sh", scriptsPath, name)
	err := cmd.Run()
	if err != nil {
		glog.Errorf("LoadKernelModule(): fail to load kernel module %s: %v", name, err)
		return err
	}
	return nil
}

func Chroot(path string) (func() error, error) {
	root, err := os.Open("/")
	if err != nil {
		return nil, err
	}

	if err := syscall.Chroot(path); err != nil {
		root.Close()
		return nil, err
	}

	return func() error {
		defer root.Close()
		if err := root.Chdir(); err != nil {
			return err
		}
		return syscall.Chroot(".")
	}, nil
}

func generateRandomGuid() net.HardwareAddr {
	guid := make(net.HardwareAddr, 8)

	// First field is 0x01 - xfe to avoid all zero and all F invalid guids
	guid[0] = byte(1 + rand.Intn(0xfe))

	for i := 1; i < len(guid); i++ {
		guid[i] = byte(rand.Intn(0x100))
	}

	return guid
}
