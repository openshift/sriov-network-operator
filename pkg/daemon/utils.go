package daemon

import (
	// "bytes"
	"fmt"
	"io/ioutil"
	// "net"
	"os"
	"path/filepath"
	// "regexp"
	"strconv"
	"strings"

	"github.com/jaypipes/ghw"
	"github.com/golang/glog"
	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

const (
	sysBusPci        = "/sys/bus/pci/devices"
	sysClassNet      = "/sys/class/net"
	netClass         = 0x02
	numVfsFile       = "sriov_numvfs"
)

func DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	glog.V(0).Info("DiscoverSriovDevices")
	pfList := []sriovnetworkv1.InterfaceExt{}

	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("DiscoverSriovDevices(): error getting PCI info: %v", err)
	}

	devices := pci.ListDevices()
	if len(devices) == 0 {
		return nil, fmt.Errorf("DiscoverSriovDevices(): could not retrieve PCI devices")
	}

	for _, device := range devices {
		devClass, err := strconv.ParseInt(device.Class.ID, 16, 64)
		if err != nil {
			glog.Warningf("DiscoverSriovDevices(): unable to parse device class for device %+v %q", device, err)
			continue
		}
		if devClass != netClass {
			// Not network device
			continue
		}

		// TODO: exclude devices used by host system


		if dputils.IsSriovVF(device.Address) {
			continue	
		}

		driver, err := dputils.GetDriverName(device.Address)
		if err != nil {
			glog.Warningf("DiscoverSriovDevices(): unable to parse device driver for device %+v %q", device, err)
			continue
		}
		iface := sriovnetworkv1.InterfaceExt{
			PciAddress:   device.Address,
			Driver:       driver,
			Vendor:       device.Vendor.ID,
			DeviceID:     device.Product.ID,
		}
		if mtu := getNetdevMTU(device.Address); mtu > 0 {
			iface.Mtu = mtu
		}

		if dputils.IsSriovPF(device.Address) {
			iface.TotalVfs = dputils.GetSriovVFcapacity(device.Address)
			iface.NumVfs = dputils.GetVFconfigured(device.Address)
			if dputils.SriovConfigured(device.Address) {
				vfs, err:= dputils.GetVFList(device.Address)
				if err != nil {
					glog.Warningf("DiscoverSriovDevices(): unable to parse VFs for device %+v %q", device, err)
					continue
				}
				for _, vf := range vfs {
					instance := getVfInfo(vf, devices)
					iface.VFs = append(iface.VFs, instance)
				}
			}
		}
		pfList = append(pfList, iface)
	}

	return pfList, nil
}

func syncNodeState(nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	var err error
	for _, ifaceStatus := range nodeState.Status.Interfaces {
		configured := false
		for _, iface := range nodeState.Spec.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true
				if !needUpdate(&iface, &ifaceStatus) {
					glog.V(2).Infof("syncNodeState(): no need update interface %s", iface.PciAddress)
					break
				}
				if err = configSriovDevice(&iface); err != nil {
					return err
				}
				break
			}
		}
		if !configured {
			if err = resetSriovDevice(ifaceStatus.PciAddress); err != nil {
				return err
			}
		}
	}
	return nil
}

func needUpdate(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) bool {
	switch {
	case iface.Mtu != ifaceStatus.Mtu:
		return true
	case iface.NumVfs != ifaceStatus.NumVfs:
		return true
	}
	return false
}

func configSriovDevice(iface *sriovnetworkv1.Interface) error {
	glog.V(2).Infof("configSriovDevice(): config interface %s with %v", iface.PciAddress, iface)
	if iface.NumVfs > 0 {
		err := setSriovNumVfs(iface.PciAddress, iface.NumVfs)
		if err != nil {
			glog.Warningf("configSriovDevice(): fail to set NumVfs for device %s", iface.PciAddress)
			return err
		}
	}
	if iface.Mtu > 0 && (iface.DeviceType == "netdevice" || iface.DeviceType == ""){
		err := setNetdevMTU(iface.PciAddress, iface.Mtu)
		if err != nil {
			glog.Warningf("configSriovDevice(): fail to set mtu for device %s", iface.PciAddress)
			return err
		}

		if iface.NumVfs > 0 {
			vfs, err:= dputils.GetVFList(iface.PciAddress)
			if err != nil {
				glog.Warningf("configSriovDevice(): unable to parse VFs for device %+v %q", iface.PciAddress, err)
			}
			for _, vf := range vfs {
				err := setNetdevMTU(vf, iface.Mtu)
				if err != nil {
					glog.Warningf("configSriovDevice(): fail to set mtu for device %s", vf)
				}
			}
		}
	}
	return nil
}

func setSriovNumVfs(pciAddr string, numVfs int) error {
	glog.V(2).Infof("setSriovNumVfs(): set NumVfs for device %s", pciAddr)
	numVfsFilePath := filepath.Join(sysBusPci, pciAddr, numVfsFile)
	bs := []byte(strconv.Itoa(numVfs))
	err := ioutil.WriteFile(numVfsFilePath, []byte("0"), os.ModeAppend)
	if err != nil {
		glog.Warningf("setSriovNumVfs(): fail to reset NumVfs file %s", numVfsFilePath)
		return err
	}
	err = ioutil.WriteFile(numVfsFilePath, bs, os.ModeAppend)
	if err != nil {
		glog.Warningf("setSriovNumVfs(): fail to set NumVfs file %s", numVfsFilePath)
		return err
	}
	return nil
}

func setNetdevMTU(pciAddr string, mtu int) error {
	glog.V(2).Infof("setNetdevMTU(): set MTU for device %s", pciAddr)
	ifaceName, err:= dputils.GetNetNames(pciAddr)
	if err != nil {
		glog.Errorf("setNetdevMTU(): fail to get interface name for %s, ERR: %s", pciAddr, err)
		return err
	}
	mtuFile := "net/" + ifaceName[0] + "/mtu"
	mtuFilePath := filepath.Join(sysBusPci, pciAddr, mtuFile)
	bs := []byte(strconv.Itoa(mtu))
	err = ioutil.WriteFile(mtuFilePath, bs, os.ModeAppend)
	if err != nil {
		glog.Warningf("setNetdevMTU(): fail to set mtu file %s", mtuFilePath)
		return err
	}
	return nil
}

func getNetdevMTU(pciAddr string) int {
	glog.V(2).Infof("getNetdevMTU(): get MTU for device %s", pciAddr)
	ifaceName, err:= dputils.GetNetNames(pciAddr)
	if err != nil {
		return 0
	}
	mtuFile := "net/" + ifaceName[0] + "/mtu"
	mtuFilePath := filepath.Join(sysBusPci, pciAddr, mtuFile)
	data, err := ioutil.ReadFile(mtuFilePath)
	if err != nil {
		glog.Warningf("setNetdevMTU(): fail to read mtu file %s", mtuFilePath)
		return 0
	}
	mtu, err:= strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		glog.Warningf("setNetdevMTU(): fail to convert mtu %s to int", strings.TrimSpace(string(data)))
		return 0
	}
	return mtu
}

func resetSriovDevice(pciAddr string) error {
	glog.V(2).Infof("resetSriovDevice(): reset sr-iov device %s", pciAddr)
	numVfsFilePath := filepath.Join(sysBusPci, pciAddr, numVfsFile)
	err := ioutil.WriteFile(numVfsFilePath, []byte("0"), os.ModeAppend)
	if err != nil {
		return err
	}
	ifaceName, err:= dputils.GetNetNames(pciAddr)
	if err != nil {
		return err
	}
	mtuFile := "net/" + ifaceName[0] + "/mtu"
	mtuFilePath := filepath.Join(sysBusPci, pciAddr, mtuFile)
	err = ioutil.WriteFile(mtuFilePath, []byte("1500"), os.ModeAppend)
	if err != nil {
		return err
	}
	return nil
}

func getVfInfo(pciAddr string, devices []*ghw.PCIDevice) (sriovnetworkv1.VirutalFunction) {
	driver, err := dputils.GetDriverName(pciAddr)
	if err != nil {
		glog.Warningf("getVfInfo(): unable to parse device driver for device %s %q", pciAddr, err)
	}

	vf := sriovnetworkv1.VirutalFunction{
		PciAddress: pciAddr,
		Driver:     driver,
	}

	if mtu := getNetdevMTU(pciAddr); mtu > 0 {
		vf.Mtu = mtu
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
