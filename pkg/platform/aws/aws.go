package aws

import (
	"fmt"
	"net/url"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	virtualplugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/virtual"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	// metadataBaseURL is the base URL for the EC2 instance metadata service.
	metadataBaseURL = "http://169.254.169.254/latest/meta-data/"
	// macsPath is the path to list MAC addresses.
	macsPath = "network/interfaces/macs/"
	// subnetIDPath is the path suffix for fetching subnet ID from metadata service.
	subnetIDPath = "subnet-id"
	// awsNetworkIDPrefix is the prefix used for AWS network ID in NetFilter field.
	// Format: "aws/NetworkID:<subnet-id>"
	awsNetworkIDPrefix = "aws/NetworkID:"
)

// Aws implements the platform.Interface for AWS EC2 instances.
// It handles SR-IOV device discovery for virtual functions in AWS environments,
// using metadata from the EC2 instance metadata service.
type Aws struct {
	hostHelpers        helper.HostHelpersInterface
	InitialDevicesInfo sriovnetworkv1.InterfaceExts
}

// New creates a new Aws platform instance.
// Returns a configured Aws platform or an error if initialization fails.
func New(hostHelpers helper.HostHelpersInterface) (*Aws, error) {
	return &Aws{
		hostHelpers: hostHelpers,
	}, nil
}

// Init initializes the AWS platform by loading device information.
// If a checkpoint exists, it loads device info from the saved node state.
// Otherwise, it queries the EC2 instance metadata service.
func (a *Aws) Init() error {
	ns, err := a.hostHelpers.GetCheckPointNodeState()
	if err != nil {
		return err
	}

	if ns == nil {
		err = a.createDevicesInfo()
		return err
	}

	a.createDevicesInfoFromNodeStatus(ns)
	return nil
}

// Name returns the name of the AWS platform.
func (a *Aws) Name() string {
	return string(consts.AWS)
}

// GetVendorPlugins returns the virtual plugin as the main plugin for AWS.
// AWS platforms only use the virtual plugin, with no additional plugins.
func (a *Aws) GetVendorPlugins(_ *sriovnetworkv1.SriovNetworkNodeState) (plugin.VendorPlugin, []plugin.VendorPlugin, error) {
	virtual, err := virtualplugin.NewVirtualPlugin(a.hostHelpers)
	return virtual, []plugin.VendorPlugin{}, err
}

// SystemdGetVendorPlugin is not supported on AWS platform.
// Returns ErrOperationNotSupportedByPlatform as AWS does not support systemd.
func (a *Aws) SystemdGetVendorPlugin(_ string) (plugin.VendorPlugin, error) {
	return nil, vars.ErrOperationNotSupportedByPlatform
}

// DiscoverSriovDevices discovers VFs on the AWS virtual platform.
// Each network device is treated as a single VF with TotalVfs=1 and NumVfs=1.
// Device metadata (MAC address, subnet ID) is enriched from EC2 metadata service.
// Returns a copy of the device info to prevent callers from modifying internal state.
func (a *Aws) DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error) {
	log.Log.V(2).Info("discovering sriov devices")

	result := make([]sriovnetworkv1.InterfaceExt, 0, len(a.InitialDevicesInfo))

	// TODO: check if we want to support hot plug here in the future
	for _, iface := range a.InitialDevicesInfo {
		// Create a copy to avoid modifying the original
		ifaceCopy := iface.DeepCopy()
		_, driver := a.hostHelpers.HasDriver(ifaceCopy.PciAddress)
		ifaceCopy.Driver = driver

		if mtu := a.hostHelpers.GetNetdevMTU(ifaceCopy.PciAddress); mtu > 0 {
			ifaceCopy.Mtu = mtu
		}

		if name := a.hostHelpers.TryToGetVirtualInterfaceName(ifaceCopy.PciAddress); name != "" {
			ifaceCopy.Name = name
			if macAddr := a.hostHelpers.GetNetDevMac(name); macAddr != "" {
				ifaceCopy.Mac = macAddr
			}
			ifaceCopy.LinkSpeed = a.hostHelpers.GetNetDevLinkSpeed(name)
			ifaceCopy.LinkType = a.hostHelpers.GetLinkType(name)
		}
		if len(ifaceCopy.VFs) != 1 {
			log.Log.Error(nil, "only one vf should exist", "iface", ifaceCopy)
			return nil, fmt.Errorf("unexpected number of vfs found for device %s", ifaceCopy.Name)
		}
		// Create a new VFs slice to ensure deep copy
		ifaceCopy.VFs = []sriovnetworkv1.VirtualFunction{{
			PciAddress: ifaceCopy.PciAddress,
			Driver:     driver,
			VfID:       0,
			Vendor:     ifaceCopy.Vendor,
			DeviceID:   ifaceCopy.DeviceID,
			Mtu:        ifaceCopy.Mtu,
			Mac:        ifaceCopy.Mac,
		}}
		result = append(result, *ifaceCopy)
	}
	return result, nil
}

// DiscoverBridges is not supported on AWS platform.
// Returns ErrOperationNotSupportedByPlatform as AWS does not support software bridge management.
func (a *Aws) DiscoverBridges() (sriovnetworkv1.Bridges, error) {
	return sriovnetworkv1.Bridges{}, vars.ErrOperationNotSupportedByPlatform
}

// createDevicesInfo creates the AWS device info map
// This function is used to create the AWS device info map from the metadata server.
func (a *Aws) createDevicesInfo() error {
	funcLog := log.Log.WithName("createDevicesInfo")
	a.InitialDevicesInfo = make(sriovnetworkv1.InterfaceExts, 0)
	funcLog.Info("getting aws network info from metadata server")

	macsURL, err := url.JoinPath(metadataBaseURL, macsPath)
	if err != nil {
		return fmt.Errorf("failed to construct MACs URL: %w", err)
	}

	metaData, err := a.hostHelpers.HTTPGetFetchData(macsURL)
	if err != nil {
		return fmt.Errorf("error getting aws meta_data from %s: %v", macsURL, err)
	}

	// If the endpoint returns an empty body (e.g., no MACs available or an issue),
	// treat it as no MACs found rather than an error. The caller can then decide how to handle an empty list.
	if metaData == "" {
		funcLog.Info("no MAC addresses found from metadata service, no devices loaded")
		return nil
	}

	// MAC addresses are returned separated by newlines, each ending with a '/'.
	// Example raw response: "0e:1a:95:aa:12:a1/\n0e:92:d3:ee:52:1b/"
	macEntries := strings.Split(metaData, "\n")
	if len(macEntries) == 0 {
		return nil
	}

	macAddressToSubNetID := map[string]string{}
	for _, macEntry := range macEntries {
		// Strip trailing '/' from MAC entry before constructing URL
		cleanMac := strings.TrimSuffix(macEntry, "/")
		// Example URL: http://169.254.169.254/latest/meta-data/network/interfaces/macs/0e:1a:95:aa:12:a1/subnet-id
		// Example raw response: "subnet-090ab34e7af072e18"
		subnetIDURL, err := url.JoinPath(metadataBaseURL, macsPath, cleanMac, subnetIDPath)
		if err != nil {
			return fmt.Errorf("failed to construct subnet ID URL for MAC %s: %w", cleanMac, err)
		}

		subnetIDData, err := a.hostHelpers.HTTPGetFetchData(subnetIDURL)
		if err != nil {
			return fmt.Errorf("error getting aws subnet_id from %s: %v", subnetIDURL, err)
		}

		if subnetIDData == "" {
			return fmt.Errorf("empty subnet_id from %s", subnetIDURL)
		}
		macAddressToSubNetID[cleanMac] = subnetIDData
	}

	pfList, err := a.hostHelpers.DiscoverSriovVirtualDevices()
	if err != nil {
		return err
	}

	for _, iface := range pfList {
		subnetID, exist := macAddressToSubNetID[iface.Mac]
		if !exist {
			funcLog.Error(nil, "unable to find subnet ID for device", "device", iface)
			continue
		}

		subnetID = strings.TrimPrefix(subnetID, "subnet-")
		iface.NetFilter = awsNetworkIDPrefix + subnetID
		iface.TotalVfs = 1
		iface.NumVfs = 1

		vf := sriovnetworkv1.VirtualFunction{
			PciAddress: iface.PciAddress,
			Driver:     iface.Driver,
			VfID:       0,
			Vendor:     iface.Vendor,
			DeviceID:   iface.DeviceID,
			Mtu:        iface.Mtu,
			Mac:        iface.Mac,
		}
		iface.VFs = append(iface.VFs, vf)
		a.InitialDevicesInfo = append(a.InitialDevicesInfo, iface)
	}
	funcLog.V(2).Info("loaded devices info from metadata server", "devices", a.InitialDevicesInfo)
	return nil
}

func (a *Aws) createDevicesInfoFromNodeStatus(networkState *sriovnetworkv1.SriovNetworkNodeState) {
	a.InitialDevicesInfo = make(sriovnetworkv1.InterfaceExts, 0, len(networkState.Status.Interfaces))
	for _, iface := range networkState.Status.Interfaces {
		a.InitialDevicesInfo = append(a.InitialDevicesInfo, *iface.DeepCopy())
	}
}
