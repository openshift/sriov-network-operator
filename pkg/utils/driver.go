package utils

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/golang/glog"

	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var DpdkDrivers = []string{"igb_uio", "vfio-pci", "uio_pci_generic"}

// Unbind unbind driver for one device
func Unbind(pciAddr string) error {
	glog.V(2).Infof("Unbind(): unbind device %s driver", pciAddr)
	yes, driver := hasDriver(pciAddr)
	if !yes {
		return nil
	}

	filePath := filepath.Join(sysBusPciDrivers, driver, "unbind")
	err := ioutil.WriteFile(filePath, []byte(pciAddr), os.ModeAppend)
	if err != nil {
		glog.Errorf("Unbind(): fail to unbind driver for device %s. %s", pciAddr, err)
		return err
	}
	return nil
}

// BindDpdkDriver bind dpdk driver for one device
// Bind the device given by "pciAddr" to the driver "driver"
func BindDpdkDriver(pciAddr, driver string) error {
	glog.V(2).Infof("BindDpdkDriver(): bind device %s to driver %s", pciAddr, driver)

	if yes, d := hasDriver(pciAddr); yes {
		if driver == d {
			glog.V(2).Infof("BindDpdkDriver(): device %s already bound to driver %s", pciAddr, driver)
			return nil
		}

		if err := Unbind(pciAddr); err != nil {
			return err
		}
	}

	driverOverridePath := filepath.Join(sysBusPciDevices, pciAddr, "driver_override")
	err := ioutil.WriteFile(driverOverridePath, []byte(driver), os.ModeAppend)
	if err != nil {
		glog.Errorf("BindDpdkDriver(): fail to write driver_override for device %s %s", driver, err)
		return err
	}
	bindPath := filepath.Join(sysBusPciDrivers, driver, "bind")
	err = ioutil.WriteFile(bindPath, []byte(pciAddr), os.ModeAppend)
	if err != nil {
		glog.Errorf("BindDpdkDriver(): fail to bind driver for device %s: %s", pciAddr, err)
		_, err := os.Readlink(filepath.Join(sysBusPciDevices, pciAddr, "iommu_group"))
		if err != nil {
			glog.Errorf("Could not read IOMMU group for device %s: %s", pciAddr, err)
			return fmt.Errorf("Cannot bind driver %s to %s, make sure IOMMU is enabled in BIOS", driver, pciAddr)
		}
		return err
	}
	err = ioutil.WriteFile(driverOverridePath, []byte(""), os.ModeAppend)
	if err != nil {
		glog.Errorf("BindDpdkDriver(): fail to clear driver_override for device %s: %s", pciAddr, err)
		return err
	}

	return nil
}

// BindDefaultDriver bind driver for one device
// Bind the device given by "pciAddr" to the default driver
func BindDefaultDriver(pciAddr string) error {
	glog.V(2).Infof("BindDefaultDriver(): bind device %s to default driver", pciAddr)

	if yes, d := hasDriver(pciAddr); yes {
		if !sriovnetworkv1.StringInArray(d, DpdkDrivers) {
			glog.V(2).Infof("BindDefaultDriver(): device %s already bound to default driver %s", pciAddr, d)
			return nil
		}
		if err := Unbind(pciAddr); err != nil {
			return err
		}
	}

	driverOverridePath := filepath.Join(sysBusPciDevices, pciAddr, "driver_override")
	err := ioutil.WriteFile(driverOverridePath, []byte("\x00"), os.ModeAppend)
	if err != nil {
		glog.Errorf("BindDefaultDriver(): fail to write driver_override for device %s: %s", pciAddr, err)
		return err
	}
	err = ioutil.WriteFile(sysBusPciDriversProbe, []byte(pciAddr), os.ModeAppend)
	if err != nil {
		glog.Errorf("BindDpdkDriver(): fail to bind driver for device %s: %s", pciAddr, err)
		return err
	}

	return nil
}

func hasDriver(pciAddr string) (bool, string) {
	driver, err := dputils.GetDriverName(pciAddr)
	if err != nil {
		glog.V(2).Infof("hasDriver(): device %s driver is empty", pciAddr)
		return false, ""
	}
	glog.V(2).Infof("hasDriver(): device %s driver is %s", pciAddr, driver)
	return true, driver
}
