package utils

import (
	// "bytes"
	"fmt"
	"io/ioutil"
	// "net"
	"os"
	"os/exec"
	"path/filepath"
	// "regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/golang/glog"
	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"
	"github.com/jaypipes/ghw"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	"github.com/vishvananda/netlink"
)

const (
	sysBusPciDevices      = "/sys/bus/pci/devices"
	sysBusPciDrivers      = "/sys/bus/pci/drivers"
	sysBusPciDriversProbe = "/sys/bus/pci/drivers_probe"
	sysClassNet           = "/sys/class/net"
	netClass              = 0x02
	numVfsFile            = "sriov_numvfs"
	scriptsPath           = "bindata/scripts/load-kmod.sh"
)

func DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	glog.V(2).Info("DiscoverSriovDevices")
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
			PciAddress: device.Address,
			Driver:     driver,
			Vendor:     device.Vendor.ID,
			DeviceID:   device.Product.ID,
		}
		if mtu := getNetdevMTU(device.Address); mtu > 0 {
			iface.Mtu = mtu
		}
		if name := tryGetInterfaceName(device.Address); name != "" {
			iface.Name = name
		}
		iface.LinkType = getLinkType(iface)

		if dputils.IsSriovPF(device.Address) {
			iface.TotalVfs = dputils.GetSriovVFcapacity(device.Address)
			iface.NumVfs = dputils.GetVFconfigured(device.Address)
			if dputils.SriovConfigured(device.Address) {
				vfs, err := dputils.GetVFList(device.Address)
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

func SyncNodeState(newState *sriovnetworkv1.SriovNetworkNodeState) error {
	var err error
	for _, ifaceStatus := range newState.Status.Interfaces {
		configured := false
		for _, iface := range newState.Spec.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true
				if !needUpdate(&iface, &ifaceStatus) {
					glog.V(2).Infof("syncNodeState(): no need update interface %s", iface.PciAddress)
					break
				}
				if err = configSriovDevice(&iface, &ifaceStatus); err != nil {
					glog.Errorf("SyncNodeState(): fail to config sriov interface %s: %v", iface.PciAddress, err)
					return err
				}
				break
			}
		}
		if !configured && ifaceStatus.NumVfs > 0 {
			if err = resetSriovDevice(ifaceStatus.PciAddress); err != nil {
				return err
			}
		}
	}
	return nil
}

func needUpdate(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) bool {
	if iface.Mtu > 0 && iface.Mtu != ifaceStatus.Mtu {
		glog.V(2).Infof("needUpdate(): MTU needs update, desired=%d, current=%d", iface.Mtu, ifaceStatus.Mtu)
		return true
	}
	if iface.NumVfs != ifaceStatus.NumVfs {
		glog.V(2).Infof("needUpdate(): NumVfs needs update desired=%d, current=%d", iface.NumVfs, ifaceStatus.NumVfs)
		return true
	}
	if iface.NumVfs > 0 {
		for _, vf := range ifaceStatus.VFs {
			ingroup := false
			for _, group := range iface.VfGroups {
				if sriovnetworkv1.IndexInRange(vf.VfID, group.VfRange) {
					ingroup = true
					if group.DeviceType != "netdevice" {
						if group.DeviceType != vf.Driver {
							glog.V(2).Infof("needUpdate(): Driver needs update, desired=%s, current=%s", group.DeviceType, vf.Driver)
							return true
						}
					} else {
						if sriovnetworkv1.StringInArray(vf.Driver, DpdkDrivers) {
							glog.V(2).Infof("needUpdate(): Driver needs update, desired=%s, current=%s", group.DeviceType, vf.Driver)
							return true
						}
					}
					break
				}
			}
			if !ingroup && sriovnetworkv1.StringInArray(vf.Driver, DpdkDrivers) {
				// VF which has DPDK driver loaded but not in any group, needs to be reset to default driver.
				return true
			}
		}
	}
	return false
}

func configSriovDevice(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) error {
	glog.V(2).Infof("configSriovDevice(): config interface %s with %v", iface.PciAddress, iface)
	var err error
	if iface.NumVfs > ifaceStatus.TotalVfs {
		err := fmt.Errorf("cannot config SRIOV device: NumVfs is larger than TotalVfs")
		glog.Errorf("configSriovDevice(): fail to set NumVfs for device %s: %v", iface.PciAddress, err)
		return err
	}
	// set numVFs
	if iface.NumVfs != ifaceStatus.NumVfs {
		err = setSriovNumVfs(iface.PciAddress, iface.NumVfs)
		if err != nil {
			glog.Errorf("configSriovDevice(): fail to set NumVfs for device %s", iface.PciAddress)
			return err
		}
		if err = setVfsAdminMac(ifaceStatus); err != nil {
			return err
		}
	}
	// set PF mtu
	if iface.Mtu > 0 && iface.Mtu != ifaceStatus.Mtu {
		err = setNetdevMTU(iface.PciAddress, iface.Mtu)
		if err != nil {
			glog.Warningf("configSriovDevice(): fail to set mtu for PF %s: %v", iface.PciAddress, err)
			return err
		}
	}
	// Config VFs
	if iface.NumVfs > 0 {
		vfAddrs, err := dputils.GetVFList(iface.PciAddress)
		if err != nil {
			glog.Warningf("configSriovDevice(): unable to parse VFs for device %+v %q", iface.PciAddress, err)
		}
		for _, addr := range vfAddrs {
			driver := ""
			vfID, err := dputils.GetVFID(addr)
			for _, group := range iface.VfGroups {
				if err != nil {
					glog.Warningf("configSriovDevice(): unable to get VF id %+v %q", iface.PciAddress, err)
				}
				if sriovnetworkv1.IndexInRange(vfID, group.VfRange) {
					if sriovnetworkv1.StringInArray(group.DeviceType, DpdkDrivers) {
						driver = group.DeviceType
					}
					break
				}
			}
			if driver == "" {
				if err := BindDefaultDriver(addr); err != nil {
					glog.Warningf("configSriovDevice(): fail to bind default driver for device %s", addr)
					return err
				}
				// only set MTU for VF with default driver
				if err := setNetdevMTU(addr, iface.Mtu); err != nil {
					glog.Warningf("configSriovDevice(): fail to set mtu for VF %s: %v", addr, err)
					return err
				}
			} else {
				if err := BindDpdkDriver(addr, driver); err != nil {
					glog.Warningf("configSriovDevice(): fail to bind driver %s for device %s", driver, addr)
					return err
				}
			}
		}
	}
	return nil
}

func setSriovNumVfs(pciAddr string, numVfs int) error {
	glog.V(2).Infof("setSriovNumVfs(): set NumVfs for device %s", pciAddr)
	numVfsFilePath := filepath.Join(sysBusPciDevices, pciAddr, numVfsFile)
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
	if mtu <= 0 {
		glog.V(2).Infof("setNetdevMTU(): not set MTU to %d", mtu)
		return nil
	}
	b := backoff.NewConstantBackOff(1 * time.Second)
	err := backoff.Retry(func() error {
		ifaceName, err := dputils.GetNetNames(pciAddr)
		if err != nil {
			glog.Warningf("setNetdevMTU(): fail to get interface name for %s: %s", pciAddr, err)
			return err
		}
		mtuFile := "net/" + ifaceName[0] + "/mtu"
		mtuFilePath := filepath.Join(sysBusPciDevices, pciAddr, mtuFile)
		return ioutil.WriteFile(mtuFilePath, []byte(strconv.Itoa(mtu)), os.ModeAppend)
	}, backoff.WithMaxRetries(b, 10))
	if err != nil {
		glog.Warningf("setNetdevMTU(): fail to write mtu file after retrying: %v", err)
		return err
	}
	return nil
}

func tryGetInterfaceName(pciAddr string) string {
	names, err := dputils.GetNetNames(pciAddr)
	if err != nil || len(names) < 1 {
		return ""
	}
	glog.V(2).Infof("tryGetInterfaceName(): name is %s", names[0])
	return names[0]
}

func getNetdevMTU(pciAddr string) int {
	glog.V(2).Infof("getNetdevMTU(): get MTU for device %s", pciAddr)
	ifaceName := tryGetInterfaceName(pciAddr)
	if ifaceName == "" {
		return 0
	}
	mtuFile := "net/" + ifaceName + "/mtu"
	mtuFilePath := filepath.Join(sysBusPciDevices, pciAddr, mtuFile)
	data, err := ioutil.ReadFile(mtuFilePath)
	if err != nil {
		glog.Warningf("getNetdevMTU(): fail to read mtu file %s", mtuFilePath)
		return 0
	}
	mtu, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		glog.Warningf("getNetdevMTU(): fail to convert mtu %s to int", strings.TrimSpace(string(data)))
		return 0
	}
	return mtu
}

func resetSriovDevice(pciAddr string) error {
	glog.V(2).Infof("resetSriovDevice(): reset sr-iov device %s", pciAddr)
	if err := setSriovNumVfs(pciAddr, 0); err != nil {
		return err
	}
	if err := setNetdevMTU(pciAddr, 1500); err != nil {
		return err
	}
	return nil
}

func getVfInfo(pciAddr string, devices []*ghw.PCIDevice) sriovnetworkv1.VirtualFunction {
	driver, err := dputils.GetDriverName(pciAddr)
	if err != nil {
		glog.Warningf("getVfInfo(): unable to parse device driver for device %s %q", pciAddr, err)
	}
	id, err := dputils.GetVFID(pciAddr)
	if err != nil {
		glog.Warningf("getVfInfo(): unable to get VF index for device %s %q", pciAddr, err)
	}
	vf := sriovnetworkv1.VirtualFunction{
		PciAddress: pciAddr,
		Driver:     driver,
		VfID:       id,
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

func setVfsAdminMac(iface *sriovnetworkv1.InterfaceExt) error {
	glog.Infof("setVfsAdminMac(): device %s", iface.PciAddress)
	pfLink, err := netlink.LinkByName(iface.Name)
	if err != nil {
		glog.Errorf("setVfsAdminMac(): unable to get PF link for device %+v %q", iface, err)
		return err
	}
	vfs, err := dputils.GetVFList(iface.PciAddress)
	if err != nil {
		return err
	}
	for _, addr := range vfs {
		vfID, err := dputils.GetVFID(addr)
		if err != nil {
			glog.Errorf("setVfsAdminMac(): unable to get VF id %+v %q", iface.PciAddress, err)
			return err
		}
		vfName := tryGetInterfaceName(addr)
		vfLink, err := netlink.LinkByName(vfName)
		if err != nil {
			glog.Errorf("setVfsAdminMac(): unable to get VF link for device %+v %q", iface, err)
			return err
		}
		if err := netlink.LinkSetVfHardwareAddr(pfLink, vfID, vfLink.Attrs().HardwareAddr); err != nil {
			return err
		}
		if err = BindDefaultDriver(addr); err != nil {
			return err
		}
	}

	return nil
}

func getLinkType(ifaceStatus sriovnetworkv1.InterfaceExt) string {
	glog.Infof("getLinkType(): Device %s", ifaceStatus.PciAddress)
	if ifaceStatus.Name != "" {
		link, err := netlink.LinkByName(ifaceStatus.Name)
		if err != nil {
			glog.Warningf("getLinkType(): %v", err)
			return ""
		}
		linkType := link.Attrs().EncapType
		if linkType == "ether" {
			return "ETH"
		} else if linkType == "infiniband" {
			return "IB"
		}
	}

	return ""
}
