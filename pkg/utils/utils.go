package utils

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"encoding/hex"
	"hash/fnv"
	"sort"

	corev1 "k8s.io/api/core/v1"

	"github.com/cenkalti/backoff"
	"github.com/jaypipes/ghw"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dputils "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/utils"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

const (
	sysBusPciDevices      = "/sys/bus/pci/devices"
	sysBusPciDrivers      = "/sys/bus/pci/drivers"
	sysBusPciDriversProbe = "/sys/bus/pci/drivers_probe"
	sysClassNet           = "/sys/class/net"
	procKernelCmdLine     = "/proc/cmdline"
	netClass              = 0x02
	numVfsFile            = "sriov_numvfs"

	ClusterTypeOpenshift  = "openshift"
	ClusterTypeKubernetes = "kubernetes"
	VendorMellanox        = "15b3"
	DeviceBF2             = "a2d6"
	DeviceBF3             = "a2dc"

	udevFolder      = "/etc/udev"
	udevRulesFolder = udevFolder + "/rules.d"
	udevDisableNM   = "/bindata/scripts/udev-find-sriov-pf.sh"
	nmUdevRule      = "SUBSYSTEM==\"net\", ACTION==\"add|change|move\", ATTRS{device}==\"%s\", IMPORT{program}=\"/etc/udev/disable-nm-sriov.sh $env{INTERFACE} %s\""

	KernelArgPciRealloc = "pci=realloc"
	KernelArgIntelIommu = "intel_iommu=on"
	KernelArgIommuPt    = "iommu=pt"
)

var InitialState sriovnetworkv1.SriovNetworkNodeState
var ClusterType string

var pfPhysPortNameRe = regexp.MustCompile(`p\d+`)

// FilesystemRoot used by test to mock interactions with filesystem
var FilesystemRoot = ""

var SupportedVfIds []string

func init() {
	ClusterType = os.Getenv("CLUSTER_TYPE")
}

// GetCurrentKernelArgs This retrieves the kernel cmd line arguments
func GetCurrentKernelArgs(chroot bool) (string, error) {
	path := procKernelCmdLine
	if !chroot {
		path = "/host" + path
	}
	cmdLine, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("GetCurrentKernelArgs(): Error reading %s: %v", procKernelCmdLine, err)
	}
	return string(cmdLine), nil
}

// IsKernelArgsSet This checks if the kernel cmd line is set properly. Please note that the same key could be repeated
// several times in the kernel cmd line. We can only ensure that the kernel cmd line has the key/val kernel arg that we set.
func IsKernelArgsSet(cmdLine string, karg string) bool {
	elements := strings.Fields(cmdLine)
	for _, element := range elements {
		if element == karg {
			return true
		}
	}
	return false
}

func DiscoverSriovDevices(withUnsupported bool, storeManager StoreManagerInterface) ([]sriovnetworkv1.InterfaceExt, error) {
	log.Log.V(2).Info("DiscoverSriovDevices")
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
			log.Log.Error(err, "DiscoverSriovDevices(): unable to parse device class, skipping",
				"device", device)
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
			log.Log.Error(err, "DiscoverSriovDevices(): unable to parse device driver for device, skipping", "device", device)
			continue
		}

		deviceNames, err := dputils.GetNetNames(device.Address)
		if err != nil {
			log.Log.Error(err, "DiscoverSriovDevices(): unable to get device names for device, skipping", "device", device)
			continue
		}

		if len(deviceNames) == 0 {
			// no network devices found, skipping device
			continue
		}

		if !withUnsupported {
			if !sriovnetworkv1.IsSupportedModel(device.Vendor.ID, device.Product.ID) {
				log.Log.Info("DiscoverSriovDevices(): unsupported device", "device", device)
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

		pfStatus, exist, err := storeManager.LoadPfsStatus(iface.PciAddress)
		if err != nil {
			log.Log.Error(err, "DiscoverSriovDevices(): failed to load PF status from disk")
		} else {
			if exist {
				iface.ExternallyManaged = pfStatus.ExternallyManaged
			}
		}

		if dputils.IsSriovPF(device.Address) {
			iface.TotalVfs = dputils.GetSriovVFcapacity(device.Address)
			iface.NumVfs = dputils.GetVFconfigured(device.Address)
			if iface.EswitchMode, err = GetNicSriovMode(device.Address); err != nil {
				log.Log.Error(err, "DiscoverSriovDevices(): warning, unable to get device eswitch mode",
					"device", device.Address)
			}
			if dputils.SriovConfigured(device.Address) {
				vfs, err := dputils.GetVFList(device.Address)
				if err != nil {
					log.Log.Error(err, "DiscoverSriovDevices(): unable to parse VFs for device, skipping",
						"device", device)
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
func SyncNodeState(newState *sriovnetworkv1.SriovNetworkNodeState, pfsToConfig map[string]bool) error {
	return ConfigSriovInterfaces(newState.Spec.Interfaces, newState.Status.Interfaces, pfsToConfig)
}

func ConfigSriovInterfaces(interfaces []sriovnetworkv1.Interface, ifaceStatuses []sriovnetworkv1.InterfaceExt, pfsToConfig map[string]bool) error {
	if IsKernelLockdownMode(true) && hasMellanoxInterfacesInSpec(ifaceStatuses, interfaces) {
		log.Log.Error(nil, "cannot use mellanox devices when in kernel lockdown mode")
		return fmt.Errorf("cannot use mellanox devices when in kernel lockdown mode")
	}

	// we are already inside chroot, so we initialize the store as running on host
	storeManager, err := NewStoreManager(true)
	if err != nil {
		return fmt.Errorf("SyncNodeState(): error initializing storeManager: %v", err)
	}

	for _, ifaceStatus := range ifaceStatuses {
		configured := false
		for _, iface := range interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true

				if skip := pfsToConfig[iface.PciAddress]; skip {
					break
				}

				if !NeedUpdate(&iface, &ifaceStatus) {
					log.Log.V(2).Info("syncNodeState(): no need update interface", "address", iface.PciAddress)

					// Save the PF status to the host
					err = storeManager.SaveLastPfAppliedStatus(&iface)
					if err != nil {
						log.Log.Error(err, "SyncNodeState(): failed to save PF applied config to host")
						return err
					}

					break
				}
				if err = configSriovDevice(&iface, &ifaceStatus); err != nil {
					log.Log.Error(err, "SyncNodeState(): fail to configure sriov interface. resetting interface.", "address", iface.PciAddress)
					if iface.ExternallyManaged {
						log.Log.Info("SyncNodeState(): skipping device reset as the nic is marked as externally created")
					} else {
						if resetErr := resetSriovDevice(ifaceStatus); resetErr != nil {
							log.Log.Error(resetErr, "SyncNodeState(): failed to reset on error SR-IOV interface")
						}
					}
					return err
				}

				// Save the PF status to the host
				err = storeManager.SaveLastPfAppliedStatus(&iface)
				if err != nil {
					log.Log.Error(err, "SyncNodeState(): failed to save PF applied config to host")
					return err
				}
				break
			}
		}
		if !configured && ifaceStatus.NumVfs > 0 {
			if skip := pfsToConfig[ifaceStatus.PciAddress]; skip {
				continue
			}

			// load the PF info
			pfStatus, exist, err := storeManager.LoadPfsStatus(ifaceStatus.PciAddress)
			if err != nil {
				log.Log.Error(err, "SyncNodeState(): failed to load info about PF status for device",
					"address", ifaceStatus.PciAddress)
				return err
			}

			if !exist {
				log.Log.Info("SyncNodeState(): PF name with pci address has VFs configured but they weren't created by the sriov operator. Skipping the device reset",
					"pf-name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			}

			if pfStatus.ExternallyManaged {
				log.Log.Info("SyncNodeState(): PF name with pci address was externally created skipping the device reset",
					"pf-name", ifaceStatus.Name,
					"address", ifaceStatus.PciAddress)
				continue
			} else {
				err = RemoveUdevRule(ifaceStatus.PciAddress)
				if err != nil {
					return err
				}
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
		log.Log.V(2).Info("skipConfigVf(): skip config VF for switchdev device")
		return true, nil
	}

	//  NVIDIA BlueField 2 and BlueField3 in OpenShift
	if ClusterType == ClusterTypeOpenshift && ifStatus.Vendor == VendorMellanox && (ifStatus.DeviceID == DeviceBF2 || ifStatus.DeviceID == DeviceBF3) {
		// TODO: remove this when switch to the systemd configuration support.
		mode, err := mellanoxBlueFieldMode(ifStatus.PciAddress)
		if err != nil {
			return false, fmt.Errorf("failed to read Mellanox Bluefield card mode for %s,%v", ifStatus.PciAddress, err)
		}

		if mode == bluefieldConnectXMode {
			return false, nil
		}

		log.Log.V(2).Info("skipConfigVf(): skip config VF for Bluefiled card on DPU mode")
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
					log.Log.Error(err, "GetPfsToSkip(): fail to check for skip VFs", "device", iface.PciAddress)
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
			log.Log.V(2).Info("NeedUpdate(): MTU needs update", "desired", mtu, "current", ifaceStatus.Mtu)
			return true
		}
	}

	if iface.NumVfs != ifaceStatus.NumVfs {
		log.Log.V(2).Info("NeedUpdate(): NumVfs needs update", "desired", iface.NumVfs, "current", ifaceStatus.NumVfs)
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
							log.Log.V(2).Info("NeedUpdate(): Driver needs update",
								"desired", group.DeviceType, "current", vf.Driver)
							return true
						}
					} else {
						if sriovnetworkv1.StringInArray(vf.Driver, DpdkDrivers) {
							log.Log.V(2).Info("NeedUpdate(): Driver needs update",
								"desired", group.DeviceType, "current", vf.Driver)
							return true
						}
						if vf.Mtu != 0 && group.Mtu != 0 && vf.Mtu != group.Mtu {
							log.Log.V(2).Info("NeedUpdate(): VF MTU needs update",
								"vf", vf.VfID, "desired", group.Mtu, "current", vf.Mtu)
							return true
						}

						// this is needed to be sure the admin mac address is configured as expected
						if iface.ExternallyManaged {
							log.Log.V(2).Info("NeedUpdate(): need to update the device as it's externally manage",
								"device", ifaceStatus.PciAddress)
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
	log.Log.V(2).Info("configSriovDevice(): configure sriov device",
		"device", iface.PciAddress, "config", iface)
	var err error
	if iface.NumVfs > ifaceStatus.TotalVfs {
		err := fmt.Errorf("cannot config SRIOV device: NumVfs (%d) is larger than TotalVfs (%d)", iface.NumVfs, ifaceStatus.TotalVfs)
		log.Log.Error(err, "configSriovDevice(): fail to set NumVfs for device", "device", iface.PciAddress)
		return err
	}
	// set numVFs
	if iface.NumVfs != ifaceStatus.NumVfs {
		if iface.ExternallyManaged {
			if iface.NumVfs > ifaceStatus.NumVfs {
				errMsg := fmt.Sprintf("configSriovDevice(): number of request virtual functions %d is not equal to configured virtual functions %d but the policy is configured as ExternallyManaged for device %s", iface.NumVfs, ifaceStatus.NumVfs, iface.PciAddress)
				log.Log.Error(nil, errMsg)
				return fmt.Errorf(errMsg)
			}
		} else {
			// create the udev rule to disable all the vfs from network manager as this vfs are managed by the operator
			err = AddUdevRule(iface.PciAddress)
			if err != nil {
				return err
			}

			err = setSriovNumVfs(iface.PciAddress, iface.NumVfs)
			if err != nil {
				log.Log.Error(err, "configSriovDevice(): fail to set NumVfs for device", "device", iface.PciAddress)
				errRemove := RemoveUdevRule(iface.PciAddress)
				if errRemove != nil {
					log.Log.Error(errRemove, "configSriovDevice(): fail to remove udev rule", "device", iface.PciAddress)
				}
				return err
			}
		}
	}
	// set PF mtu
	if iface.Mtu > 0 && iface.Mtu > ifaceStatus.Mtu {
		err = setNetdevMTU(iface.PciAddress, iface.Mtu)
		if err != nil {
			log.Log.Error(err, "configSriovDevice(): fail to set mtu for PF", "device", iface.PciAddress)
			return err
		}
	}
	// Config VFs
	if iface.NumVfs > 0 {
		vfAddrs, err := dputils.GetVFList(iface.PciAddress)
		if err != nil {
			log.Log.Error(err, "configSriovDevice(): unable to parse VFs for device", "device", iface.PciAddress)
		}
		pfLink, err := netlink.LinkByName(iface.Name)
		if err != nil {
			log.Log.Error(err, "configSriovDevice(): unable to get PF link for device", "device", iface)
			return err
		}

		for _, addr := range vfAddrs {
			var group *sriovnetworkv1.VfGroup

			vfID, err := dputils.GetVFID(addr)
			if err != nil {
				log.Log.Error(err, "configSriovDevice(): unable to get VF id", "device", iface.PciAddress)
				return err
			}

			for i := range iface.VfGroups {
				if sriovnetworkv1.IndexInRange(vfID, iface.VfGroups[i].VfRange) {
					group = &iface.VfGroups[i]
					break
				}
			}

			// VF group not found.
			if group == nil {
				continue
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
						log.Log.Error(err, "configSriovDevice(): VF link is not ready", "address", addr)
						err = RebindVfToDefaultDriver(addr)
						if err != nil {
							log.Log.Error(err, "configSriovDevice(): failed to rebind VF", "address", addr)
							return err
						}

						// Try to check the VF status again
						vfLink, err = vfIsReady(addr)
						if err != nil {
							log.Log.Error(err, "configSriovDevice(): VF link is not ready", "address", addr)
							return err
						}
					}
					if err = setVfAdminMac(addr, pfLink, vfLink); err != nil {
						log.Log.Error(err, "configSriovDevice(): fail to configure VF admin mac", "device", addr)
						return err
					}
				}
			}

			if err = unbindDriverIfNeeded(addr, group.IsRdma); err != nil {
				return err
			}

			if !sriovnetworkv1.StringInArray(group.DeviceType, DpdkDrivers) {
				if err := BindDefaultDriver(addr); err != nil {
					log.Log.Error(err, "configSriovDevice(): fail to bind default driver for device", "device", addr)
					return err
				}
				// only set MTU for VF with default driver
				if group.Mtu > 0 {
					if err := setNetdevMTU(addr, group.Mtu); err != nil {
						log.Log.Error(err, "configSriovDevice(): fail to set mtu for VF", "address", addr)
						return err
					}
				}
			} else {
				if err := BindDpdkDriver(addr, group.DeviceType); err != nil {
					log.Log.Error(err, "configSriovDevice(): fail to bind driver for device",
						"driver", group.DeviceType, "device", addr)
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
	log.Log.V(2).Info("setSriovNumVfs(): set NumVfs", "device", pciAddr, "numVfs", numVfs)
	numVfsFilePath := filepath.Join(sysBusPciDevices, pciAddr, numVfsFile)
	bs := []byte(strconv.Itoa(numVfs))
	err := os.WriteFile(numVfsFilePath, []byte("0"), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "setSriovNumVfs(): fail to reset NumVfs file", "path", numVfsFilePath)
		return err
	}
	err = os.WriteFile(numVfsFilePath, bs, os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "setSriovNumVfs(): fail to set NumVfs file", "path", numVfsFilePath)
		return err
	}
	return nil
}

func setNetdevMTU(pciAddr string, mtu int) error {
	log.Log.V(2).Info("setNetdevMTU(): set MTU", "device", pciAddr, "mtu", mtu)
	if mtu <= 0 {
		log.Log.V(2).Info("setNetdevMTU(): refusing to set MTU", "mtu", mtu)
		return nil
	}
	b := backoff.NewConstantBackOff(1 * time.Second)
	err := backoff.Retry(func() error {
		ifaceName, err := dputils.GetNetNames(pciAddr)
		if err != nil {
			log.Log.Error(err, "setNetdevMTU(): fail to get interface name", "device", pciAddr)
			return err
		}
		if len(ifaceName) < 1 {
			return fmt.Errorf("setNetdevMTU(): interface name is empty")
		}
		mtuFile := "net/" + ifaceName[0] + "/mtu"
		mtuFilePath := filepath.Join(sysBusPciDevices, pciAddr, mtuFile)
		return os.WriteFile(mtuFilePath, []byte(strconv.Itoa(mtu)), os.ModeAppend)
	}, backoff.WithMaxRetries(b, 10))
	if err != nil {
		log.Log.Error(err, "setNetdevMTU(): fail to write mtu file after retrying")
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

	log.Log.V(2).Info("tryGetInterfaceName()", "name", netDevName)
	return netDevName
}

func getNetdevMTU(pciAddr string) int {
	log.Log.V(2).Info("getNetdevMTU(): get MTU", "device", pciAddr)
	ifaceName := tryGetInterfaceName(pciAddr)
	if ifaceName == "" {
		return 0
	}
	mtuFile := "net/" + ifaceName + "/mtu"
	mtuFilePath := filepath.Join(sysBusPciDevices, pciAddr, mtuFile)
	data, err := os.ReadFile(mtuFilePath)
	if err != nil {
		log.Log.Error(err, "getNetdevMTU(): fail to read mtu file", "path", mtuFilePath)
		return 0
	}
	mtu, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		log.Log.Error(err, "getNetdevMTU(): fail to convert mtu to int", "raw-mtu", strings.TrimSpace(string(data)))
		return 0
	}
	return mtu
}

func getNetDevMac(ifaceName string) string {
	log.Log.V(2).Info("getNetDevMac(): get Mac", "device", ifaceName)
	macFilePath := filepath.Join(sysClassNet, ifaceName, "address")
	data, err := os.ReadFile(macFilePath)
	if err != nil {
		log.Log.Error(err, "getNetDevMac(): fail to read Mac file", "path", macFilePath)
		return ""
	}

	return strings.TrimSpace(string(data))
}

func getNetDevLinkSpeed(ifaceName string) string {
	log.Log.V(2).Info("getNetDevLinkSpeed(): get LinkSpeed", "device", ifaceName)
	speedFilePath := filepath.Join(sysClassNet, ifaceName, "speed")
	data, err := os.ReadFile(speedFilePath)
	if err != nil {
		log.Log.Error(err, "getNetDevLinkSpeed(): fail to read Link Speed file", "path", speedFilePath)
		return ""
	}

	return fmt.Sprintf("%s Mb/s", strings.TrimSpace(string(data)))
}

func resetSriovDevice(ifaceStatus sriovnetworkv1.InterfaceExt) error {
	log.Log.V(2).Info("resetSriovDevice(): reset SRIOV device", "address", ifaceStatus.PciAddress)
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
		log.Log.V(2).Info("resetSriovDevice(): reset mtu", "value", mtu)
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
		log.Log.Error(err, "getVfInfo(): unable to parse device driver", "device", pciAddr)
	}
	id, err := dputils.GetVFID(pciAddr)
	if err != nil {
		log.Log.Error(err, "getVfInfo(): unable to get VF index", "device", pciAddr)
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
	log.Log.Info("vfIsReady()", "device", pciAddr)
	var err error
	var vfLink netlink.Link
	err = wait.PollImmediate(time.Second, 10*time.Second, func() (bool, error) {
		vfName := tryGetInterfaceName(pciAddr)
		vfLink, err = netlink.LinkByName(vfName)
		if err != nil {
			log.Log.Error(err, "vfIsReady(): unable to get VF link", "device", pciAddr)
		}
		return err == nil, nil
	})
	if err != nil {
		return vfLink, err
	}
	return vfLink, nil
}

func setVfAdminMac(vfAddr string, pfLink, vfLink netlink.Link) error {
	log.Log.Info("setVfAdminMac()", "vf", vfAddr)

	vfID, err := dputils.GetVFID(vfAddr)
	if err != nil {
		log.Log.Error(err, "setVfAdminMac(): unable to get VF id", "address", vfAddr)
		return err
	}

	if err := netlink.LinkSetVfHardwareAddr(pfLink, vfID, vfLink.Attrs().HardwareAddr); err != nil {
		return err
	}

	return nil
}

func unbindDriverIfNeeded(vfAddr string, isRdma bool) error {
	if isRdma {
		log.Log.Info("unbindDriverIfNeeded(): unbind driver", "device", vfAddr)
		if err := Unbind(vfAddr); err != nil {
			return err
		}
	}
	return nil
}

func getLinkType(ifaceStatus sriovnetworkv1.InterfaceExt) string {
	log.Log.V(2).Info("getLinkType()", "device", ifaceStatus.PciAddress)
	if ifaceStatus.Name != "" {
		link, err := netlink.LinkByName(ifaceStatus.Name)
		if err != nil {
			log.Log.Error(err, "getLinkType(): failed to get link", "device", ifaceStatus.Name)
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
	log.Log.Info("setVfGuid()", "vf", vfAddr)
	vfID, err := dputils.GetVFID(vfAddr)
	if err != nil {
		log.Log.Error(err, "setVfGuid(): unable to get VF id", "address", vfAddr)
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
	log.Log.V(2).Info("GetNicSriovMode()", "device", pciAddress)

	devLink, err := netlink.DevLinkGetDeviceByName("pci", pciAddress)
	if err != nil {
		if errors.Is(err, syscall.ENODEV) {
			// the device doesn't support devlink
			return "", nil
		}
		return "", err
	}

	return devLink.Attrs.Eswitch.Mode, nil
}

func GetPhysSwitchID(name string) (string, error) {
	swIDFile := filepath.Join(sysClassNet, name, "phys_switch_id")
	physSwitchID, err := os.ReadFile(swIDFile)
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
	physPortName, err := os.ReadFile(devicePortNameFile)
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
	log.Log.V(2).Info("IsKernelLockdownMode()", "output", out, "error", err)
	if err != nil {
		return false
	}
	return strings.Contains(out, "[integrity]") || strings.Contains(out, "[confidentiality]")
}

// RunCommand runs a command
func RunCommand(command string, args ...string) (string, error) {
	log.Log.Info("RunCommand()", "command", command, "args", args)
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	log.Log.V(2).Info("RunCommand()", "output", stdout.String(), "error", err)
	return stdout.String(), err
}

func HashConfigMap(cm *corev1.ConfigMap) string {
	var keys []string
	for k := range cm.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	hash := fnv.New128()
	for _, k := range keys {
		hash.Write([]byte(k))
		hash.Write([]byte(cm.Data[k]))
	}
	hashed := hash.Sum(nil)
	return hex.EncodeToString(hashed)
}

func hasMellanoxInterfacesInSpec(ifaceStatuses sriovnetworkv1.InterfaceExts, ifaceSpecs sriovnetworkv1.Interfaces) bool {
	for _, ifaceStatus := range ifaceStatuses {
		if ifaceStatus.Vendor == VendorMellanox {
			for _, iface := range ifaceSpecs {
				if iface.PciAddress == ifaceStatus.PciAddress {
					log.Log.V(2).Info("hasMellanoxInterfacesInSpec(): Mellanox device specified in SriovNetworkNodeState spec",
						"name", ifaceStatus.Name,
						"address", ifaceStatus.PciAddress)
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
	log.Log.Info("RebindVfToDefaultDriver()", "vf", vfAddr)
	if err := Unbind(vfAddr); err != nil {
		return err
	}
	if err := BindDefaultDriver(vfAddr); err != nil {
		log.Log.Error(err, "RebindVfToDefaultDriver(): fail to bind default driver", "device", vfAddr)
		return err
	}

	log.Log.Info("RebindVfToDefaultDriver(): workaround implemented", "vf", vfAddr)
	return nil
}

func PrepareNMUdevRule(supportedVfIds []string) error {
	log.Log.V(2).Info("PrepareNMUdevRule()")
	dirPath := path.Join(FilesystemRoot, "/host/etc/udev/rules.d")
	filePath := path.Join(dirPath, "10-nm-unmanaged.rules")

	// remove the old unmanaged rules file
	if _, err := os.Stat(filePath); err == nil {
		err = os.Remove(filePath)
		if err != nil {
			log.Log.Error(err, "failed to remove the network manager global unmanaged rule",
				"path", filePath)
		}
	}

	// create the pf finder script for udev rules
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/bash", path.Join(FilesystemRoot, udevDisableNM))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Log.Error(err, "PrepareNMUdevRule(): failed to prepare nmUdevRule", "stderr", stderr.String())
		return err
	}
	log.Log.V(2).Info("PrepareNMUdevRule()", "stdout", stdout.String())

	//save the device list to use for udev rules
	SupportedVfIds = supportedVfIds
	return nil
}

func AddUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("AddUdevRule()", "device", pfPciAddress)
	pathFile := udevRulesFolder
	udevRuleContent := fmt.Sprintf(nmUdevRule, strings.Join(SupportedVfIds, "|"), pfPciAddress)

	err := os.MkdirAll(pathFile, os.ModePerm)
	if err != nil && !os.IsExist(err) {
		log.Log.Error(err, "AddUdevRule(): failed to create dir", "path", pathFile)
		return err
	}

	filePath := path.Join(pathFile, fmt.Sprintf("10-nm-disable-%s.rules", pfPciAddress))
	// if the file does not exist or if oldContent != newContent
	// write to file and create it if it doesn't exist
	err = os.WriteFile(filePath, []byte(udevRuleContent), 0666)
	if err != nil {
		log.Log.Error(err, "AddUdevRule(): fail to write file", "path", filePath)
		return err
	}
	return nil
}

func RemoveUdevRule(pfPciAddress string) error {
	pathFile := udevRulesFolder
	filePath := path.Join(pathFile, fmt.Sprintf("10-nm-disable-%s.rules", pfPciAddress))
	err := os.Remove(filePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
