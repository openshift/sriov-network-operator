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

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	dputilsPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/dputils"
	ghwPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/ghw"
	netlinkPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	sriovnetPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/sriovnet"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/store"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type interfaceToConfigure struct {
	iface       sriovnetworkv1.Interface
	ifaceStatus sriovnetworkv1.InterfaceExt
}

type sriov struct {
	utilsHelper   utils.CmdInterface
	kernelHelper  types.KernelInterface
	networkHelper types.NetworkInterface
	udevHelper    types.UdevInterface
	vdpaHelper    types.VdpaInterface
	netlinkLib    netlinkPkg.NetlinkLib
	dputilsLib    dputilsPkg.DPUtilsLib
	sriovnetLib   sriovnetPkg.SriovnetLib
	ghwLib        ghwPkg.GHWLib
}

func New(utilsHelper utils.CmdInterface,
	kernelHelper types.KernelInterface,
	networkHelper types.NetworkInterface,
	udevHelper types.UdevInterface,
	vdpaHelper types.VdpaInterface,
	netlinkLib netlinkPkg.NetlinkLib,
	dputilsLib dputilsPkg.DPUtilsLib,
	sriovnetLib sriovnetPkg.SriovnetLib,
	ghwLib ghwPkg.GHWLib) types.SriovInterface {
	return &sriov{utilsHelper: utilsHelper,
		kernelHelper:  kernelHelper,
		networkHelper: networkHelper,
		udevHelper:    udevHelper,
		vdpaHelper:    vdpaHelper,
		netlinkLib:    netlinkLib,
		dputilsLib:    dputilsLib,
		sriovnetLib:   sriovnetLib,
		ghwLib:        ghwLib,
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
	if ifaceStatus.LinkType == consts.LinkTypeETH {
		var mtu int
		eswitchMode := sriovnetworkv1.ESwithModeLegacy
		is := sriovnetworkv1.InitialState.GetInterfaceStateByPciAddress(ifaceStatus.PciAddress)
		if is != nil {
			mtu = is.Mtu
			eswitchMode = sriovnetworkv1.GetEswitchModeFromStatus(is)
		} else {
			mtu = 1500
		}
		log.Log.V(2).Info("ResetSriovDevice(): reset mtu", "value", mtu)
		if err := s.networkHelper.SetNetdevMTU(ifaceStatus.PciAddress, mtu); err != nil {
			return err
		}
		log.Log.V(2).Info("ResetSriovDevice(): reset eswitch mode and number of VFs", "mode", eswitchMode)
		if err := s.setEswitchModeAndNumVFs(ifaceStatus.PciAddress, eswitchMode, 0); err != nil {
			return err
		}
	} else if ifaceStatus.LinkType == consts.LinkTypeIB {
		if err := s.SetSriovNumVfs(ifaceStatus.PciAddress, 0); err != nil {
			return err
		}
		if err := s.networkHelper.SetNetdevMTU(ifaceStatus.PciAddress, 2048); err != nil {
			return err
		}
	}
	return nil
}

func (s *sriov) getVfInfo(vfAddr string, pfName string, eswitchMode string, devices []*ghw.PCIDevice) sriovnetworkv1.VirtualFunction {
	driver, err := s.dputilsLib.GetDriverName(vfAddr)
	if err != nil {
		log.Log.Error(err, "getVfInfo(): unable to parse device driver", "device", vfAddr)
	}
	id, err := s.dputilsLib.GetVFID(vfAddr)
	if err != nil {
		log.Log.Error(err, "getVfInfo(): unable to get VF index", "device", vfAddr)
	}
	vf := sriovnetworkv1.VirtualFunction{
		PciAddress: vfAddr,
		Driver:     driver,
		VfID:       id,
		VdpaType:   s.vdpaHelper.DiscoverVDPAType(vfAddr),
	}

	if eswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
		repName, err := s.sriovnetLib.GetVfRepresentor(pfName, id)
		if err != nil {
			log.Log.Error(err, "getVfInfo(): failed to get VF representor name", "device", vfAddr)
		} else {
			vf.RepresentorName = repName
		}
	}

	if name := s.networkHelper.TryGetInterfaceName(vfAddr); name != "" {
		link, err := s.netlinkLib.LinkByName(name)
		if err != nil {
			log.Log.Error(err, "getVfInfo(): unable to get VF Link Object", "name", name, "device", vfAddr)
		} else {
			vf.Name = name
			vf.Mtu = link.Attrs().MTU
			vf.Mac = link.Attrs().HardwareAddr.String()
		}
	}
	vf.GUID = s.networkHelper.GetNetDevNodeGUID(vfAddr)

	for _, device := range devices {
		if vfAddr == device.Address {
			vf.Vendor = device.Vendor.ID
			vf.DeviceID = device.Product.ID
			break
		}
	}
	return vf
}

func (s *sriov) SetVfGUID(vfAddr string, pfLink netlink.Link) error {
	log.Log.Info("SetVfGUID()", "vf", vfAddr)
	vfID, err := s.dputilsLib.GetVFID(vfAddr)
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
		vfIndex, err := s.networkHelper.GetInterfaceIndex(pciAddr)
		if err != nil {
			log.Log.Error(err, "VFIsReady(): invalid index number")
			return false, nil
		}
		vfLink, err = s.netlinkLib.LinkByIndex(vfIndex)
		if err != nil {
			log.Log.Error(err, "VFIsReady(): unable to get VF link", "device", pciAddr)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return vfLink, err
	}
	return vfLink, nil
}

func (s *sriov) SetVfAdminMac(vfAddr string, pfLink, vfLink netlink.Link) error {
	log.Log.Info("SetVfAdminMac()", "vf", vfAddr)

	vfID, err := s.dputilsLib.GetVFID(vfAddr)
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

	pci, err := s.ghwLib.PCI()
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

		if s.dputilsLib.IsSriovVF(device.Address) {
			continue
		}

		if !vars.DevMode {
			if !sriovnetworkv1.IsSupportedModel(device.Vendor.ID, device.Product.ID) {
				log.Log.Info("DiscoverSriovDevices(): unsupported device", "device", device)
				continue
			}
		}

		driver, err := s.dputilsLib.GetDriverName(device.Address)
		if err != nil {
			log.Log.Error(err, "DiscoverSriovDevices(): unable to parse device driver for device, skipping", "device", device)
			continue
		}

		pfNetName := s.networkHelper.TryGetInterfaceName(device.Address)

		if pfNetName == "" {
			log.Log.Error(err, "DiscoverSriovDevices(): unable to get device name for device, skipping", "device", device.Address)
			continue
		}

		link, err := s.netlinkLib.LinkByName(pfNetName)
		if err != nil {
			log.Log.Error(err, "DiscoverSriovDevices(): unable to get Link for device, skipping", "device", device.Address)
			continue
		}

		iface := sriovnetworkv1.InterfaceExt{
			Name:           pfNetName,
			PciAddress:     device.Address,
			Driver:         driver,
			Vendor:         device.Vendor.ID,
			DeviceID:       device.Product.ID,
			Mtu:            link.Attrs().MTU,
			Mac:            link.Attrs().HardwareAddr.String(),
			LinkType:       s.encapTypeToLinkType(link.Attrs().EncapType),
			LinkSpeed:      s.networkHelper.GetNetDevLinkSpeed(pfNetName),
			LinkAdminState: s.networkHelper.GetNetDevLinkAdminState(pfNetName),
		}

		pfStatus, exist, err := storeManager.LoadPfsStatus(iface.PciAddress)
		if err != nil {
			log.Log.Error(err, "DiscoverSriovDevices(): failed to load PF status from disk")
		} else {
			if exist {
				iface.ExternallyManaged = pfStatus.ExternallyManaged
			}
		}

		if s.dputilsLib.IsSriovPF(device.Address) {
			iface.TotalVfs = s.dputilsLib.GetSriovVFcapacity(device.Address)
			iface.NumVfs = s.dputilsLib.GetVFconfigured(device.Address)
			iface.EswitchMode = s.GetNicSriovMode(device.Address)
			if s.dputilsLib.SriovConfigured(device.Address) {
				vfs, err := s.dputilsLib.GetVFList(device.Address)
				if err != nil {
					log.Log.Error(err, "DiscoverSriovDevices(): unable to parse VFs for device, skipping",
						"device", device)
					continue
				}
				for _, vf := range vfs {
					instance := s.getVfInfo(vf, pfNetName, iface.EswitchMode, devices)
					iface.VFs = append(iface.VFs, instance)
				}
			}
		}
		pfList = append(pfList, iface)
	}

	return pfList, nil
}

func (s *sriov) configSriovPFDevice(iface *sriovnetworkv1.Interface) error {
	log.Log.V(2).Info("configSriovPFDevice(): configure PF sriov device",
		"device", iface.PciAddress)
	totalVfs := s.dputilsLib.GetSriovVFcapacity(iface.PciAddress)
	if iface.NumVfs > totalVfs {
		err := fmt.Errorf("cannot config SRIOV device: NumVfs (%d) is larger than TotalVfs (%d)", iface.NumVfs, totalVfs)
		log.Log.Error(err, "configSriovPFDevice(): fail to set NumVfs for device", "device", iface.PciAddress)
		return err
	}
	if err := s.configureHWOptionsForSwitchdev(iface); err != nil {
		return err
	}
	// remove all UDEV rules for the PF before adding new rules to
	// make sure that rules are always in a consistent state, e.g. there is no
	// switchdev-related rules for PF in legacy mode
	if err := s.removeUdevRules(iface.PciAddress); err != nil {
		log.Log.Error(err, "configSriovPFDevice(): fail to remove udev rules", "device", iface.PciAddress)
		return err
	}
	err := s.addUdevRules(iface)
	if err != nil {
		log.Log.Error(err, "configSriovPFDevice(): fail to add udev rules", "device", iface.PciAddress)
		return err
	}
	err = s.createVFs(iface)
	if err != nil {
		log.Log.Error(err, "configSriovPFDevice(): fail to set NumVfs for device", "device", iface.PciAddress)
		return err
	}
	if err := s.addVfRepresentorUdevRule(iface); err != nil {
		log.Log.Error(err, "configSriovPFDevice(): fail to add VR representor udev rule", "device", iface.PciAddress)
		return err
	}
	// set PF mtu
	if iface.Mtu > 0 && iface.Mtu > s.networkHelper.GetNetdevMTU(iface.PciAddress) {
		err = s.networkHelper.SetNetdevMTU(iface.PciAddress, iface.Mtu)
		if err != nil {
			log.Log.Error(err, "configSriovPFDevice(): fail to set mtu for PF", "device", iface.PciAddress)
			return err
		}
	}
	return nil
}

func (s *sriov) configureHWOptionsForSwitchdev(iface *sriovnetworkv1.Interface) error {
	log.Log.V(2).Info("configureHWOptionsForSwitchdev(): configure HW options for device",
		"device", iface.PciAddress)
	if sriovnetworkv1.GetEswitchModeFromSpec(iface) != sriovnetworkv1.ESwithModeSwitchDev {
		// we need to configure HW options only for PFs for which switchdev is a target mode
		return nil
	}
	if err := s.networkHelper.EnableHwTcOffload(iface.Name); err != nil {
		return err
	}
	desiredFlowSteeringMode := "smfs"
	currentFlowSteeringMode, err := s.networkHelper.GetDevlinkDeviceParam(iface.PciAddress, "flow_steering_mode")
	if err != nil {
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENODEV) {
			log.Log.V(2).Info("configureHWOptionsForSwitchdev(): device has no flow_steering_mode parameter, skip",
				"device", iface.PciAddress)
			return nil
		}
		log.Log.Error(err, "configureHWOptionsForSwitchdev(): fail to read current flow steering mode for the device", "device", iface.PciAddress)
		return err
	}
	if currentFlowSteeringMode == desiredFlowSteeringMode {
		return nil
	}
	// flow steering mode can be changed only when NIC is in legacy mode
	if s.GetNicSriovMode(iface.PciAddress) != sriovnetworkv1.ESwithModeLegacy {
		s.setEswitchModeAndNumVFs(iface.PciAddress, sriovnetworkv1.ESwithModeLegacy, 0)
	}
	if err := s.networkHelper.SetDevlinkDeviceParam(iface.PciAddress, "flow_steering_mode", desiredFlowSteeringMode); err != nil {
		if errors.Is(err, syscall.ENOTSUP) {
			log.Log.V(2).Info("configureHWOptionsForSwitchdev(): device doesn't support changing of flow_steering_mode, skip", "device", iface.PciAddress)
			return nil
		}
		log.Log.Error(err, "configureHWOptionsForSwitchdev(): fail to configure flow steering mode for the device", "device", iface.PciAddress)
		return err
	}
	return nil
}

func (s *sriov) checkExternallyManagedPF(iface *sriovnetworkv1.Interface) error {
	log.Log.V(2).Info("checkExternallyManagedPF(): configure PF sriov device",
		"device", iface.PciAddress)
	currentNumVfs := s.dputilsLib.GetVFconfigured(iface.PciAddress)
	if iface.NumVfs > currentNumVfs {
		errMsg := fmt.Sprintf("checkExternallyManagedPF(): number of request virtual functions %d is not equal to configured virtual "+
			"functions %d but the policy is configured as ExternallyManaged for device %s",
			iface.NumVfs, currentNumVfs, iface.PciAddress)
		log.Log.Error(nil, errMsg)
		return fmt.Errorf(errMsg)
	}
	currentEswitchMode := s.GetNicSriovMode(iface.PciAddress)
	expectedEswitchMode := sriovnetworkv1.GetEswitchModeFromSpec(iface)
	if currentEswitchMode != expectedEswitchMode {
		errMsg := fmt.Sprintf("checkExternallyManagedPF(): requested ESwitchMode mode \"%s\" is not equal to configured \"%s\" "+
			"but the policy is configured as ExternallyManaged for device %s", expectedEswitchMode, currentEswitchMode, iface.PciAddress)
		log.Log.Error(nil, errMsg)
		return fmt.Errorf(errMsg)
	}
	currentMtu := s.networkHelper.GetNetdevMTU(iface.PciAddress)
	if iface.Mtu > 0 && iface.Mtu > currentMtu {
		err := fmt.Errorf("checkExternallyManagedPF(): requested MTU(%d) is greater than configured MTU(%d) for device %s. cannot change MTU as policy is configured as ExternallyManaged",
			iface.Mtu, currentMtu, iface.PciAddress)
		log.Log.Error(nil, err.Error())
		return err
	}
	return nil
}

func (s *sriov) configSriovVFDevices(iface *sriovnetworkv1.Interface) error {
	log.Log.V(2).Info("configSriovVFDevices(): configure PF sriov device",
		"device", iface.PciAddress)
	if iface.NumVfs > 0 {
		vfAddrs, err := s.dputilsLib.GetVFList(iface.PciAddress)
		if err != nil {
			log.Log.Error(err, "configSriovVFDevices(): unable to parse VFs for device", "device", iface.PciAddress)
		}
		pfLink, err := s.netlinkLib.LinkByName(iface.Name)
		if err != nil {
			log.Log.Error(err, "configSriovVFDevices(): unable to get PF link for device", "device", iface)
			return err
		}

		for _, addr := range vfAddrs {
			hasDriver, _ := s.kernelHelper.HasDriver(addr)
			if !hasDriver {
				if err := s.kernelHelper.BindDefaultDriver(addr); err != nil {
					log.Log.Error(err, "configSriovVFDevices(): fail to bind default driver for device", "device", addr)
					return err
				}
			}
			var group *sriovnetworkv1.VfGroup

			vfID, err := s.dputilsLib.GetVFID(addr)
			if err != nil {
				log.Log.Error(err, "configSriovVFDevices(): unable to get VF id", "device", iface.PciAddress)
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
					linkType = s.GetLinkType(iface.Name)
				}
				if strings.EqualFold(linkType, consts.LinkTypeIB) {
					if err = s.SetVfGUID(addr, pfLink); err != nil {
						return err
					}
				} else {
					vfLink, err := s.VFIsReady(addr)
					if err != nil {
						log.Log.Error(err, "configSriovVFDevices(): VF link is not ready", "address", addr)
						err = s.kernelHelper.RebindVfToDefaultDriver(addr)
						if err != nil {
							log.Log.Error(err, "configSriovVFDevices(): failed to rebind VF", "address", addr)
							return err
						}

						// Try to check the VF status again
						vfLink, err = s.VFIsReady(addr)
						if err != nil {
							log.Log.Error(err, "configSriovVFDevices(): VF link is not ready", "address", addr)
							return err
						}
					}
					if err = s.SetVfAdminMac(addr, pfLink, vfLink); err != nil {
						log.Log.Error(err, "configSriovVFDevices(): fail to configure VF admin mac", "device", addr)
						return err
					}
				}
			}

			if err = s.kernelHelper.UnbindDriverIfNeeded(addr, group.IsRdma); err != nil {
				return err
			}
			// we set eswitch mode before this point and if the desired mode (and current at this point)
			// is legacy, then VDPA device is already automatically disappeared,
			// so we don't need to check it
			if sriovnetworkv1.GetEswitchModeFromSpec(iface) == sriovnetworkv1.ESwithModeSwitchDev && group.VdpaType == "" {
				if err := s.vdpaHelper.DeleteVDPADevice(addr); err != nil {
					log.Log.Error(err, "configSriovVFDevices(): fail to delete VDPA device",
						"device", addr)
					return err
				}
			}
			if !sriovnetworkv1.StringInArray(group.DeviceType, vars.DpdkDrivers) {
				if err := s.kernelHelper.BindDefaultDriver(addr); err != nil {
					log.Log.Error(err, "configSriovVFDevices(): fail to bind default driver for device", "device", addr)
					return err
				}
				// only set MTU for VF with default driver
				if group.Mtu > 0 {
					if err := s.networkHelper.SetNetdevMTU(addr, group.Mtu); err != nil {
						log.Log.Error(err, "configSriovVFDevices(): fail to set mtu for VF", "address", addr)
						return err
					}
				}
				if sriovnetworkv1.GetEswitchModeFromSpec(iface) == sriovnetworkv1.ESwithModeSwitchDev && group.VdpaType != "" {
					if err := s.vdpaHelper.CreateVDPADevice(addr, group.VdpaType); err != nil {
						log.Log.Error(err, "configSriovVFDevices(): fail to create VDPA device",
							"vdpaType", group.VdpaType, "device", addr)
						return err
					}
				}
			} else {
				if err := s.kernelHelper.BindDpdkDriver(addr, group.DeviceType); err != nil {
					log.Log.Error(err, "configSriovVFDevices(): fail to bind driver for device",
						"driver", group.DeviceType, "device", addr)
					return err
				}
			}
		}
	}
	return nil
}

func (s *sriov) configSriovDevice(iface *sriovnetworkv1.Interface, skipVFConfiguration bool) error {
	log.Log.V(2).Info("configSriovDevice(): configure sriov device",
		"device", iface.PciAddress, "config", iface, "skipVFConfiguration", skipVFConfiguration)
	if !iface.ExternallyManaged {
		if err := s.configSriovPFDevice(iface); err != nil {
			return err
		}
	}
	if skipVFConfiguration {
		if iface.ExternallyManaged {
			return nil
		}
		log.Log.V(2).Info("configSriovDevice(): skipVFConfiguration is true, unbind all VFs from drivers",
			"device", iface.PciAddress)
		return s.unbindAllVFsOnPF(iface.PciAddress)
	}
	// we don't need to validate externally managed PFs when skipVFConfiguration is true.
	// The function usually called with skipVFConfiguration true when running in the systemd mode and configuration is
	// in pre phase. Externally managed PFs may not be configured at this stage yet (preConfig stage is executed before NetworkManager, netplan)

	if iface.ExternallyManaged {
		if err := s.checkExternallyManagedPF(iface); err != nil {
			return err
		}
	}
	if err := s.configSriovVFDevices(iface); err != nil {
		return err
	}
	// Set PF link up
	pfLink, err := s.netlinkLib.LinkByName(iface.Name)
	if err != nil {
		return err
	}
	if !s.netlinkLib.IsLinkAdminStateUp(pfLink) {
		err = s.netlinkLib.LinkSetUp(pfLink)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *sriov) ConfigSriovInterfaces(storeManager store.ManagerInterface,
	interfaces []sriovnetworkv1.Interface, ifaceStatuses []sriovnetworkv1.InterfaceExt, skipVFConfiguration bool) error {
	toBeConfigured, toBeResetted, err := s.getConfigureAndReset(storeManager, interfaces, ifaceStatuses)
	if err != nil {
		log.Log.Error(err, "cannot get a list of interfaces to configure")
		return fmt.Errorf("cannot get a list of interfaces to configure")
	}

	if vars.ParallelNicConfig {
		err = s.configSriovInterfacesInParallel(storeManager, toBeConfigured, skipVFConfiguration)
	} else {
		err = s.configSriovInterfaces(storeManager, toBeConfigured, skipVFConfiguration)
	}
	if err != nil {
		log.Log.Error(err, "cannot configure sriov interfaces")
		return fmt.Errorf("cannot configure sriov interfaces")
	}
	if sriovnetworkv1.ContainsSwitchdevInterface(interfaces) && len(toBeConfigured) > 0 {
		// for switchdev devices we create udev rule that renames VF representors
		// after VFs are created. Reload rules to update interfaces
		if err := s.udevHelper.LoadUdevRules(); err != nil {
			log.Log.Error(err, "cannot reload udev rules")
			return fmt.Errorf("failed to reload udev rules: %v", err)
		}
	}

	if vars.ParallelNicConfig {
		err = s.resetSriovInterfacesInParallel(storeManager, toBeResetted)
	} else {
		err = s.resetSriovInterfaces(storeManager, toBeResetted)
	}
	if err != nil {
		log.Log.Error(err, "cannot reset sriov interfaces")
		return fmt.Errorf("cannot reset sriov interfaces")
	}
	return nil
}

func (s *sriov) getConfigureAndReset(storeManager store.ManagerInterface, interfaces []sriovnetworkv1.Interface,
	ifaceStatuses []sriovnetworkv1.InterfaceExt) ([]interfaceToConfigure, []sriovnetworkv1.InterfaceExt, error) {
	toBeConfigured := []interfaceToConfigure{}
	toBeResetted := []sriovnetworkv1.InterfaceExt{}
	for _, ifaceStatus := range ifaceStatuses {
		configured := false
		for _, iface := range interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				configured = true
				skip, err := skipSriovConfig(&iface, &ifaceStatus, storeManager)
				if err != nil {
					log.Log.Error(err, "getConfigureAndReset(): failed to check interface")
					return nil, nil, err
				}
				if skip {
					break
				}
				iface := iface
				ifaceStatus := ifaceStatus
				toBeConfigured = append(toBeConfigured, interfaceToConfigure{iface: iface, ifaceStatus: ifaceStatus})
			}
		}

		if !configured && ifaceStatus.NumVfs > 0 {
			toBeResetted = append(toBeResetted, ifaceStatus)
		}
	}
	return toBeConfigured, toBeResetted, nil
}

func (s *sriov) configSriovInterfacesInParallel(storeManager store.ManagerInterface, interfaces []interfaceToConfigure, skipVFConfiguration bool) error {
	log.Log.V(2).Info("configSriovInterfacesInParallel(): start sriov configuration")

	var result error
	errChannel := make(chan error)
	interfacesToConfigure := 0
	for ifaceIndex, iface := range interfaces {
		interfacesToConfigure += 1
		go func(iface *interfaceToConfigure) {
			var err error
			if err = s.configSriovDevice(&iface.iface, skipVFConfiguration); err != nil {
				log.Log.Error(err, "configSriovInterfacesInParallel(): fail to configure sriov interface. resetting interface.", "address", iface.iface.PciAddress)
				if iface.iface.ExternallyManaged {
					log.Log.V(2).Info("configSriovInterfacesInParallel(): skipping device reset as the nic is marked as externally created")
				} else {
					if resetErr := s.ResetSriovDevice(iface.ifaceStatus); resetErr != nil {
						log.Log.Error(resetErr, "configSriovInterfacesInParallel(): failed to reset on error SR-IOV interface")
						err = resetErr
					}
				}
			}
			errChannel <- err
		}(&interfaces[ifaceIndex])
		// Save the PF status to the host
		err := storeManager.SaveLastPfAppliedStatus(&iface.iface)
		if err != nil {
			log.Log.Error(err, "configSriovInterfacesInParallel(): failed to save PF applied config to host")
			return err
		}
	}

	for i := 0; i < interfacesToConfigure; i++ {
		errMsg := <-errChannel
		result = errors.Join(result, errMsg)
	}
	if result != nil {
		log.Log.Error(result, "configSriovInterfacesInParallel(): fail to configure sriov interfaces")
		return result
	}
	log.Log.V(2).Info("configSriovInterfacesInParallel(): sriov configuration finished")
	return nil
}

func (s *sriov) resetSriovInterfacesInParallel(storeManager store.ManagerInterface, interfaces []sriovnetworkv1.InterfaceExt) error {
	var result error
	errChannel := make(chan error, len(interfaces))
	interfacesToReset := 0
	for ifaceIndex := range interfaces {
		interfacesToReset += 1
		go func(iface *sriovnetworkv1.InterfaceExt) {
			var err error
			if err = s.checkForConfigAndReset(*iface, storeManager); err != nil {
				log.Log.Error(err, "resetSriovInterfacesInParallel(): fail to reset sriov interface. resetting interface.", "address", iface.PciAddress)
			}
			errChannel <- err
		}(&interfaces[ifaceIndex])
	}

	for i := 0; i < interfacesToReset; i++ {
		errMsg := <-errChannel
		result = errors.Join(result, errMsg)
	}
	if result != nil {
		log.Log.Error(result, "resetSriovInterfacesInParallel(): fail to reset sriov interface")
		return result
	}
	log.Log.V(2).Info("resetSriovInterfacesInParallel(): sriov reset finished")

	return nil
}

func (s *sriov) configSriovInterfaces(storeManager store.ManagerInterface, interfaces []interfaceToConfigure, skipVFConfiguration bool) error {
	log.Log.V(2).Info("configSriovInterfaces(): start sriov configuration")
	for _, iface := range interfaces {
		if err := s.configSriovDevice(&iface.iface, skipVFConfiguration); err != nil {
			log.Log.Error(err, "configSriovInterfaces(): fail to configure sriov interface. resetting interface.", "address", iface.iface.PciAddress)
			if iface.iface.ExternallyManaged {
				log.Log.V(2).Info("configSriovInterfaces(): skipping device reset as the nic is marked as externally created")
			} else {
				if resetErr := s.ResetSriovDevice(iface.ifaceStatus); resetErr != nil {
					log.Log.Error(resetErr, "configSriovInterfaces(): failed to reset on error SR-IOV interface")
				}
			}
			return err
		}

		// Save the PF status to the host
		err := storeManager.SaveLastPfAppliedStatus(&iface.iface)
		if err != nil {
			log.Log.Error(err, "configSriovInterfaces(): failed to save PF applied config to host")
			return err
		}
	}
	log.Log.V(2).Info("configSriovInterfaces(): sriov configuration finished")
	return nil
}

func (s *sriov) resetSriovInterfaces(storeManager store.ManagerInterface, interfaces []sriovnetworkv1.InterfaceExt) error {
	for _, iface := range interfaces {
		if err := s.checkForConfigAndReset(iface, storeManager); err != nil {
			log.Log.Error(err, "resetSriovInterfaces(): failed to reset sriov interface. resetting interface.", "address", iface.PciAddress)
			return err
		}
	}
	log.Log.V(2).Info("resetSriovInterfaces(): sriov reset finished")
	return nil
}

// / skipSriovConfig checks if we need to apply SR-IOV configuration specified specific interface
func skipSriovConfig(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt, storeManager store.ManagerInterface) (bool, error) {
	if !sriovnetworkv1.NeedToUpdateSriov(iface, ifaceStatus) {
		log.Log.V(2).Info("ConfigSriovInterfaces(): no need update interface", "address", iface.PciAddress)

		// Save the PF status to the host
		err := storeManager.SaveLastPfAppliedStatus(iface)
		if err != nil {
			log.Log.Error(err, "ConfigSriovInterfaces(): failed to save PF applied status config to host")
			return false, err
		}

		return true, nil
	}
	return false, nil
}

func (s *sriov) checkForConfigAndReset(ifaceStatus sriovnetworkv1.InterfaceExt, storeManager store.ManagerInterface) error {
	// load the PF info
	pfStatus, exist, err := storeManager.LoadPfsStatus(ifaceStatus.PciAddress)
	if err != nil {
		log.Log.Error(err, "checkForConfigAndReset(): failed to load info about PF status for device",
			"address", ifaceStatus.PciAddress)
		return err
	}

	if !exist {
		log.Log.V(2).Info("checkForConfigAndReset(): PF name with pci address has VFs configured but they weren't created by the sriov operator. Skipping the device reset",
			"pf-name", ifaceStatus.Name,
			"address", ifaceStatus.PciAddress)
		return nil
	}

	if pfStatus.ExternallyManaged {
		log.Log.V(2).Info("checkForConfigAndReset(): PF name with pci address was externally created skipping the device reset",
			"pf-name", ifaceStatus.Name,
			"address", ifaceStatus.PciAddress)

		// remove pf status from host
		err = storeManager.RemovePfAppliedStatus(ifaceStatus.PciAddress)
		if err != nil {
			return err
		}

		return nil
	}
	err = s.removeUdevRules(ifaceStatus.PciAddress)
	if err != nil {
		return err
	}

	if err = s.ResetSriovDevice(ifaceStatus); err != nil {
		return err
	}

	// remove pf status from host
	err = storeManager.RemovePfAppliedStatus(ifaceStatus.PciAddress)
	if err != nil {
		return err
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

func (s *sriov) GetNicSriovMode(pciAddress string) string {
	log.Log.V(2).Info("GetNicSriovMode()", "device", pciAddress)
	devLink, err := s.netlinkLib.DevLinkGetDeviceByName("pci", pciAddress)
	if err != nil {
		if !errors.Is(err, syscall.ENODEV) {
			log.Log.Error(err, "GetNicSriovMode(): failed to get eswitch mode, assume legacy", "device", pciAddress)
		}
	}
	if devLink != nil && devLink.Attrs.Eswitch.Mode != "" {
		return devLink.Attrs.Eswitch.Mode
	}

	return sriovnetworkv1.ESwithModeLegacy
}

func (s *sriov) SetNicSriovMode(pciAddress string, mode string) error {
	log.Log.V(2).Info("SetNicSriovMode()", "device", pciAddress, "mode", mode)

	dev, err := s.netlinkLib.DevLinkGetDeviceByName("pci", pciAddress)
	if err != nil {
		return err
	}
	return s.netlinkLib.DevLinkSetEswitchMode(dev, mode)
}

func (s *sriov) GetLinkType(name string) string {
	log.Log.V(2).Info("GetLinkType()", "name", name)
	link, err := s.netlinkLib.LinkByName(name)
	if err != nil {
		log.Log.Error(err, "GetLinkType(): failed to get link", "device", name)
		return ""
	}
	return s.encapTypeToLinkType(link.Attrs().EncapType)
}

func (s *sriov) encapTypeToLinkType(encapType string) string {
	if encapType == "ether" {
		return consts.LinkTypeETH
	} else if encapType == "infiniband" {
		return consts.LinkTypeIB
	}
	return ""
}

// create required udev rules for PF:
// * rule to disable NetworkManager for VFs - for all modes
// * rule to keep PF name after switching to switchdev mode - only for switchdev mode
func (s *sriov) addUdevRules(iface *sriovnetworkv1.Interface) error {
	log.Log.V(2).Info("addUdevRules(): add udev rules for device",
		"device", iface.PciAddress)
	if err := s.udevHelper.AddDisableNMUdevRule(iface.PciAddress); err != nil {
		return err
	}
	if sriovnetworkv1.GetEswitchModeFromSpec(iface) == sriovnetworkv1.ESwithModeSwitchDev {
		if err := s.udevHelper.AddPersistPFNameUdevRule(iface.PciAddress, iface.Name); err != nil {
			return err
		}
	}
	return nil
}

// add switchdev-specific udev rule that renames representors.
// this rule relies on phys_port_name and phys_switch_id parameter which
// on old kernels can be read only after switching PF to switchdev mode.
// if PF doesn't expose phys_port_name and phys_switch_id, then rule creation will be skipped
func (s *sriov) addVfRepresentorUdevRule(iface *sriovnetworkv1.Interface) error {
	if sriovnetworkv1.GetEswitchModeFromSpec(iface) == sriovnetworkv1.ESwithModeSwitchDev {
		portName, err := s.networkHelper.GetPhysPortName(iface.Name)
		if err != nil {
			log.Log.Error(err, "addVfRepresentorUdevRule(): WARNING: can't read phys_port_name for device, skip creation of UDEV rule")
			return nil
		}
		switchID, err := s.networkHelper.GetPhysSwitchID(iface.Name)
		if err != nil {
			log.Log.Error(err, "addVfRepresentorUdevRule(): WARNING: can't read phys_switch_id for device, skip creation of UDEV rule")
			return nil
		}
		return s.udevHelper.AddVfRepresentorUdevRule(iface.PciAddress, iface.Name, switchID, portName)
	}
	return nil
}

// remove all udev rules for PF created by the operator
func (s *sriov) removeUdevRules(pciAddress string) error {
	log.Log.V(2).Info("removeUdevRules(): remove udev rules for device",
		"device", pciAddress)
	if err := s.udevHelper.RemoveDisableNMUdevRule(pciAddress); err != nil {
		return err
	}
	if err := s.udevHelper.RemoveVfRepresentorUdevRule(pciAddress); err != nil {
		return err
	}
	return s.udevHelper.RemovePersistPFNameUdevRule(pciAddress)
}

// create VFs on the PF
func (s *sriov) createVFs(iface *sriovnetworkv1.Interface) error {
	expectedEswitchMode := sriovnetworkv1.GetEswitchModeFromSpec(iface)
	log.Log.V(2).Info("createVFs(): configure VFs for device",
		"device", iface.PciAddress, "count", iface.NumVfs, "mode", expectedEswitchMode)

	if s.dputilsLib.GetVFconfigured(iface.PciAddress) == iface.NumVfs {
		if s.GetNicSriovMode(iface.PciAddress) == expectedEswitchMode {
			log.Log.V(2).Info("createVFs(): device is already configured",
				"device", iface.PciAddress, "count", iface.NumVfs, "mode", expectedEswitchMode)
			return nil
		}
	}
	return s.setEswitchModeAndNumVFs(iface.PciAddress, expectedEswitchMode, iface.NumVfs)
}

func (s *sriov) setEswitchMode(pciAddr, eswitchMode string) error {
	log.Log.V(2).Info("setEswitchMode(): set eswitch mode", "device", pciAddr, "mode", eswitchMode)
	if err := s.unbindAllVFsOnPF(pciAddr); err != nil {
		log.Log.Error(err, "setEswitchMode(): failed to unbind VFs", "device", pciAddr, "mode", eswitchMode)
		return err
	}
	if err := s.SetNicSriovMode(pciAddr, eswitchMode); err != nil {
		err = fmt.Errorf("failed to switch NIC to SRIOV %s mode: %v", eswitchMode, err)
		log.Log.Error(err, "setEswitchMode(): failed to set mode", "device", pciAddr, "mode", eswitchMode)
		return err
	}
	return nil
}

func (s *sriov) setEswitchModeAndNumVFs(pciAddr string, desiredEswitchMode string, numVFs int) error {
	log.Log.V(2).Info("setEswitchModeAndNumVFs(): configure VFs for device",
		"device", pciAddr, "count", numVFs, "mode", desiredEswitchMode)

	// always switch NIC to the legacy mode before creating VFs. This is required because some drivers
	// may not support VF creation in the switchdev mode
	if s.GetNicSriovMode(pciAddr) != sriovnetworkv1.ESwithModeLegacy {
		if err := s.setEswitchMode(pciAddr, sriovnetworkv1.ESwithModeLegacy); err != nil {
			return err
		}
	}
	if err := s.SetSriovNumVfs(pciAddr, numVFs); err != nil {
		return err
	}

	if desiredEswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
		return s.setEswitchMode(pciAddr, sriovnetworkv1.ESwithModeSwitchDev)
	}
	return nil
}

// retrieve all VFs for the PF and unbind them from a driver
func (s *sriov) unbindAllVFsOnPF(addr string) error {
	log.Log.V(2).Info("unbindAllVFsOnPF(): unbind all VFs on PF", "device", addr)
	vfAddrs, err := s.dputilsLib.GetVFList(addr)
	if err != nil {
		return fmt.Errorf("failed to read VF list: %v", err)
	}
	for _, vfAddr := range vfAddrs {
		if err := s.kernelHelper.Unbind(vfAddr); err != nil {
			return fmt.Errorf("failed to unbind VF from the driver: %v", err)
		}
	}
	return nil
}
