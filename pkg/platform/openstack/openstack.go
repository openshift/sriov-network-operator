package openstack

import (
	"fmt"
	"strconv"

	"github.com/jaypipes/ghw"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dputils "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/utils"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	virtualplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/virtual"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	varConfigPath      = "/var/config"
	ospMetaDataBaseDir = "/openstack/2018-08-27"
	ospMetaDataDir     = varConfigPath + ospMetaDataBaseDir
	ospMetaDataBaseURL = "http://169.254.169.254" + ospMetaDataBaseDir
	ospNetworkDataJSON = "network_data.json"
	ospMetaDataJSON    = "meta_data.json"
	ospNetworkDataURL  = ospMetaDataBaseURL + "/" + ospNetworkDataJSON
	ospMetaDataURL     = ospMetaDataBaseURL + "/" + ospMetaDataJSON
	// Config drive is defined as an iso9660 or vfat (deprecated) drive
	// with the "config-2" label.
	//https://docs.openstack.org/nova/latest/user/config-drive.html
	configDriveLabel = "config-2"
)

var (
	ospNetworkDataFile = ospMetaDataDir + "/" + ospNetworkDataJSON
	ospMetaDataFile    = ospMetaDataDir + "/" + ospMetaDataJSON
)

// Openstack implements the platform.Interface for OpenStack virtual platforms.
// It handles SR-IOV device discovery for virtual functions in OpenStack environments,
// using metadata from the OpenStack metadata service or config drive.
type Openstack struct {
	hostHelpers          helper.HostHelpersInterface
	openStackDevicesInfo OSPDevicesInfo
}

// New creates a new Openstack platform instance.
// Returns a configured Openstack platform or an error if initialization fails.
func New(hostHelper helper.HostHelpersInterface) (*Openstack, error) {
	return &Openstack{
		hostHelpers: hostHelper,
	}, nil
}

// Init initializes the OpenStack platform by loading device information.
// If a checkpoint exists, it loads device info from the saved node state.
// Otherwise, it queries the OpenStack metadata service or config drive.
func (o *Openstack) Init() error {
	ns, err := o.hostHelpers.GetCheckPointNodeState()
	if err != nil {
		return err
	}

	if ns == nil {
		err = o.createDevicesInfo()
		return err
	}

	o.createDevicesInfoFromNodeStatus(ns)
	return nil
}

// Name returns the name of the OpenStack platform.
func (o *Openstack) Name() string {
	return "OpenStack"
}

// GetVendorPlugins returns the virtual plugin as the main plugin for OpenStack.
// OpenStack platforms only use the virtual plugin, with no additional plugins.
func (o *Openstack) GetVendorPlugins(_ *sriovnetworkv1.SriovNetworkNodeState) (plugin.VendorPlugin, []plugin.VendorPlugin, error) {
	virtual, err := virtualplugin.NewVirtualPlugin(o.hostHelpers)
	return virtual, []plugin.VendorPlugin{}, err
}

// SystemdGetVendorPlugin returns the appropriate plugin for systemd mode on OpenStack.
// For PhasePre, returns the virtual plugin.
// For PhasePost, returns nil as no post-configuration is needed for virtual platforms.
func (o *Openstack) SystemdGetVendorPlugin(phase string) (plugin.VendorPlugin, error) {
	switch phase {
	case consts.PhasePre:
		return virtualplugin.NewVirtualPlugin(o.hostHelpers)
	case consts.PhasePost:
		return nil, nil
	default:
		return nil, fmt.Errorf("invalid phase %s", phase)
	}
}

// DiscoverSriovDevices discovers VFs on the OpenStack virtual platform.
// Each network device is treated as a single VF with TotalVfs=1 and NumVfs=1.
// Device metadata (MAC address, network ID) is enriched from OpenStack metadata.
func (o *Openstack) DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	log.Log.V(2).Info("DiscoverSriovDevices()")
	pfList := []sriovnetworkv1.InterfaceExt{}

	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("DiscoverSriovDevicesVirtual(): error getting PCI info: %v", err)
	}

	devices := pci.Devices
	if len(devices) == 0 {
		return nil, fmt.Errorf("DiscoverSriovDevicesVirtual(): could not retrieve PCI devices")
	}

	for _, device := range devices {
		devClass, err := strconv.ParseInt(device.Class.ID, 16, 64)
		if err != nil {
			log.Log.Error(err, "DiscoverSriovDevicesVirtual(): unable to parse device class for device, skipping",
				"device", device)
			continue
		}
		if devClass != consts.NetClass {
			// Not network device
			continue
		}

		deviceInfo, exist := o.openStackDevicesInfo[device.Address]
		if !exist {
			log.Log.Error(nil, "DiscoverSriovDevicesVirtual(): unable to find device in devicesInfo list, skipping",
				"device", device.Address)
			continue
		}
		netFilter := deviceInfo.NetworkID
		metaMac := deviceInfo.MacAddress

		driver, err := dputils.GetDriverName(device.Address)
		if err != nil {
			log.Log.Error(err, "DiscoverSriovDevicesVirtual(): unable to parse device driver for device, skipping",
				"device", device)
			continue
		}
		iface := sriovnetworkv1.InterfaceExt{
			PciAddress: device.Address,
			Driver:     driver,
			Vendor:     device.Vendor.ID,
			DeviceID:   device.Product.ID,
			NetFilter:  netFilter,
		}
		if mtu := o.hostHelpers.GetNetdevMTU(device.Address); mtu > 0 {
			iface.Mtu = mtu
		}
		if name := o.hostHelpers.TryToGetVirtualInterfaceName(device.Address); name != "" {
			iface.Name = name
			if iface.Mac = o.hostHelpers.GetNetDevMac(name); iface.Mac == "" {
				iface.Mac = metaMac
			}
			iface.LinkSpeed = o.hostHelpers.GetNetDevLinkSpeed(name)
			iface.LinkType = o.hostHelpers.GetLinkType(name)
		}

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

// DiscoverBridges is not supported on OpenStack virtual platforms.
// Returns ErrOperationNotSupportedByPlatform as OpenStack does not support software bridge management.
func (o *Openstack) DiscoverBridges() (sriovnetworkv1.Bridges, error) {
	return sriovnetworkv1.Bridges{}, vars.ErrOperationNotSupportedByPlatform
}

// CreateOpenstackDevicesInfo create the openstack device info map
func (o *Openstack) createDevicesInfo() error {
	log.Log.Info("CreateDevicesInfo()")
	devicesInfo := make(OSPDevicesInfo)

	metaData, networkData, err := getOpenstackData(true)
	if err != nil {
		log.Log.Error(err, "failed to read OpenStack data")
		return err
	}

	if metaData == nil || networkData == nil {
		o.openStackDevicesInfo = make(OSPDevicesInfo)
		return nil
	}

	// use this for hw pass throw interfaces
	for _, device := range metaData.Devices {
		for _, link := range networkData.Links {
			if device.Mac == link.EthernetMac {
				for _, network := range networkData.Networks {
					if network.Link == link.ID {
						networkID := sriovnetworkv1.OpenstackNetworkID.String() + ":" + network.NetworkID
						devicesInfo[device.Address] = &OSPDeviceInfo{MacAddress: device.Mac, NetworkID: networkID}
					}
				}
			}
		}
	}

	// for vhostuser interface type we check the interfaces on the node
	pci, err := ghw.PCI()
	if err != nil {
		return fmt.Errorf("CreateOpenstackDevicesInfo(): error getting PCI info: %v", err)
	}

	devices := pci.Devices
	if len(devices) == 0 {
		return fmt.Errorf("CreateOpenstackDevicesInfo(): could not retrieve PCI devices")
	}

	for _, device := range devices {
		if _, exist := devicesInfo[device.Address]; exist {
			//we already discover the device via openstack metadata
			continue
		}

		devClass, err := strconv.ParseInt(device.Class.ID, 16, 64)
		if err != nil {
			log.Log.Error(err, "CreateOpenstackDevicesInfo(): unable to parse device class for device, skipping",
				"device", device)
			continue
		}
		if devClass != consts.NetClass {
			// Not network device
			continue
		}

		macAddress := ""
		if name := o.hostHelpers.TryToGetVirtualInterfaceName(device.Address); name != "" {
			if mac := o.hostHelpers.GetNetDevMac(name); mac != "" {
				macAddress = mac
			}
		}
		if macAddress == "" {
			// we didn't manage to find a mac address for the nic skipping
			continue
		}

		for _, link := range networkData.Links {
			if macAddress == link.EthernetMac {
				for _, network := range networkData.Networks {
					if network.Link == link.ID {
						networkID := sriovnetworkv1.OpenstackNetworkID.String() + ":" + network.NetworkID
						devicesInfo[device.Address] = &OSPDeviceInfo{MacAddress: macAddress, NetworkID: networkID}
					}
				}
			}
		}
	}

	o.openStackDevicesInfo = devicesInfo
	return nil
}

func (o *Openstack) createDevicesInfoFromNodeStatus(networkState *sriovnetworkv1.SriovNetworkNodeState) {
	devicesInfo := make(OSPDevicesInfo)
	for _, iface := range networkState.Status.Interfaces {
		devicesInfo[iface.PciAddress] = &OSPDeviceInfo{MacAddress: iface.Mac, NetworkID: iface.NetFilter}
	}

	o.openStackDevicesInfo = devicesInfo
}
