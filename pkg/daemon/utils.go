package daemon

import (
	"bytes"
	// "fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	// "regexp"
	"strconv"
	// "strings"

	"gopkg.in/ini.v1"
	"github.com/golang/glog"
	"github.com/intel/sriov-network-device-plugin/pkg/utils"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"

)

const (
	sysBusPci = "/sys/bus/pci/devices"
	sysClassNet = "/sys/class/net"
	deviceUeventFile = "device/uevent"
	deviceVendorFile = "device/vendor"
	speedFile = "speed"
	mtuFile = "mtu"
	numVfsFile = "device/sriov_numvfs"
	totalVfFile      = "sriov_totalvfs"
	configuredVfFile = "sriov_numvfs"
)

func DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	glog.Info("DiscoverSriovDevices")
	pfList := []sriovnetworkv1.InterfaceExt{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		glog.Infof("Interface %s\n", iface.Name)
		pciAddr, driver := getPciAddrAndDriverWithName(iface.Name)
		if pciAddr == "" && driver == ""{
			continue
		}
		if totalVfs := utils.GetSriovVFcapacity(pciAddr); totalVfs > 0 {
			vendorFilePath := filepath.Join(sysClassNet, iface.Name, deviceVendorFile)
			vendorID, err := ioutil.ReadFile(vendorFilePath)
			if err != nil {
				continue
			}

			numVfsFilePath := filepath.Join(sysClassNet, iface.Name, numVfsFile)
			vfs, err := ioutil.ReadFile(numVfsFilePath)
			numVfs, err := strconv.Atoi(string(bytes.TrimSpace(vfs)))
			if err != nil {
				continue
			}

			mtuFilePath := filepath.Join(sysClassNet, iface.Name, mtuFile)
			m, err := ioutil.ReadFile(mtuFilePath)
			mtu, err := strconv.Atoi(string(bytes.TrimSpace(m)))
			if err != nil {
				continue
			}

			vendorID = bytes.TrimSpace(vendorID)
			var vendorName string
			switch string(vendorID) {
			case "0x8086":
				vendorName = "Intel"
			case "0x15b3":
				vendorName = "Mellanox"
			}

			speedFilePath := filepath.Join(sysClassNet, iface.Name, speedFile)
			s, err := ioutil.ReadFile(speedFilePath)
			if err != nil {
				continue
			}
			s = bytes.TrimSpace(s)

			pfList = append(pfList, sriovnetworkv1.InterfaceExt{
				Name: iface.Name,
				PciAddress: pciAddr,
				KernelDriver: driver,
				Vendor: vendorName,
				LinkSpeed: string(s),
				TotalVfs: totalVfs,
				NumVfs: numVfs,
				Mtu: mtu,
			})
		}
	}
	return pfList, nil
}

func getPciAddrAndDriverWithName (name string) (string, string) {
	ueventFilePath := filepath.Join(sysClassNet, name, deviceUeventFile)
	cfg, err := ini.Load(ueventFilePath)
	if err != nil {
		return "", ""
	}
	return cfg.Section("").Key("PCI_SLOT_NAME").String(), cfg.Section("").Key("DRIVER").String()
}

func syncNodeState(nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	var err error
	for _, ifaceStatus := range nodeState.Status.Interfaces {
		if err = resetSriovDevice(ifaceStatus.Name); err != nil {
			return err
		}
		for _, iface := range nodeState.Spec.Interfaces {
			if iface.Name == ifaceStatus.Name {
				if err = configSriovDevice(&iface); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func configSriovDevice(iface *sriovnetworkv1.Interface) error {
	switch {
	case iface.NumVfs > 0: 
		numVfsFilePath := filepath.Join(sysClassNet, iface.Name, numVfsFile)
		bs := []byte(strconv.Itoa(iface.NumVfs))
		err := ioutil.WriteFile(numVfsFilePath, []byte("0"), os.ModeAppend)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(numVfsFilePath, bs, os.ModeAppend)
		if err != nil {
			return err
		}
	case iface.Mtu > 0:
		mtuFilePath := filepath.Join(sysClassNet, iface.Name, mtuFile)
		bs := []byte(strconv.Itoa(iface.Mtu))
		err := ioutil.WriteFile(mtuFilePath, bs, os.ModeAppend)
		if err != nil {
			return err
		}
	}
	return nil
}

func resetSriovDevice(ifaceName string) error {
	numVfsFilePath := filepath.Join(sysClassNet, ifaceName, numVfsFile)
	err := ioutil.WriteFile(numVfsFilePath, []byte("0"), os.ModeAppend)
	if err != nil {
		return err
	}
	mtuFilePath := filepath.Join(sysClassNet, ifaceName, mtuFile)
	err = ioutil.WriteFile(mtuFilePath, []byte("1500"), os.ModeAppend)
	if err != nil {
		return err
	}
	return nil
}
