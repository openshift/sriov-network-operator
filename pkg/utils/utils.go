package utils

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/golang/glog"
	"github.com/jaypipes/ghw"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"

	dputils "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/utils"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
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
	VendorMellanox        = "15b3"
	DeviceBF2             = "a2d6"
)

var InitialState sriovnetworkv1.SriovNetworkNodeState
var ClusterType string

var pfPhysPortNameRe = regexp.MustCompile(`p\d+`)

func init() {
	ClusterType = os.Getenv("CLUSTER_TYPE")
}

func DiscoverSriovDevices(withUnsupported bool) ([]sriovnetworkv1.InterfaceExt, error) {
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

		deviceNames, err := dputils.GetNetNames(device.Address)
		if err != nil {
			glog.Warningf("DiscoverSriovDevices(): unable to get device names for device %+v %q", device, err)
			continue
		}

		if len(deviceNames) == 0 {
			// no network devices found, skipping device
			continue
		}

		if !withUnsupported {
			if !sriovnetworkv1.IsSupportedModel(device.Vendor.ID, device.Product.ID) {
				glog.Infof("DiscoverSriovDevices(): unsupported device %+v", device)
				continue
			}
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
func SyncNodeState(newState *sriovnetworkv1.SriovNetworkNodeState, pfsToConfig map[string]bool) error {
	if IsKernelLockdownMode(true) && hasMellanoxInterfacesInSpec(newState) {
		glog.Warningf("cannot use mellanox devices when in kernel lockdown mode")
		return fmt.Errorf("cannot use mellanox devices when in kernel lockdown mode")
	}
	var err error
	for _, ifaceStatus := range newState.Status.Interfaces {
		configured := false
		for _, iface := range newState.Spec.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true

				if skip := pfsToConfig[iface.PciAddress]; skip {
					break
				}

				if !NeedUpdate(&iface, &ifaceStatus) {
					glog.V(2).Infof("syncNodeState(): no need update interface %s", iface.PciAddress)
					break
				}
				if err = configSriovDevice(&iface, &ifaceStatus); err != nil {
					glog.Errorf("SyncNodeState(): fail to configure sriov interface %s: %v. resetting interface.", iface.PciAddress, err)
					if resetErr := resetSriovDevice(ifaceStatus); resetErr != nil {
						glog.Errorf("SyncNodeState(): fail to reset on error SR-IOV interface: %s", resetErr)
					}
					return err
				}
				break
			}
		}
		if !configured && ifaceStatus.NumVfs > 0 {
			if skip := pfsToConfig[ifaceStatus.PciAddress]; skip {
				continue
			}

			if err = resetSriovDevice(ifaceStatus); err != nil {
				return err
			}
		}
	}
	return nil
}

// skipConfigVf Use systemd service to configure switchdev mode or BF-2 NICs in OpenShift
func skipConfigVf(ifSpec sriovnetworkv1.Interface, ifStatus sriovnetworkv1.InterfaceExt) (bool, error) {
	if ifSpec.EswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
		glog.V(2).Infof("skipConfigVf(): skip config VF for switchdev device")
		return true, nil
	}

	// Nvidia_mlx5_MT42822_BlueField-2_integrated_ConnectX-6_Dx in OpenShift
	if ClusterType == ClusterTypeOpenshift && ifStatus.Vendor == VendorMellanox && ifStatus.DeviceID == DeviceBF2 {
		// TODO: remove this when switch to the systemd configuration support.
		mode, err := mellanoxBlueFieldMode(ifStatus.PciAddress)
		if err != nil {
			return false, fmt.Errorf("failed to read Mellanox Bluefield card mode for %s,%v", ifStatus.PciAddress, err)
		}

		if mode == bluefieldConnectXMode {
			return false, nil
		}

		glog.V(2).Infof("skipConfigVf(): skip config VF for Bluefiled card on DPU mode")
		return true, nil
	}

	return false, nil
}

// GetPfsToSkip return a map of devices pci addresses to should be configured via systemd instead if the legacy mode
// we skip devices in switchdev mode and Bluefield card in ConnectX mode
func GetPfsToSkip(ns *sriovnetworkv1.SriovNetworkNodeState) (map[string]bool, error) {
	pfsToSkip := map[string]bool{}
	for _, ifaceStatus := range ns.Status.Interfaces {
		for _, iface := range ns.Spec.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				skip, err := skipConfigVf(iface, ifaceStatus)
				if err != nil {
					glog.Errorf("GetPfsToSkip(): fail to check for skip VFs %s: %v.", iface.PciAddress, err)
					return pfsToSkip, err
				}
				pfsToSkip[iface.PciAddress] = skip
				break
			}
		}
	}

	return pfsToSkip, nil
}

func NeedUpdate(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) bool {
	if iface.Mtu > 0 {
		mtu := iface.Mtu
		if mtu != ifaceStatus.Mtu {
			glog.V(2).Infof("NeedUpdate(): MTU needs update, desired=%d, current=%d", mtu, ifaceStatus.Mtu)
			return true
		}
	}

	if iface.NumVfs != ifaceStatus.NumVfs {
		glog.V(2).Infof("NeedUpdate(): NumVfs needs update desired=%d, current=%d", iface.NumVfs, ifaceStatus.NumVfs)
		return true
	}
	if iface.NumVfs > 0 {
		for _, vf := range ifaceStatus.VFs {
			ingroup := false
			for _, group := range iface.VfGroups {
				if sriovnetworkv1.IndexInRange(vf.VfID, group.VfRange) {
					ingroup = true
					if group.DeviceType != constants.DeviceTypeNetDevice {
						if group.DeviceType != vf.Driver {
							glog.V(2).Infof("NeedUpdate(): Driver needs update, desired=%s, current=%s", group.DeviceType, vf.Driver)
							return true
						}
					} else {
						if sriovnetworkv1.StringInArray(vf.Driver, DpdkDrivers) {
							glog.V(2).Infof("NeedUpdate(): Driver needs update, desired=%s, current=%s", group.DeviceType, vf.Driver)
							return true
						}
						if vf.Mtu != 0 && group.Mtu != 0 && vf.Mtu != group.Mtu {
							glog.V(2).Infof("NeedUpdate(): VF %d MTU needs update, desired=%d, current=%d", vf.VfID, group.Mtu, vf.Mtu)
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
		err := fmt.Errorf("cannot config SRIOV device: NumVfs (%d) is larger than TotalVfs (%d)", iface.NumVfs, ifaceStatus.TotalVfs)
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
		pfLink, err := netlink.LinkByName(iface.Name)
		if err != nil {
			glog.Errorf("configSriovDevice(): unable to get PF link for device %+v %q", iface, err)
			return err
		}

		for _, addr := range vfAddrs {
			var group sriovnetworkv1.VfGroup
			i := 0
			var dpdkDriver string
			var isRdma bool
			vfID, err := dputils.GetVFID(addr)
			for i, group = range iface.VfGroups {
				if err != nil {
					glog.Warningf("configSriovDevice(): unable to get VF id %+v %q", iface.PciAddress, err)
				}
				if sriovnetworkv1.IndexInRange(vfID, group.VfRange) {
					isRdma = group.IsRdma
					if sriovnetworkv1.StringInArray(group.DeviceType, DpdkDrivers) {
						dpdkDriver = group.DeviceType
					}
					break
				}
			}

			// only set GUID and MAC for VF with default driver
			// for userspace drivers like vfio we configure the vf mac using the kernel nic mac address
			// before we switch to the userspace driver
			if yes, d := hasDriver(addr); yes && !sriovnetworkv1.StringInArray(d, DpdkDrivers) {
				// LinkType is an optional field. Let's fallback to current link type
				// if nothing is specified in the SriovNodePolicy
				linkType := iface.LinkType
				if linkType == "" {
					linkType = ifaceStatus.LinkType
				}
				if strings.EqualFold(linkType, constants.LinkTypeIB) {
					if err = setVfGUID(addr, pfLink); err != nil {
						return err
					}
				} else {
					vfLink, err := vfIsReady(addr)
					if err != nil {
						glog.Errorf("configSriovDevice(): VF link is not ready for device %s %q", addr, err)
						err = RebindVfToDefaultDriver(addr)
						if err != nil {
							glog.Errorf("configSriovDevice(): failed to rebind VF %s %q", addr, err)
							return err
						}

						// Try to check the VF status again
						vfLink, err = vfIsReady(addr)
						if err != nil {
							glog.Errorf("configSriovDevice(): VF link is not ready for device %s %q", addr, err)
							return err
						}
					}
					if err = setVfAdminMac(addr, pfLink, vfLink); err != nil {
						glog.Errorf("configSriovDevice(): fail to configure VF admin mac address for device %s %q", addr, err)
						return err
					}
				}
			}

			if err = unbindDriverIfNeeded(addr, isRdma); err != nil {
				return err
			}

			if dpdkDriver == "" {
				if err := BindDefaultDriver(addr); err != nil {
					glog.Warningf("configSriovDevice(): fail to bind default driver for device %s", addr)
					return err
				}
				// only set MTU for VF with default driver
				if iface.VfGroups[i].Mtu > 0 {
					if err := setNetdevMTU(addr, iface.VfGroups[i].Mtu); err != nil {
						glog.Warningf("configSriovDevice(): fail to set mtu for VF %s: %v", addr, err)
						return err
					}
				}
			} else {
				if err := BindDpdkDriver(addr, dpdkDriver); err != nil {
					glog.Warningf("configSriovDevice(): fail to bind driver %s for device %s", dpdkDriver, addr)
					return err
				}
			}
		}
	}
	// Set PF link up
	pfLink, err := netlink.LinkByName(ifaceStatus.Name)
	if err != nil {
		return err
	}
	if pfLink.Attrs().OperState != netlink.OperUp {
		err = netlink.LinkSetUp(pfLink)
		if err != nil {
			return err
		}
	}
	return nil
}

func setSriovNumVfs(pciAddr string, numVfs int) error {
	glog.V(2).Infof("setSriovNumVfs(): set NumVfs for device %s to %d", pciAddr, numVfs)
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
	netDevName := names[0]

	// Switchdev PF and their VFs representors are existing under the same PCI address since kernel 5.8
	// if device is switchdev then return PF name
	for _, name := range names {
		if !isSwitchdev(name) {
			continue
		}
		// Try to get the phys port name, if not exists then fallback to check without it
		// phys_port_name should be in formant p<port-num> e.g p0,p1,p2 ...etc.
		if physPortName, err := GetPhysPortName(name); err == nil {
			if !pfPhysPortNameRe.MatchString(physPortName) {
				continue
			}
		}
		return name
	}

	glog.V(2).Infof("tryGetInterfaceName(): name is %s", netDevName)
	return netDevName
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
	if ifaceStatus.LinkType == constants.LinkTypeETH {
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
	} else if ifaceStatus.LinkType == constants.LinkTypeIB {
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

func LoadKernelModule(name string, args ...string) error {
	glog.Infof("LoadKernelModule(): try to load kernel module %s with arguments '%s'", name, args)
	cmdArgs := strings.Join(args, " ")
	cmd := exec.Command("/bin/sh", scriptsPath, name, cmdArgs)
	err := cmd.Run()
	if err != nil {
		glog.Errorf("LoadKernelModule(): fail to load kernel module %s with arguments '%s': %v", name, args, err)
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
	err = wait.PollImmediate(time.Second, 10*time.Second, func() (bool, error) {
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

func setVfAdminMac(vfAddr string, pfLink, vfLink netlink.Link) error {
	glog.Infof("setVfAdminMac(): VF %s", vfAddr)

	vfID, err := dputils.GetVFID(vfAddr)
	if err != nil {
		glog.Errorf("setVfAdminMac(): unable to get VF id %+v %q", vfAddr, err)
		return err
	}

	if err := netlink.LinkSetVfHardwareAddr(pfLink, vfID, vfLink.Attrs().HardwareAddr); err != nil {
		return err
	}

	return nil
}

func unbindDriverIfNeeded(vfAddr string, isRdma bool) error {
	if isRdma {
		glog.Infof("unbindDriverIfNeeded(): unbind driver for %s", vfAddr)
		if err := Unbind(vfAddr); err != nil {
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
			return constants.LinkTypeETH
		} else if linkType == "infiniband" {
			return constants.LinkTypeIB
		}
	}

	return ""
}

func setVfGUID(vfAddr string, pfLink netlink.Link) error {
	glog.Infof("setVfGuid(): VF %s", vfAddr)
	vfID, err := dputils.GetVFID(vfAddr)
	if err != nil {
		glog.Errorf("setVfGuid(): unable to get VF id %+v %q", vfAddr, err)
		return err
	}
	guid := generateRandomGUID()
	if err := netlink.LinkSetVfNodeGUID(pfLink, vfID, guid); err != nil {
		return err
	}
	if err := netlink.LinkSetVfPortGUID(pfLink, vfID, guid); err != nil {
		return err
	}
	if err = Unbind(vfAddr); err != nil {
		return err
	}

	return nil
}

func generateRandomGUID() net.HardwareAddr {
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

func GetPhysSwitchID(name string) (string, error) {
	swIDFile := filepath.Join(sysClassNet, name, "phys_switch_id")
	physSwitchID, err := ioutil.ReadFile(swIDFile)
	if err != nil {
		return "", err
	}
	if physSwitchID != nil {
		return strings.TrimSpace(string(physSwitchID)), nil
	}
	return "", nil
}

func GetPhysPortName(name string) (string, error) {
	devicePortNameFile := filepath.Join(sysClassNet, name, "phys_port_name")
	physPortName, err := ioutil.ReadFile(devicePortNameFile)
	if err != nil {
		return "", err
	}
	if physPortName != nil {
		return strings.TrimSpace(string(physPortName)), nil
	}
	return "", nil
}

func isSwitchdev(name string) bool {
	switchID, err := GetPhysSwitchID(name)
	if err != nil || switchID == "" {
		return false
	}

	return true
}

// IsKernelLockdownMode returns true when kernel lockdown mode is enabled
func IsKernelLockdownMode(chroot bool) bool {
	path := "/sys/kernel/security/lockdown"
	if !chroot {
		path = "/host" + path
	}
	out, err := RunCommand("cat", path)
	glog.V(2).Infof("IsKernelLockdownMode(): %s, %+v", out, err)
	if err != nil {
		return false
	}
	return strings.Contains(out, "[integrity]") || strings.Contains(out, "[confidentiality]")
}

// RunCommand runs a command
func RunCommand(command string, args ...string) (string, error) {
	glog.Infof("RunCommand(): %s %v", command, args)
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	glog.V(2).Infof("RunCommand(): out:(%s), err:(%v)", stdout.String(), err)
	return stdout.String(), err
}

func hasMellanoxInterfacesInSpec(newState *sriovnetworkv1.SriovNetworkNodeState) bool {
	for _, ifaceStatus := range newState.Status.Interfaces {
		if ifaceStatus.Vendor == VendorMellanox {
			for _, iface := range newState.Spec.Interfaces {
				if iface.PciAddress == ifaceStatus.PciAddress {
					glog.V(2).Infof("hasMellanoxInterfacesInSpec(): Mellanox device %s (pci: %s) specified in SriovNetworkNodeState spec", ifaceStatus.Name, ifaceStatus.PciAddress)
					return true
				}
			}
		}
	}
	return false
}

// Workaround function to handle a case where the vf default driver is stuck and not able to create the vf kernel interface.
// This function unbind the VF from the default driver and try to bind it again
// bugzilla: https://bugzilla.redhat.com/show_bug.cgi?id=2045087
func RebindVfToDefaultDriver(vfAddr string) error {
	glog.Infof("RebindVfToDefaultDriver(): VF %s", vfAddr)
	if err := Unbind(vfAddr); err != nil {
		return err
	}
	if err := BindDefaultDriver(vfAddr); err != nil {
		glog.Errorf("RebindVfToDefaultDriver(): fail to bind default driver for device %s", vfAddr)
		return err
	}

	glog.Warningf("RebindVfToDefaultDriver(): workaround implemented for VF %s", vfAddr)
	return nil
}
