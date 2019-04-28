package daemon

import (
	"bytes"
	// "fmt"
	"io/ioutil"
	"net"
	// "os"
	"path/filepath"
	// "regexp"
	"strconv"
	// "strings"

	"gopkg.in/ini.v1"
	"github.com/golang/glog"
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
	totalVfFile = "device/sriov_totalvfs"
)

func DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	glog.Info("DiscoverSriovDevices")
	pfList := []sriovnetworkv1.InterfaceExt{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		pciAddr, driver := getPciAddrAndDriverWithName(iface.Name)
		if pciAddr == "" && driver == ""{
			continue
		}
		totalVfFilePath := filepath.Join(sysClassNet, iface.Name, totalVfFile)
		vfs, err := ioutil.ReadFile(totalVfFilePath)
		if err != nil {
			continue
		}
		vfs = bytes.TrimSpace(vfs)
		totalVfs, err := strconv.Atoi(string(vfs))
		if err != nil {
			continue
		}
		if totalVfs > 0 {
			vendorFilePath := filepath.Join(sysClassNet, iface.Name, deviceVendorFile)
			vendorID, err := ioutil.ReadFile(vendorFilePath)
			if err != nil {
				continue
			}

			numVfsFilePath := filepath.Join(sysClassNet, iface.Name, numVfsFile)
			vfs, err := ioutil.ReadFile(numVfsFilePath)
			if err != nil {
				continue
			}
			numVfs, err := strconv.Atoi(string(bytes.TrimSpace(vfs)))
			if err != nil {
				continue
			}

			mtuFilePath := filepath.Join(sysClassNet, iface.Name, mtuFile)
			m, err := ioutil.ReadFile(mtuFilePath)
			if err != nil {
				continue
			}
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
