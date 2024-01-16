package sriov

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jaypipes/ghw"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dputils "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/utils"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	netlinkPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/store"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	mlx "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vendors/mellanox"
)

type sriov struct {
	utilsHelper   utils.CmdInterface
	kernelHelper  types.KernelInterface
	networkHelper types.NetworkInterface
	udevHelper    types.UdevInterface
	netlinkLib    netlinkPkg.NetlinkLib
}

func New(utilsHelper utils.CmdInterface,
	kernelHelper types.KernelInterface,
	networkHelper types.NetworkInterface,
	udevHelper types.UdevInterface,
	netlinkLib netlinkPkg.NetlinkLib) types.SriovInterface {
	return &sriov{utilsHelper: utilsHelper,
		kernelHelper:  kernelHelper,
		networkHelper: networkHelper,
		udevHelper:    udevHelper,
		netlinkLib:    netlinkLib,
	}
}

func (s *sriov) SetSriovNumVfs(pciAddr string, numVfs int) error {
	log.Log.V(2).Info("SetSriovNumVfs(): set NumVfs", "device", pciAddr, "numVfs", numVfs)
	numVfsFilePath := filepath.Join(vars.FilesystemRoot, consts.SysBusPciDevices, pciAddr, consts.NumVfsFile)
	bs := []byte(strconv.Itoa(numVfs))
	err := os.WriteFile(numVfsFilePath, []byte("0"), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "SetSriovNumVfs(): fail to reset NumVfs file", "path", numVfsFilePath)
		return err
	}
	if numVfs == 0 {
		return nil
	}
	err = os.WriteFile(numVfsFilePath, bs, os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "SetSriovNumVfs(): fail to set NumVfs file", "path", numVfsFilePath)
		return err
	}
	return nil
}

func (s *sriov) ResetSriovDevice(ifaceStatus sriovnetworkv1.InterfaceExt) error {
	log.Log.V(2).Info("ResetSriovDevice(): reset SRIOV device", "address", ifaceStatus.PciAddress)
	if err := s.SetSriovNumVfs(ifaceStatus.PciAddress, 0); err != nil {
		return err
	}
	if ifaceStatus.LinkType == consts.LinkTypeETH {
		var mtu int
		is := sriovnetworkv1.InitialState.GetInterfaceStateByPciAddress(ifaceStatus.PciAddress)
		if is != nil {
			mtu = is.Mtu
		} else {
			mtu = 1500
		}
		log.Log.V(2).Info("ResetSriovDevice(): reset mtu", "value", mtu)
		if err := s.networkHelper.SetNetdevMTU(ifaceStatus.PciAddress, mtu); err != nil {
			return err
		}
	} else if ifaceStatus.LinkType == consts.LinkTypeIB {
		if err := s.networkHelper.SetNetdevMTU(ifaceStatus.PciAddress, 2048); err != nil {
			return err
		}
	}
	return nil
}

func (s *sriov) GetVfInfo(pciAddr string, devices []*ghw.PCIDevice) sriovnetworkv1.VirtualFunction {
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

	if mtu := s.networkHelper.GetNetdevMTU(pciAddr); mtu > 0 {
		vf.Mtu = mtu
	}
	if name := s.networkHelper.TryGetInterfaceName(pciAddr); name != "" {
		vf.Name = name
		vf.Mac = s.networkHelper.GetNetDevMac(name)
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

func (s *sriov) SetVfGUID(vfAddr string, pfLink netlink.Link) error {
	log.Log.Info("SetVfGUID()", "vf", vfAddr)
	vfID, err := dputils.GetVFID(vfAddr)
	if err != nil {
		log.Log.Error(err, "SetVfGUID(): unable to get VF id", "address", vfAddr)
		return err
	}
	guid := utils.GenerateRandomGUID()
	if err := s.netlinkLib.LinkSetVfNodeGUID(pfLink, vfID, guid); err != nil {
		return err
	}
	if err := s.netlinkLib.LinkSetVfPortGUID(pfLink, vfID, guid); err != nil {
		return err
	}
	if err = s.kernelHelper.Unbind(vfAddr); err != nil {
		return err
	}

	return nil
}

func (s *sriov) VFIsReady(pciAddr string) (netlink.Link, error) {
	log.Log.Info("VFIsReady()", "device", pciAddr)
	var err error
	var vfLink netlink.Link
	err = wait.PollImmediate(time.Second, 10*time.Second, func() (bool, error) {
		vfName := s.networkHelper.TryGetInterfaceName(pciAddr)
		vfLink, err = s.netlinkLib.LinkByName(vfName)
		if err != nil {
			log.Log.Error(err, "VFIsReady(): unable to get VF link", "device", pciAddr)
		}
		return err == nil, nil
	})
	if err != nil {
		return vfLink, err
	}
	return vfLink, nil
}

func (s *sriov) SetVfAdminMac(vfAddr string, pfLink, vfLink netlink.Link) error {
	log.Log.Info("SetVfAdminMac()", "vf", vfAddr)

	vfID, err := dputils.GetVFID(vfAddr)
	if err != nil {
		log.Log.Error(err, "SetVfAdminMac(): unable to get VF id", "address", vfAddr)
		return err
	}

	if err := s.netlinkLib.LinkSetVfHardwareAddr(pfLink, vfID, vfLink.Attrs().HardwareAddr); err != nil {
		return err
	}

	return nil
}

func (s *sriov) DiscoverSriovDevices(storeManager store.ManagerInterface) ([]sriovnetworkv1.InterfaceExt, error) {
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
		if devClass != consts.NetClass {
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

		if !vars.DevMode {
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
		if mtu := s.networkHelper.GetNetdevMTU(device.Address); mtu > 0 {
			iface.Mtu = mtu
		}
		if name := s.networkHelper.TryGetInterfaceName(device.Address); name != "" {
			iface.Name = name
			iface.Mac = s.networkHelper.GetNetDevMac(name)
			iface.LinkSpeed = s.networkHelper.GetNetDevLinkSpeed(name)
		}
		iface.LinkType = s.GetLinkType(iface)

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
			if iface.EswitchMode, err = s.GetNicSriovMode(device.Address); err != nil {
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
					instance := s.GetVfInfo(vf, devices)
					iface.VFs = append(iface.VFs, instance)
				}
			}
		}
		pfList = append(pfList, iface)
	}

	return pfList, nil
}

func (s *sriov) ConfigSriovDevice(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) error {
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
			err = s.udevHelper.AddUdevRule(iface.PciAddress)
			if err != nil {
				return err
			}

			err = s.SetSriovNumVfs(iface.PciAddress, iface.NumVfs)
			if err != nil {
				log.Log.Error(err, "configSriovDevice(): fail to set NumVfs for device", "device", iface.PciAddress)
				errRemove := s.udevHelper.RemoveUdevRule(iface.PciAddress)
				if errRemove != nil {
					log.Log.Error(errRemove, "configSriovDevice(): fail to remove udev rule", "device", iface.PciAddress)
				}
				return err
			}
		}
	}
	// set PF mtu
	if iface.Mtu > 0 && iface.Mtu > ifaceStatus.Mtu {
		if iface.ExternallyManaged {
			err := fmt.Errorf("ConfigSriovDevice(): requested MTU(%d) is greater than configured MTU(%d) for device %s. cannot change MTU as policy is configured as ExternallyManaged",
				iface.Mtu, ifaceStatus.Mtu, iface.PciAddress)
			log.Log.Error(nil, err.Error())
			return err
		}
		err = s.networkHelper.SetNetdevMTU(iface.PciAddress, iface.Mtu)
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
		pfLink, err := s.netlinkLib.LinkByName(iface.Name)
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
			if yes, d := s.kernelHelper.HasDriver(addr); yes && !sriovnetworkv1.StringInArray(d, vars.DpdkDrivers) {
				// LinkType is an optional field. Let's fallback to current link type
				// if nothing is specified in the SriovNodePolicy
				linkType := iface.LinkType
				if linkType == "" {
					linkType = ifaceStatus.LinkType
				}
				if strings.EqualFold(linkType, consts.LinkTypeIB) {
					if err = s.SetVfGUID(addr, pfLink); err != nil {
						return err
					}
				} else {
					vfLink, err := s.VFIsReady(addr)
					if err != nil {
						log.Log.Error(err, "configSriovDevice(): VF link is not ready", "address", addr)
						err = s.kernelHelper.RebindVfToDefaultDriver(addr)
						if err != nil {
							log.Log.Error(err, "configSriovDevice(): failed to rebind VF", "address", addr)
							return err
						}

						// Try to check the VF status again
						vfLink, err = s.VFIsReady(addr)
						if err != nil {
							log.Log.Error(err, "configSriovDevice(): VF link is not ready", "address", addr)
							return err
						}
					}
					if err = s.SetVfAdminMac(addr, pfLink, vfLink); err != nil {
						log.Log.Error(err, "configSriovDevice(): fail to configure VF admin mac", "device", addr)
						return err
					}
				}
			}

			if err = s.kernelHelper.UnbindDriverIfNeeded(addr, group.IsRdma); err != nil {
				return err
			}

			if !sriovnetworkv1.StringInArray(group.DeviceType, vars.DpdkDrivers) {
				if err := s.kernelHelper.BindDefaultDriver(addr); err != nil {
					log.Log.Error(err, "configSriovDevice(): fail to bind default driver for device", "device", addr)
					return err
				}
				// only set MTU for VF with default driver
				if group.Mtu > 0 {
					if err := s.networkHelper.SetNetdevMTU(addr, group.Mtu); err != nil {
						log.Log.Error(err, "configSriovDevice(): fail to set mtu for VF", "address", addr)
						return err
					}
				}
			} else {
				if err := s.kernelHelper.BindDpdkDriver(addr, group.DeviceType); err != nil {
					log.Log.Error(err, "configSriovDevice(): fail to bind driver for device",
						"driver", group.DeviceType, "device", addr)
					return err
				}
			}
		}
	}
	// Set PF link up
	pfLink, err := s.netlinkLib.LinkByName(ifaceStatus.Name)
	if err != nil {
		return err
	}
	if pfLink.Attrs().OperState != netlink.OperUp {
		err = s.netlinkLib.LinkSetUp(pfLink)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *sriov) ConfigSriovInterfaces(storeManager store.ManagerInterface,
	interfaces []sriovnetworkv1.Interface, ifaceStatuses []sriovnetworkv1.InterfaceExt, pfsToConfig map[string]bool) error {
	if s.kernelHelper.IsKernelLockdownMode() && mlx.HasMellanoxInterfacesInSpec(ifaceStatuses, interfaces) {
		log.Log.Error(nil, "cannot use mellanox devices when in kernel lockdown mode")
		return fmt.Errorf("cannot use mellanox devices when in kernel lockdown mode")
	}

	for _, ifaceStatus := range ifaceStatuses {
		configured := false
		for _, iface := range interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true

				if skip := pfsToConfig[iface.PciAddress]; skip {
					break
				}

				if !sriovnetworkv1.NeedToUpdateSriov(&iface, &ifaceStatus) {
					log.Log.V(2).Info("syncNodeState(): no need update interface", "address", iface.PciAddress)

					// Save the PF status to the host
					err := storeManager.SaveLastPfAppliedStatus(&iface)
					if err != nil {
						log.Log.Error(err, "SyncNodeState(): failed to save PF applied config to host")
						return err
					}

					break
				}
				if err := s.ConfigSriovDevice(&iface, &ifaceStatus); err != nil {
					log.Log.Error(err, "SyncNodeState(): fail to configure sriov interface. resetting interface.", "address", iface.PciAddress)
					if iface.ExternallyManaged {
						log.Log.Info("SyncNodeState(): skipping device reset as the nic is marked as externally created")
					} else {
						if resetErr := s.ResetSriovDevice(ifaceStatus); resetErr != nil {
							log.Log.Error(resetErr, "SyncNodeState(): failed to reset on error SR-IOV interface")
						}
					}
					return err
				}

				// Save the PF status to the host
				err := storeManager.SaveLastPfAppliedStatus(&iface)
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
				err = s.udevHelper.RemoveUdevRule(ifaceStatus.PciAddress)
				if err != nil {
					return err
				}
			}

			if err = s.ResetSriovDevice(ifaceStatus); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *sriov) ConfigSriovDeviceVirtual(iface *sriovnetworkv1.Interface) error {
	log.Log.V(2).Info("ConfigSriovDeviceVirtual(): config interface", "address", iface.PciAddress, "config", iface)
	// Config VFs
	if iface.NumVfs > 0 {
		if iface.NumVfs > 1 {
			log.Log.Error(nil, "ConfigSriovDeviceVirtual(): in a virtual environment, only one VF per interface",
				"numVfs", iface.NumVfs)
			return errors.New("NumVfs > 1")
		}
		if len(iface.VfGroups) != 1 {
			log.Log.Error(nil, "ConfigSriovDeviceVirtual(): missing VFGroup")
			return errors.New("NumVfs != 1")
		}
		addr := iface.PciAddress
		log.Log.V(2).Info("ConfigSriovDeviceVirtual()", "address", addr)
		driver := ""
		vfID := 0
		for _, group := range iface.VfGroups {
			log.Log.V(2).Info("ConfigSriovDeviceVirtual()", "group", group)
			if sriovnetworkv1.IndexInRange(vfID, group.VfRange) {
				log.Log.V(2).Info("ConfigSriovDeviceVirtual()", "indexInRange", vfID)
				if sriovnetworkv1.StringInArray(group.DeviceType, vars.DpdkDrivers) {
					log.Log.V(2).Info("ConfigSriovDeviceVirtual()", "driver", group.DeviceType)
					driver = group.DeviceType
				}
				break
			}
		}
		if driver == "" {
			log.Log.V(2).Info("ConfigSriovDeviceVirtual(): bind default")
			if err := s.kernelHelper.BindDefaultDriver(addr); err != nil {
				log.Log.Error(err, "ConfigSriovDeviceVirtual(): fail to bind default driver", "device", addr)
				return err
			}
		} else {
			log.Log.V(2).Info("ConfigSriovDeviceVirtual(): bind driver", "driver", driver)
			if err := s.kernelHelper.BindDpdkDriver(addr, driver); err != nil {
				log.Log.Error(err, "ConfigSriovDeviceVirtual(): fail to bind driver for device",
					"driver", driver, "device", addr)
				return err
			}
		}
	}
	return nil
}

func (s *sriov) GetNicSriovMode(pciAddress string) (string, error) {
	log.Log.V(2).Info("GetNicSriovMode()", "device", pciAddress)

	devLink, err := s.netlinkLib.DevLinkGetDeviceByName("pci", pciAddress)
	if err != nil {
		if errors.Is(err, syscall.ENODEV) {
			// the device doesn't support devlink
			return "", nil
		}
		return "", err
	}

	return devLink.Attrs.Eswitch.Mode, nil
}

func (s *sriov) GetLinkType(ifaceStatus sriovnetworkv1.InterfaceExt) string {
	log.Log.V(2).Info("GetLinkType()", "device", ifaceStatus.PciAddress)
	if ifaceStatus.Name != "" {
		link, err := s.netlinkLib.LinkByName(ifaceStatus.Name)
		if err != nil {
			log.Log.Error(err, "GetLinkType(): failed to get link", "device", ifaceStatus.Name)
			return ""
		}
		linkType := link.Attrs().EncapType
		if linkType == "ether" {
			return consts.LinkTypeETH
		} else if linkType == "infiniband" {
			return consts.LinkTypeIB
		}
	}

	return ""
}
