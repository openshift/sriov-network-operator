package utils

import (
	"fmt"
	"os"
	"path/filepath"

	dputils "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/utils"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var DpdkDrivers = []string{"igb_uio", "vfio-pci", "uio_pci_generic"}

// Unbind unbind driver for one device
func Unbind(pciAddr string) error {
	log.Log.V(2).Info("Unbind(): unbind device driver for device", "device", pciAddr)
	yes, driver := hasDriver(pciAddr)
	if !yes {
		return nil
	}

	filePath := filepath.Join(sysBusPciDrivers, driver, "unbind")
	err := os.WriteFile(filePath, []byte(pciAddr), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "Unbind(): fail to unbind driver for device", "device", pciAddr)
		return err
	}
	return nil
}

// BindDpdkDriver bind dpdk driver for one device
// Bind the device given by "pciAddr" to the driver "driver"
func BindDpdkDriver(pciAddr, driver string) error {
	log.Log.V(2).Info("BindDpdkDriver(): bind device to driver",
		"device", pciAddr, "driver", driver)

	if yes, d := hasDriver(pciAddr); yes {
		if driver == d {
			log.Log.V(2).Info("BindDpdkDriver(): device already bound to driver",
				"device", pciAddr, "driver", driver)
			return nil
		}

		if err := Unbind(pciAddr); err != nil {
			return err
		}
	}

	driverOverridePath := filepath.Join(sysBusPciDevices, pciAddr, "driver_override")
	err := os.WriteFile(driverOverridePath, []byte(driver), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "BindDpdkDriver(): fail to write driver_override for device",
			"device", pciAddr, "driver", driver)
		return err
	}
	bindPath := filepath.Join(sysBusPciDrivers, driver, "bind")
	err = os.WriteFile(bindPath, []byte(pciAddr), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "BindDpdkDriver(): fail to bind driver for device",
			"driver", driver, "device", pciAddr)
		_, err := os.Readlink(filepath.Join(sysBusPciDevices, pciAddr, "iommu_group"))
		if err != nil {
			log.Log.Error(err, "Could not read IOMMU group for device", "device", pciAddr)
			return fmt.Errorf(
				"cannot bind driver %s to device %s, make sure IOMMU is enabled in BIOS. %w", driver, pciAddr, err)
		}
		return err
	}
	err = os.WriteFile(driverOverridePath, []byte(""), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "BindDpdkDriver(): failed to clear driver_override for device", "device", pciAddr)
		return err
	}

	return nil
}

// BindDefaultDriver bind driver for one device
// Bind the device given by "pciAddr" to the default driver
func BindDefaultDriver(pciAddr string) error {
	log.Log.V(2).Info("BindDefaultDriver(): bind device to default driver", "device", pciAddr)

	if yes, d := hasDriver(pciAddr); yes {
		if !sriovnetworkv1.StringInArray(d, DpdkDrivers) {
			log.Log.V(2).Info("BindDefaultDriver(): device already bound to default driver",
				"device", pciAddr, "driver", d)
			return nil
		}
		if err := Unbind(pciAddr); err != nil {
			return err
		}
	}

	driverOverridePath := filepath.Join(sysBusPciDevices, pciAddr, "driver_override")
	err := os.WriteFile(driverOverridePath, []byte("\x00"), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "BindDefaultDriver(): failed to write driver_override for device", "device", pciAddr)
		return err
	}
	err = os.WriteFile(sysBusPciDriversProbe, []byte(pciAddr), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "BindDefaultDriver(): failed to bind driver for device", "device", pciAddr)
		return err
	}

	return nil
}

func hasDriver(pciAddr string) (bool, string) {
	driver, err := dputils.GetDriverName(pciAddr)
	if err != nil {
		log.Log.V(2).Info("hasDriver(): device driver is empty for device", "device", pciAddr)
		return false, ""
	}
	log.Log.V(2).Info("hasDriver(): device driver for device", "device", pciAddr, "driver", driver)
	return true, driver
}
