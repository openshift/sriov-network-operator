package types

import (
	"github.com/coreos/go-systemd/v22/unit"
	"github.com/jaypipes/ghw"
	"github.com/vishvananda/netlink"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/store"
)

type KernelInterface interface {
	// TryEnableTun load the tun kernel module
	TryEnableTun()
	// TryEnableVhostNet load the vhost-net kernel module
	TryEnableVhostNet()
	// TryEnableRdma tries to enable RDMA on the machine base on the operating system
	// if the package doesn't exist it will also will try to install it
	// supported operating systems are RHEL RHCOS and ubuntu
	TryEnableRdma() (bool, error)
	// TriggerUdevEvent triggers a udev event
	TriggerUdevEvent() error
	// GetCurrentKernelArgs reads the /proc/cmdline to check the current kernel arguments
	GetCurrentKernelArgs() (string, error)
	// IsKernelArgsSet check is the requested kernel arguments are set
	IsKernelArgsSet(string, string) bool
	// Unbind unbinds a virtual function from is current driver
	Unbind(string) error
	// BindDpdkDriver binds the virtual function to a DPDK driver
	BindDpdkDriver(string, string) error
	// BindDefaultDriver binds the virtual function to is default driver
	BindDefaultDriver(string) error
	// BindDriverByBusAndDevice binds device to the provided driver
	// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
	// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
	// driver - the name of the driver, e.g. vfio-pci or vhost_vdpa.
	BindDriverByBusAndDevice(bus, device, driver string) error
	// HasDriver returns try if the virtual function is bind to a driver
	HasDriver(string) (bool, string)
	// RebindVfToDefaultDriver rebinds the virtual function to is default driver
	RebindVfToDefaultDriver(string) error
	// UnbindDriverByBusAndDevice unbind device identified by bus and device ID from the driver
	// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
	// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
	UnbindDriverByBusAndDevice(bus, device string) error
	// UnbindDriverIfNeeded unbinds the virtual function from a driver if needed
	UnbindDriverIfNeeded(string, bool) error
	// LoadKernelModule loads a kernel module to the host
	LoadKernelModule(name string, args ...string) error
	// IsKernelModuleLoaded returns try if the requested kernel module is loaded
	IsKernelModuleLoaded(string) (bool, error)
	// ReloadDriver reloads a requested driver
	ReloadDriver(string) error
	// IsKernelLockdownMode returns true if the kernel is in lockdown mode
	IsKernelLockdownMode() bool
	// IsRHELSystem returns try if the system is a RHEL base
	IsRHELSystem() (bool, error)
	// IsUbuntuSystem returns try if the system is an ubuntu base
	IsUbuntuSystem() (bool, error)
	// IsCoreOS returns true if the system is a CoreOS or RHCOS base
	IsCoreOS() (bool, error)
	// RdmaIsLoaded returns try if RDMA kernel modules are loaded
	RdmaIsLoaded() (bool, error)
	// EnableRDMA enable RDMA on the system
	EnableRDMA(string, string, string) (bool, error)
	// InstallRDMA install RDMA packages on the system
	InstallRDMA(string) error
	// EnableRDMAOnRHELMachine enable RDMA on a RHEL base system
	EnableRDMAOnRHELMachine() (bool, error)
	// GetOSPrettyName returns OS name
	GetOSPrettyName() (string, error)
}

type NetworkInterface interface {
	// TryToGetVirtualInterfaceName tries to find the virtio interface name base on pci address
	// used for virtual environment where we pass SR-IOV virtual function into the system
	// supported platform openstack
	TryToGetVirtualInterfaceName(string) string
	// TryGetInterfaceName tries to find the SR-IOV virtual interface name base on pci address
	TryGetInterfaceName(string) string
	// GetPhysSwitchID returns the physical switch ID for a specific pci address
	GetPhysSwitchID(string) (string, error)
	// GetPhysPortName returns the physical port name for a specific pci address
	GetPhysPortName(string) (string, error)
	// IsSwitchdev returns true of the pci address is on switchdev mode
	IsSwitchdev(string) bool
	// GetNetdevMTU returns the interface MTU for devices attached to kernel drivers
	GetNetdevMTU(string) int
	// SetNetdevMTU sets the MTU for a request interface
	SetNetdevMTU(string, int) error
	// GetNetDevMac returns the network interface mac address
	GetNetDevMac(string) string
	// GetNetDevLinkSpeed returns the network interface link speed
	GetNetDevLinkSpeed(string) string
}

type ServiceInterface interface {
	// IsServiceExist checks if the requested systemd service exist on the system
	IsServiceExist(string) (bool, error)
	// IsServiceEnabled checks if the requested systemd service is enabled on the system
	IsServiceEnabled(string) (bool, error)
	// ReadService reads a systemd servers and return it as a struct
	ReadService(string) (*Service, error)
	// EnableService enables a systemd server on the host
	EnableService(service *Service) error
	// ReadServiceManifestFile reads the systemd manifest for a specific service
	ReadServiceManifestFile(path string) (*Service, error)
	// RemoveFromService removes a systemd service from the host
	RemoveFromService(service *Service, options ...*unit.UnitOption) (*Service, error)
	// ReadScriptManifestFile reads the script manifest from a systemd service
	ReadScriptManifestFile(path string) (*ScriptManifestFile, error)
	// ReadServiceInjectionManifestFile reads the injection manifest file for the systemd service
	ReadServiceInjectionManifestFile(path string) (*Service, error)
	// CompareServices compare two servers and return true if they are equal
	CompareServices(serviceA, serviceB *Service) (bool, error)
	// UpdateSystemService updates a system service on the host
	UpdateSystemService(serviceObj *Service) error
}

type SriovInterface interface {
	// SetSriovNumVfs changes the number of virtual functions allocated for a specific
	// physical function base on pci address
	SetSriovNumVfs(string, int) error
	// GetVfInfo returns the virtual function information is the operator struct from the host information
	GetVfInfo(string, []*ghw.PCIDevice) sriovnetworkv1.VirtualFunction
	// SetVfGUID sets the GUID for a virtual function
	SetVfGUID(string, netlink.Link) error
	// VFIsReady returns the interface virtual function if the device is ready
	VFIsReady(string) (netlink.Link, error)
	// SetVfAdminMac sets the virtual function administrative mac address via the physical function
	SetVfAdminMac(string, netlink.Link, netlink.Link) error
	// GetNicSriovMode returns the interface mode
	// supported modes SR-IOV legacy and switchdev
	GetNicSriovMode(string) (string, error)
	// GetLinkType return the link type
	// supported types are ethernet and infiniband
	GetLinkType(sriovnetworkv1.InterfaceExt) string
	// ResetSriovDevice resets the number of virtual function for the specific physical function to zero
	ResetSriovDevice(sriovnetworkv1.InterfaceExt) error
	// DiscoverSriovDevices returns a list of all the available SR-IOV capable network interfaces on the system
	DiscoverSriovDevices(store.ManagerInterface) ([]sriovnetworkv1.InterfaceExt, error)
	// ConfigSriovDevice configure the request SR-IOV device with the desired configuration
	ConfigSriovDevice(iface *sriovnetworkv1.Interface, ifaceStatus *sriovnetworkv1.InterfaceExt) error
	// ConfigSriovInterfaces configure multiple SR-IOV devices with the desired configuration
	ConfigSriovInterfaces(store.ManagerInterface, []sriovnetworkv1.Interface, []sriovnetworkv1.InterfaceExt, map[string]bool) error
	// ConfigSriovInterfaces configure virtual functions for virtual environments with the desired configuration
	ConfigSriovDeviceVirtual(iface *sriovnetworkv1.Interface) error
}

type UdevInterface interface {
	// WriteSwitchdevConfFile writes the needed switchdev configuration files for HW offload support
	WriteSwitchdevConfFile(*sriovnetworkv1.SriovNetworkNodeState, map[string]bool) (bool, error)
	// PrepareNMUdevRule creates the needed udev rules to disable NetworkManager from
	// our managed SR-IOV virtual functions
	PrepareNMUdevRule([]string) error
	// AddUdevRule adds a specific udev rule to the system
	AddUdevRule(string) error
	// RemoveUdevRule removes a udev rule from the system
	RemoveUdevRule(string) error
}
