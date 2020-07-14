package utils

import (
	"fmt"
	"io/ioutil"
	"strconv"

	"github.com/golang/glog"
	"github.com/jaypipes/ghw"

	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

func DiscoverSriovAcceleratorDevices() ([]sriovnetworkv1.AcceleratorExt, error) {
	glog.V(2).Info("DiscoverSriovAcceleratorDevices()")
	pfList := []sriovnetworkv1.AcceleratorExt{}

	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("DiscoverSriovAcceleratorDevices(): error getting PCI info: %v", err)
	}

	devices := pci.ListDevices()
	if len(devices) == 0 {
		return nil, fmt.Errorf("DiscoverSriovAcceleratorDevices(): could not retrieve PCI devices")
	}

	for _, device := range devices {
		devClass, err := strconv.ParseInt(device.Class.ID, 16, 64)
		if err != nil {
			glog.Warningf("DiscoverSriovAcceleratorDevices(): unable to parse device class for device %+v %q", device, err)
			continue
		}

		if devClass != accelClass {
			// Not accelerator device
			continue
		}

		if dputils.IsSriovVF(device.Address) {
			continue
		}

		driver, err := dputils.GetDriverName(device.Address)
		if err != nil {
			glog.Warningf("DiscoverSriovAcceleratorDevices(): unable to parse device driver for device %+v %q", device, err)
			continue
		}
		iface := sriovnetworkv1.AcceleratorExt{
			PciAddress: device.Address,
			Driver:     driver,
			Vendor:     device.Vendor.ID,
			DeviceID:   device.Product.ID,
		}

		if dputils.IsSriovPF(device.Address) {
			iface.TotalVfs = dputils.GetSriovVFcapacity(device.Address)
			iface.NumVfs = dputils.GetVFconfigured(device.Address)
			if dputils.SriovConfigured(device.Address) {
				vfs, err := dputils.GetVFList(device.Address)
				if err != nil {
					glog.Warningf("DiscoverSriovAcceleratorDevices(): unable to parse VFs for device %+v %q", device, err)
					continue
				}
				for _, vf := range vfs {
					instance := getAccelVfInfo(vf, devices)
					iface.VFs = append(iface.VFs, instance)
				}
			}

			content, err := ioutil.ReadFile(fmt.Sprintf("%s/%s.cfg", acceleratorConfigPath, iface.PciAddress))
			if err == nil {
				iface.Config = string(content)
			}
		}

		glog.V(2).Infof("DiscoverSriovAcceleratorDevices(): find accelerator device %s", iface.DeviceID)
		pfList = append(pfList, iface)
	}

	return pfList, nil
}

func SyncAcceleratorNodeState(newState *sriovnetworkv1.SriovAcceleratorNodeState) error {
	var err error
	for _, accelStatus := range newState.Status.Accelerators {
		configured := false
		for _, accel := range newState.Spec.Cards {
			if accel.PciAddress == accelStatus.PciAddress {
				configured = true
				if !needAcceleratorUpdate(&accel, &accelStatus) {
					glog.V(2).Infof("SyncAcceleratorNodeState(): no need update interface %s", accel.PciAddress)
					break
				}
				if err = configSriovIntelFecAcceleratorDevice(&accel, &accelStatus); err != nil {
					glog.Errorf("SyncAcceleratorNodeState(): fail to config sriov accelerator %s: %v", accel.PciAddress, err)
					return err
				}
				break
			}
		}
		if !configured && accelStatus.NumVfs > 0 {
			if err = resetSriovIntelFecAcceleratorDevice(accelStatus); err != nil {
				return err
			}
		}
	}
	return nil
}

func needAcceleratorUpdate(card *sriovnetworkv1.Card, accelStatus *sriovnetworkv1.AcceleratorExt) bool {
	if card.Config != accelStatus.Config {
		glog.V(2).Infof("needAcceleratorUpdate(): config needs update\n desired: %s\n current:\n %s", card.Config, accelStatus.Config)
		return true
	}
	if card.NumVfs != accelStatus.NumVfs {
		glog.V(2).Infof("needAcceleratorUpdate(): NumVfs needs update desired=%d, current=%d", card.NumVfs, accelStatus.NumVfs)
		return true
	}

	return false
}
