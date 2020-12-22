package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"strconv"

	"github.com/golang/glog"
	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"
	"github.com/jaypipes/ghw"
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

// PlatformType ...
type PlatformType int

const (
	// Baremetal platform
	Baremetal PlatformType = iota
	// VirtualOpenStack ...
	VirtualOpenStack
)

func (e PlatformType) String() string {
	switch e {
	case Baremetal:
		return "Baremetal"
	case VirtualOpenStack:
		return "Virtual/Openstack"
	default:
		return fmt.Sprintf("%d", int(e))
	}
}

const (
	ospMetaDataDir = "/host/var/config/openstack/latest/"
	ospNetworkData = ospMetaDataDir + "/network_data.json"
	ospMetaData    = ospMetaDataDir + "/meta_data.json"
)

// OSPMetaDataDevice -- Device structure within meta_data.json
type OSPMetaDataDevice struct {
	Vlan      int      `json:"vlan,omitempty"`
	VfTrusted bool     `json:"vf_trusted,omitempty"`
	Type      string   `json:"type,omitempty"`
	Mac       string   `json:"mac,omitempty"`
	Bus       string   `json:"bus,omitempty"`
	Address   string   `json:"address,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// OSPMetaData -- Openstack meta_data.json format
type OSPMetaData struct {
	UUID             string              `json:"uuid,omitempty"`
	AdminPass        string              `json:"admin_pass,omitempty"`
	Name             string              `json:"name,omitempty"`
	LaunchIndex      int                 `json:"launch_index,omitempty"`
	AvailabilityZone string              `json:"availability_zone,omitempty"`
	ProjectID        string              `json:"project_id,omitempty"`
	Devices          []OSPMetaDataDevice `json:"devices,omitempty"`
}

// OSPNetworkLink OSP Link metadata
type OSPNetworkLink struct {
	ID          string `json:"id"`
	VifID       string `json:"vif_id,omitempty"`
	Type        string `json:"type"`
	Mtu         int    `json:"mtu,omitempty"`
	EthernetMac string `json:"ethernet_mac_address"`
}

// OSPNetwork OSP Network metadata
type OSPNetwork struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Link      string `json:"link"`
	NetworkID string `json:"network_id"`
}

// OSPNetworkData OSP Network metadata
type OSPNetworkData struct {
	Links    []OSPNetworkLink `json:"links,omitempty"`
	Networks []OSPNetwork     `json:"networks,omitempty"`
	// Omit Services
}

func metaData(platformType PlatformType, address string) (netFilter string, macAddress string) {
	switch platformType {
	case VirtualOpenStack:
		metaData, networkData := readOpenstackMetaData()
		netFilter, macAddress = parseOpenstackMetaData(address, metaData, networkData)

	default:
		glog.V(2).Infof("Unknown PlatformType: %v", platformType)
	}

	return
}

func readOpenstackMetaData() (metaData *OSPMetaData, networkData *OSPNetworkData) {
	networkData = &OSPNetworkData{}

	rawBytes, err := ioutil.ReadFile(ospNetworkData)
	if err != nil {
		glog.Errorf("error reading file %s, %v", ospNetworkData, err)
		return
	}

	if err = json.Unmarshal(rawBytes, networkData); err != nil {
		glog.Errorf("error unmarshalling raw bytes %v from %s", err, ospNetworkData)
		return
	}

	metaData = &OSPMetaData{}

	rawBytes, err = ioutil.ReadFile(ospMetaData)
	if err != nil {
		glog.Errorf("error reading file %s, %v", ospMetaData, err)
		return
	}

	if err = json.Unmarshal(rawBytes, metaData); err != nil {
		glog.Errorf("error unmarshalling raw bytes %v from %s", err, ospNetworkData)
		return
	}

	return
}

func parseOpenstackMetaData(pciAddr string, metaData *OSPMetaData, networkData *OSPNetworkData) (networkID string, macAddress string) {

	if metaData == nil || networkData == nil {
		return
	}

	for _, device := range metaData.Devices {
		if pciAddr == device.Address {
			for _, link := range networkData.Links {
				if device.Mac == link.EthernetMac {
					for _, network := range networkData.Networks {
						if network.Link == link.ID {
							networkID = sriovnetworkv1.OpenstackNetworkID.String() + ":" + network.NetworkID
							macAddress = device.Mac
						}
					}
				}
			}
		}
	}

	return
}

// DiscoverSriovDevicesVirtual discovers VFs on a virtual platform
func DiscoverSriovDevicesVirtual(platformType PlatformType) ([]sriovnetworkv1.InterfaceExt, error) {
	glog.V(2).Info("DiscoverSriovDevicesVirtual")
	pfList := []sriovnetworkv1.InterfaceExt{}

	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("DiscoverSriovDevicesVirtual(): error getting PCI info: %v", err)
	}

	devices := pci.ListDevices()
	if len(devices) == 0 {
		return nil, fmt.Errorf("DiscoverSriovDevicesVirtual(): could not retrieve PCI devices")
	}

	for _, device := range devices {
		devClass, err := strconv.ParseInt(device.Class.ID, 16, 64)
		if err != nil {
			glog.Warningf("DiscoverSriovDevicesVirtual(): unable to parse device class for device %+v %q", device, err)
			continue
		}
		if devClass != netClass {
			// Not network device
			continue
		}

		netFilter, metaMac := metaData(platformType, device.Address)

		driver, err := dputils.GetDriverName(device.Address)
		if err != nil {
			glog.Warningf("DiscoverSriovDevicesVirtual(): unable to parse device driver for device %+v %q", device, err)
			continue
		}
		iface := sriovnetworkv1.InterfaceExt{
			PciAddress: device.Address,
			Driver:     driver,
			Vendor:     device.Vendor.ID,
			DeviceID:   device.Product.ID,
			NetFilter:  netFilter,
		}
		if mtu := getNetdevMTU(device.Address); mtu > 0 {
			iface.Mtu = mtu
		}
		if name := tryGetInterfaceName(device.Address); name != "" {
			iface.Name = name
			if iface.Mac = getNetDevMac(name); iface.Mac == "" {
				iface.Mac = metaMac
			}
			iface.LinkSpeed = getNetDevLinkSpeed(name)
		}
		iface.LinkType = getLinkType(iface)

		iface.TotalVfs = 1
		iface.NumVfs = 1

		vf := sriovnetworkv1.VirtualFunction{
			PciAddress: device.Address,
			Driver:     driver,
			VfID:       0,
			Vendor:     iface.Vendor,
			DeviceID:   iface.DeviceID,
			Mtu:        iface.Mtu,
			Mac:        iface.Mac,
		}
		iface.VFs = append(iface.VFs, vf)

		pfList = append(pfList, iface)
	}
	return pfList, nil
}

// SyncNodeStateVirtual attempt to update the node state to match the desired state
//  in virtual platforms
func SyncNodeStateVirtual(newState *sriovnetworkv1.SriovNetworkNodeState) error {
	var err error
	for _, ifaceStatus := range newState.Status.Interfaces {
		for _, iface := range newState.Spec.Interfaces {
			if iface.PciAddress == ifaceStatus.PciAddress {
				if !needUpdateVirtual(&iface, &ifaceStatus) {
					glog.V(2).Infof("SyncNodeStateVirtual(): no need update interface %s", iface.PciAddress)
					break
				}
				if err = configSriovDeviceVirtual(&iface, &ifaceStatus); err != nil {
					glog.Errorf("SyncNodeStateVirtual(): fail to config sriov interface %s: %v", iface.PciAddress, err)
					return err
				}
				break
			}
		}
	}
	return nil
}

func needUpdateVirtual(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) bool {
	// The device MTU is set by the platorm
	// The NumVfs is always 1
	if iface.NumVfs > 0 {
		for _, vf := range ifaceStatus.VFs {
			ingroup := false
			for _, group := range iface.VfGroups {
				if sriovnetworkv1.IndexInRange(vf.VfID, group.VfRange) {
					ingroup = true
					if group.DeviceType != "netdevice" {
						if group.DeviceType != vf.Driver {
							glog.V(2).Infof("needUpdateVirtual(): Driver needs update, desired=%s, current=%s", group.DeviceType, vf.Driver)
							return true
						}
					} else {
						if sriovnetworkv1.StringInArray(vf.Driver, DpdkDrivers) {
							glog.V(2).Infof("needUpdateVirtual(): Driver needs update, desired=%s, current=%s", group.DeviceType, vf.Driver)
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

func configSriovDeviceVirtual(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) error {
	glog.V(2).Infof("configSriovDeviceVirtual(): config interface %s with %v", iface.PciAddress, iface)
	// Config VFs
	if iface.NumVfs > 0 {
		if iface.NumVfs > 1 {
			glog.Warningf("configSriovDeviceVirtual(): in a virtual environment, only one VF per interface (NumVfs: %d)", iface.NumVfs)
			return errors.New("NumVfs > 1")
		}
		if len(iface.VfGroups) != 1 {
			glog.Warningf("configSriovDeviceVirtual(): missing VFGroup")
			return errors.New("NumVfs != 1")
		}
		addr := iface.PciAddress
		glog.V(2).Infof("configSriovDeviceVirtual(): addr %s", addr)
		driver := ""
		vfID := 0
		for _, group := range iface.VfGroups {
			glog.V(2).Infof("configSriovDeviceVirtual(): group %v", group)
			if sriovnetworkv1.IndexInRange(vfID, group.VfRange) {
				glog.V(2).Infof("configSriovDeviceVirtual(): indexInRange %d", vfID)
				if sriovnetworkv1.StringInArray(group.DeviceType, DpdkDrivers) {
					glog.V(2).Infof("configSriovDeviceVirtual(): driver %s", group.DeviceType)
					driver = group.DeviceType
				}
				break
			}
		}
		if driver == "" {
			glog.V(2).Infof("configSriovDeviceVirtual(): bind default")
			if err := BindDefaultDriver(addr); err != nil {
				glog.Warningf("configSriovDeviceVirtual(): fail to bind default driver for device %s", addr)
				return err
			}
		} else {
			glog.V(2).Infof("configSriovDeviceVirtual(): bind driver %s", driver)
			if err := BindDpdkDriver(addr, driver); err != nil {
				glog.Warningf("configSriovDeviceVirtual(): fail to bind driver %s for device %s", driver, addr)
				return err
			}
		}
	}
	return nil
}
