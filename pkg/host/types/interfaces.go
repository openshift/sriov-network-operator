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
	// ReloadDriver reloads a requested driver
	ReloadDriver(driver string) error
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
	EnableRDMA(conditionFilePath, serviceName, packageManager string) (bool, error)
	// InstallRDMA install RDMA packages on the system
	InstallRDMA(packageManager string) error
	// EnableRDMAOnRHELMachine enable RDMA on a RHEL base system
	EnableRDMAOnRHELMachine() (bool, error)
	// GetOSPrettyName returns OS name
	GetOSPrettyName() (string, error)
}

type NetworkInterface interface {
	// TryToGetVirtualInterfaceName tries to find the virtio interface name base on pci address
	// used for virtual environment where we pass SR-IOV virtual function into the system
	// supported platform openstack
	TryToGetVirtualInterfaceName(pciAddr string) string
	// TryGetInterfaceName tries to find the SR-IOV virtual interface name base on pci address
	TryGetInterfaceName(pciAddr string) string
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
	// GetNetDevLinkSpeed returns the network interface link speed
	GetNetDevLinkSpeed(name string) string
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
	SetSriovNumVfs(pciAddr string, numVfs int) error
	// GetVfInfo returns the virtual function information is the operator struct from the host information
	GetVfInfo(pciAddr string, devices []*ghw.PCIDevice) sriovnetworkv1.VirtualFunction
	// SetVfGUID sets the GUID for a virtual function
	SetVfGUID(vfAddr string, pfLink netlink.Link) error
	// VFIsReady returns the interface virtual function if the device is ready
	VFIsReady(pciAddr string) (netlink.Link, error)
	// SetVfAdminMac sets the virtual function administrative mac address via the physical function
	SetVfAdminMac(vfAddr string, pfLink netlink.Link, vfLink netlink.Link) error
	// GetNicSriovMode returns the interface mode
	// supported modes SR-IOV legacy and switchdev
	GetNicSriovMode(pciAddr string) (string, error)
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
		ifaceStatuses []sriovnetworkv1.InterfaceExt, pfsToConfig map[string]bool, skipVFConfiguration bool) error
	// ConfigSriovInterfaces configure virtual functions for virtual environments with the desired configuration
	ConfigSriovDeviceVirtual(iface *sriovnetworkv1.Interface) error
}

type UdevInterface interface {
	// WriteSwitchdevConfFile writes the needed switchdev configuration files for HW offload support
	WriteSwitchdevConfFile(newState *sriovnetworkv1.SriovNetworkNodeState, pfsToSkip map[string]bool) (bool, error)
	// PrepareNMUdevRule creates the needed udev rules to disable NetworkManager from
	// our managed SR-IOV virtual functions
	PrepareNMUdevRule(supportedVfIds []string) error
	// AddUdevRule adds a udev rule that disables network-manager for VFs on the concrete PF
	AddUdevRule(pfPciAddress string) error
	// RemoveUdevRule removes a udev rule that disables network-manager for VFs on the concrete PF
	RemoveUdevRule(pfPciAddress string) error
	// AddVfRepresentorUdevRule adds udev rule that renames VF representors on the concrete PF
	AddVfRepresentorUdevRule(pfPciAddress, pfName, pfSwitchID, pfSwitchPort string) error
	// RemoveVfRepresentorUdevRule removes udev rule that renames VF representors on the concrete PF
	RemoveVfRepresentorUdevRule(pfPciAddress string) error
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
