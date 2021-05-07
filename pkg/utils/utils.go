package utils

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/golang/glog"
	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"
	"github.com/jaypipes/ghw"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

const (
	sysBusPciDevices      = "/sys/bus/pci/devices"
	sysBusPciDrivers      = "/sys/bus/pci/drivers"
	sysBusPciDriversProbe = "/sys/bus/pci/drivers_probe"
	sysClassNet           = "/sys/class/net"
	netClass              = 0x02
	numVfsFile            = "sriov_numvfs"
	scriptsPath           = "bindata/scripts/load-kmod.sh"
	ClusterTypeOpenshift  = "openshift"
	ClusterTypeKubernetes = "kubernetes"
)

var InitialState sriovnetworkv1.SriovNetworkNodeState
var ClusterType string

func init() {
	ClusterType = os.Getenv("CLUSTER_TYPE")
}

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
			iface.Mac = getNetDevMac(name)
			iface.LinkSpeed = getNetDevLinkSpeed(name)
		}
		iface.LinkType = getLinkType(iface)

		if dputils.IsSriovPF(device.Address) {
			iface.TotalVfs = dputils.GetSriovVFcapacity(device.Address)
			iface.NumVfs = dputils.GetVFconfigured(device.Address)
			if iface.EswitchMode, err = GetNicSriovMode(device.Address); err != nil {
				glog.Warningf("DiscoverSriovDevices(): unable to get device mode %+v %q", device.Address, err)
			}
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

// SyncNodeState Attempt to update the node state to match the desired state
//
func SyncNodeState(newState *sriovnetworkv1.SriovNetworkNodeState) error {
	var err error
	for _, ifaceStatus := range newState.Status.Interfaces {
		configured := false
		for _, iface := range newState.Spec.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true
				if iface.EswitchMode == sriovnetworkv1.ESWITCHMODE_SWITCHDEV && ClusterType == ClusterTypeOpenshift {
					// Skip sync for openshift, MCO will inject a systemd service to config switchdev devices.
					break
				}
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
		if !configured && ifaceStatus.NumVfs > 0 && ifaceStatus.EswitchMode != sriovnetworkv1.ESWITCHMODE_SWITCHDEV {
			if err = resetSriovDevice(ifaceStatus); err != nil {
				return err
			}
		}
	}
	return nil
}

func needUpdate(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) bool {
	mtu := ifaceStatus.Mtu
	if iface.Mtu > 0 {
		mtu = iface.Mtu
		if mtu != ifaceStatus.Mtu {
			glog.V(2).Infof("needUpdate(): MTU needs update, desired=%d, current=%d", mtu, ifaceStatus.Mtu)
			return true
		}
	}
	for _, vf := range ifaceStatus.VFs {
		// 0 is a invalid value for mtu as defined in getNetdevMTU
		if vf.Mtu != 0 && vf.Mtu != mtu && !sriovnetworkv1.StringInArray(vf.Driver, DpdkDrivers) {
			glog.V(2).Infof("needUpdate(): VF MTU needs update, desired=%d", mtu)
			return true
		}
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
		if strings.EqualFold(iface.LinkType, "IB") {
			if err = setVfsGuid(iface); err != nil {
				return err
			}
		} else if err = setVfsAdminMac(ifaceStatus); err != nil {
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
				// keep VF MTU align with PF's
				mtu := ifaceStatus.Mtu
				if iface.Mtu > 0 {
					mtu = iface.Mtu
				}

				// validate the device exist only with the default driver
				// TODO: remove this work around after the BZ is close
				// https://bugzilla.redhat.com/show_bug.cgi?id=1875338
				b := backoff.NewConstantBackOff(1 * time.Second)
				err = backoff.Retry(func() error {
					_, err := dputils.GetNetNames(addr)
					if err != nil {
						glog.Warningf("configSriovDevice(): fail to get interface name for %s: %s", addr, err)
						return err
					}
					return nil
				}, backoff.WithMaxRetries(b, 10))
				if err != nil {
					glog.Warningf("configSriovDevice(): unbind PCI %s from default driver and return the error: %v", addr, err)
					err = Unbind(addr)
					if err != nil {
						return fmt.Errorf("configSriovDevice(): failed to unbind PCI %s: %v", addr, err)
					}

					if err := BindDefaultDriver(addr); err != nil {
						glog.Warningf("configSriovDevice(): fail to rebind default driver for device %s", addr)
						return err
					}

					glog.Warningf("configSriovDevice(): W/A rebind for PCI %s", addr)

				}

				// only set MTU for VF with default driver
				if err := setNetdevMTU(addr, mtu); err != nil {
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
	glog.V(2).Infof("setNetdevMTU(): set MTU for device %s to %d", pciAddr, mtu)
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
		if len(ifaceName) < 1 {
			return fmt.Errorf("setNetdevMTU(): interface name is empty")
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

func getNetDevMac(ifaceName string) string {
	glog.V(2).Infof("getNetDevMac(): get Mac for device %s", ifaceName)
	macFilePath := filepath.Join(sysClassNet, ifaceName, "address")
	data, err := ioutil.ReadFile(macFilePath)
	if err != nil {
		glog.Warningf("getNetDevMac(): fail to read Mac file %s", macFilePath)
		return ""
	}

	return strings.TrimSpace(string(data))
}

func getNetDevLinkSpeed(ifaceName string) string {
	glog.V(2).Infof("getNetDevLinkSpeed(): get LinkSpeed for device %s", ifaceName)
	speedFilePath := filepath.Join(sysClassNet, ifaceName, "speed")
	data, err := ioutil.ReadFile(speedFilePath)
	if err != nil {
		glog.Warningf("getNetDevLinkSpeed(): fail to read Link Speed file %s", speedFilePath)
		return ""
	}

	return fmt.Sprintf("%s Mb/s", strings.TrimSpace(string(data)))
}

func resetSriovDevice(ifaceStatus sriovnetworkv1.InterfaceExt) error {
	glog.V(2).Infof("resetSriovDevice(): reset SRIOV device %s", ifaceStatus.PciAddress)
	if err := setSriovNumVfs(ifaceStatus.PciAddress, 0); err != nil {
		return err
	}
	if ifaceStatus.LinkType == "ETH" {
		var mtu int
		is := InitialState.GetInterfaceStateByPciAddress(ifaceStatus.PciAddress)
		if is != nil {
			mtu = is.Mtu
		} else {
			mtu = 1500
		}
		glog.V(2).Infof("resetSriovDevice(): reset mtu to %d", mtu)
		if err := setNetdevMTU(ifaceStatus.PciAddress, mtu); err != nil {
			return err
		}
	} else if ifaceStatus.LinkType == "IB" {
		if err := setNetdevMTU(ifaceStatus.PciAddress, 2048); err != nil {
			return err
		}
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
	if name := tryGetInterfaceName(pciAddr); name != "" {
		vf.Name = name
		vf.Mac = getNetDevMac(name)
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

func vfIsReady(pciAddr string) (netlink.Link, error) {
	glog.Infof("vfIsReady(): VF device %s", pciAddr)
	var err error
	var vfLink netlink.Link
	err = wait.PollImmediate(time.Second, 5*time.Second, func() (bool, error) {
		vfName := tryGetInterfaceName(pciAddr)
		vfLink, err = netlink.LinkByName(vfName)
		if err != nil {
			glog.Errorf("vfIsReady(): unable to get VF link for device %+v, %q", pciAddr, err)
		}
		return err == nil, nil
	})
	if err != nil {
		return vfLink, err
	}
	return vfLink, nil
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
		vfLink, err := vfIsReady(addr)
		if err != nil {
			glog.Errorf("setVfsAdminMac(): VF link is not ready for device %+v %q", addr, err)
			return err
		}
		if err := netlink.LinkSetVfHardwareAddr(pfLink, vfID, vfLink.Attrs().HardwareAddr); err != nil {
			return err
		}
		if err = Unbind(addr); err != nil {
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

func setVfsGuid(iface *sriovnetworkv1.Interface) error {
	glog.Infof("setVfsGuid(): device %s", iface.PciAddress)
	pfLink, err := netlink.LinkByName(iface.Name)
	if err != nil {
		glog.Errorf("setVfsGuid(): unable to get PF link for device %+v %q", iface, err)
		return err
	}
	vfs, err := dputils.GetVFList(iface.PciAddress)
	if err != nil {
		return err
	}
	for _, addr := range vfs {
		vfID, err := dputils.GetVFID(addr)
		if err != nil {
			glog.Errorf("setVfsGuid(): unable to get VF id %+v %q", iface.PciAddress, err)
			return err
		}
		guid := generateRandomGuid()
		if err := netlink.LinkSetVfNodeGUID(pfLink, vfID, guid); err != nil {
			return err
		}
		if err := netlink.LinkSetVfPortGUID(pfLink, vfID, guid); err != nil {
			return err
		}
		if err = Unbind(addr); err != nil {
			return err
		}
		if err = BindDefaultDriver(addr); err != nil {
			return err
		}
	}

	return nil
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

func GetNicSriovMode(pciAddress string) (string, error) {
	glog.V(2).Infof("GetNicSriovMode(): device %s", pciAddress)
	devLink, err := netlink.DevLinkGetDeviceByName("pci", pciAddress)
	if err != nil {
		return "", err
	}
	return devLink.Attrs.Eswitch.Mode, nil
}
