package types

import (
	"github.com/vishvananda/netlink"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/store"
)

type KernelInterface interface {
	// TryEnableTun load the tun kernel module
	TryEnableTun()
	// TryEnableVhostNet load the vhost-net kernel module
	TryEnableVhostNet()
	// CheckRDMAEnabled returns true if RDMA modules are loaded on host
	CheckRDMAEnabled() (bool, error)
	// GetCurrentKernelArgs reads the /proc/cmdline to check the current kernel arguments
	GetCurrentKernelArgs() (string, error)
	// IsKernelArgsSet check is the requested kernel arguments are set
	IsKernelArgsSet(cmdLine, karg string) bool
	// Unbind unbinds a virtual function from is current driver
	Unbind(pciAddr string) error
	// BindDpdkDriver binds the virtual function to a DPDK driver
	BindDpdkDriver(pciAddr, driver string) error
	// BindDefaultDriver binds the virtual function to is default driver
	BindDefaultDriver(pciAddr string) error
	// BindDriverByBusAndDevice binds device to the provided driver
	// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
	// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
	// driver - the name of the driver, e.g. vfio-pci or vhost_vdpa.
	BindDriverByBusAndDevice(bus, device, driver string) error
	// HasDriver returns try if the virtual function is bind to a driver
	HasDriver(pciAddr string) (bool, string)
	// GetDriverByBusAndDevice returns driver for the device or error.
	// returns "", nil if the device has no driver.
	// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
	// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
	GetDriverByBusAndDevice(bus, device string) (string, error)
	// RebindVfToDefaultDriver rebinds the virtual function to is default driver
	RebindVfToDefaultDriver(pciAddr string) error
	// UnbindDriverByBusAndDevice unbind device identified by bus and device ID from the driver
	// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
	// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
	UnbindDriverByBusAndDevice(bus, device string) error
	// UnbindDriverIfNeeded unbinds the virtual function from a driver if needed
	UnbindDriverIfNeeded(pciAddr string, isRdma bool) error
	// LoadKernelModule loads a kernel module to the host
	LoadKernelModule(name string, args ...string) error
	// IsKernelModuleLoaded returns try if the requested kernel module is loaded
	IsKernelModuleLoaded(name string) (bool, error)
	// IsKernelLockdownMode returns true if the kernel is in lockdown mode
	IsKernelLockdownMode() bool
}

type NetworkInterface interface {
	// TryToGetVirtualInterfaceName tries to find the virtio interface name base on pci address
	// used for virtual environment where we pass SR-IOV virtual function into the system
	// supported platform openstack
	TryToGetVirtualInterfaceName(pciAddr string) string
	// TryGetInterfaceName tries to find the SR-IOV virtual interface name base on pci address
	TryGetInterfaceName(pciAddr string) string
	// GetInterfaceIndex returns network interface index base on pci address or error if occurred
	GetInterfaceIndex(pciAddr string) (int, error)
	// GetPhysSwitchID returns the physical switch ID for a specific pci address
	GetPhysSwitchID(name string) (string, error)
	// GetPhysPortName returns the physical port name for a specific pci address
	GetPhysPortName(name string) (string, error)
	// IsSwitchdev returns true of the pci address is on switchdev mode
	IsSwitchdev(name string) bool
	// GetNetdevMTU returns the interface MTU for devices attached to kernel drivers
	GetNetdevMTU(pciAddr string) int
	// SetNetdevMTU sets the MTU for a request interface
	SetNetdevMTU(pciAddr string, mtu int) error
	// GetNetDevMac returns the network interface mac address
	GetNetDevMac(name string) string
	// GetNetDevNodeGUID returns the network interface node GUID if device is RDMA capable otherwise returns empty string
	GetNetDevNodeGUID(pciAddr string) string
	// GetNetDevLinkSpeed returns the network interface link speed
	GetNetDevLinkSpeed(name string) string
	// GetDevlinkDeviceParam returns devlink parameter for the device as a string, if the parameter has multiple values
	// then the function will return only first one from the list.
	GetDevlinkDeviceParam(pciAddr, paramName string) (string, error)
	// SetDevlinkDeviceParam set devlink parameter for the device, accepts paramName and value
	// as a string. Automatically set CMODE for the parameter and converts the value to the right
	// type before submitting it.
	SetDevlinkDeviceParam(pciAddr, paramName, value string) error
	// EnableHwTcOffload make sure that hw-tc-offload feature is enabled if device supports it
	EnableHwTcOffload(ifaceName string) error
	// GetNetDevLinkAdminState returns the admin state of the interface.
	GetNetDevLinkAdminState(ifaceName string) string
	// GetPciAddressFromInterfaceName parses sysfs to get pci address of an interface by name
	GetPciAddressFromInterfaceName(interfaceName string) (string, error)
	// DiscoverRDMASubsystem returns RDMA subsystem mode
	DiscoverRDMASubsystem() (string, error)
	// SetRDMASubsystem changes RDMA subsystem mode
	SetRDMASubsystem(mode string) error
}

type ServiceInterface interface {
	// IsServiceExist checks if the requested systemd service exist on the system
	IsServiceExist(servicePath string) (bool, error)
	// IsServiceEnabled checks if the requested systemd service is enabled on the system
	IsServiceEnabled(servicePath string) (bool, error)
	// ReadService reads a systemd servers and return it as a struct
	ReadService(servicePath string) (*Service, error)
	// EnableService enables a systemd server on the host
	EnableService(service *Service) error
	// ReadServiceManifestFile reads the systemd manifest for a specific service
	ReadServiceManifestFile(path string) (*Service, error)
	// ReadServiceInjectionManifestFile reads the injection manifest file for the systemd service
	ReadServiceInjectionManifestFile(path string) (*Service, error)
	// CompareServices returns true if serviceA needs update(doesn't contain all fields from service B)
	CompareServices(serviceA, serviceB *Service) (bool, error)
	// UpdateSystemService updates a system service on the host
	UpdateSystemService(serviceObj *Service) error
}

type SriovInterface interface {
	// SetSriovNumVfs changes the number of virtual functions allocated for a specific
	// physical function base on pci address
	SetSriovNumVfs(pciAddr string, numVfs int) error
	// VFIsReady returns the interface virtual function if the device is ready
	VFIsReady(pciAddr string) (netlink.Link, error)
	// SetVfAdminMac sets the virtual function administrative mac address via the physical function
	SetVfAdminMac(vfAddr string, pfLink netlink.Link, vfLink netlink.Link) error
	// GetNicSriovMode returns the interface mode
	// supported modes SR-IOV legacy and switchdev
	GetNicSriovMode(pciAddr string) string
	// SetNicSriovMode configure the interface mode
	// supported modes SR-IOV legacy and switchdev
	SetNicSriovMode(pciAddr, mode string) error
	// GetLinkType return the link type
	// supported types are ethernet and infiniband
	GetLinkType(name string) string
	// ResetSriovDevice resets the number of virtual function for the specific physical function to zero
	ResetSriovDevice(ifaceStatus sriovnetworkv1.InterfaceExt) error
	// DiscoverSriovDevices returns a list of all the available SR-IOV capable network interfaces on the system
	DiscoverSriovDevices(storeManager store.ManagerInterface) ([]sriovnetworkv1.InterfaceExt, error)
	// ConfigSriovInterfaces configure multiple SR-IOV devices with the desired configuration
	// if skipVFConfiguration flag is set, the function will configure PF and create VFs on it, but will skip VFs configuration
	ConfigSriovInterfaces(storeManager store.ManagerInterface, interfaces []sriovnetworkv1.Interface,
		ifaceStatuses []sriovnetworkv1.InterfaceExt, skipVFConfiguration bool) error
	// ConfigSriovInterfaces configure virtual functions for virtual environments with the desired configuration
	ConfigSriovDeviceVirtual(iface *sriovnetworkv1.Interface) error
}

type UdevInterface interface {
	// PrepareNMUdevRule creates the needed udev rules to disable NetworkManager from
	// our managed SR-IOV virtual functions
	PrepareNMUdevRule() error
	// PrepareVFRepUdevRule creates a script which helps to configure representor name for the VF
	PrepareVFRepUdevRule() error
	// AddDisableNMUdevRule adds udev rule that disables NetworkManager for VFs on the concrete PF:
	AddDisableNMUdevRule(pfPciAddress string) error
	// RemoveDisableNMUdevRule removes udev rule that disables NetworkManager for VFs on the concrete PF
	RemoveDisableNMUdevRule(pfPciAddress string) error
	// AddPersistPFNameUdevRule add udev rule that preserves PF name after switching to switchdev mode
	AddPersistPFNameUdevRule(pfPciAddress, pfName string) error
	// RemovePersistPFNameUdevRule removes udev rule that preserves PF name after switching to switchdev mode
	RemovePersistPFNameUdevRule(pfPciAddress string) error
	// AddVfRepresentorUdevRule adds udev rule that renames VF representors on the concrete PF
	AddVfRepresentorUdevRule(pfPciAddress, pfName, pfSwitchID, pfSwitchPort string) error
	// RemoveVfRepresentorUdevRule removes udev rule that renames VF representors on the concrete PF
	RemoveVfRepresentorUdevRule(pfPciAddress string) error
	// LoadUdevRules triggers udev rules for network subsystem
	LoadUdevRules() error
	// WaitUdevEventsProcessed calls `udevadm settle“ with provided timeout
	// The command watches the udev event queue, and exits if all current events are handled.
	WaitUdevEventsProcessed(timeout int) error
}

type VdpaInterface interface {
	// CreateVDPADevice creates VDPA device for VF with required type
	CreateVDPADevice(pciAddr, vdpaType string) error
	// DeleteVDPADevice removes VDPA device for provided pci address
	DeleteVDPADevice(pciAddr string) error
	// DiscoverVDPAType returns type of existing VDPA device for VF,
	// returns empty string if VDPA device not found or unknown driver is in use
	DiscoverVDPAType(pciAddr string) string
}

type BridgeInterface interface {
	// DiscoverBridges returns information about managed bridges on the host
	DiscoverBridges() (sriovnetworkv1.Bridges, error)
	// ConfigureBridge configure managed bridges for the host
	ConfigureBridges(bridgesSpec sriovnetworkv1.Bridges, bridgesStatus sriovnetworkv1.Bridges) error
	// DetachInterfaceFromManagedBridge detach interface from a managed bridge,
	// this step is required before applying some configurations to PF, e.g. changing of eSwitch mode.
	// The function detach interface from managed bridges only.
	DetachInterfaceFromManagedBridge(pciAddr string) error
}

type InfinibandInterface interface {
	// ConfigureVfGUID configures and sets a GUID for an IB VF device
	ConfigureVfGUID(vfAddr string, pfAddr string, vfID int, pfLink netlink.Link) error
}

type CPUVendor int

const (
	CPUVendorIntel CPUVendor = iota
	CPUVendorAMD
	CPUVendorARM
)

type CPUInfoProviderInterface interface {
	// Retrieve the CPU vendor of the current system
	GetCPUVendor() (CPUVendor, error)
}

type SystemdInterface interface {
	ReadConfFile() (spec *SriovConfig, err error)
	WriteConfFile(newState *sriovnetworkv1.SriovNetworkNodeState) (bool, error)
	WriteSriovResult(result *SriovResult) error
	ReadSriovResult() (*SriovResult, error)
	RemoveSriovResult() error
	WriteSriovSupportedNics() error
	ReadSriovSupportedNics() ([]string, error)
	CleanSriovFilesFromHost(isOpenShift bool) error
}
