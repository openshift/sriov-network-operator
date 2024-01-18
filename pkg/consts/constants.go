package consts

import (
	"fmt"
	"time"
)

type DrainState string

// PlatformTypes
type PlatformTypes int

const (
	Chroot = "/host"
	Host   = "/host"

	ResyncPeriod                       = 5 * time.Minute
	DefaultConfigName                  = "default"
	ConfigDaemonPath                   = "./bindata/manifests/daemon"
	InjectorWebHookPath                = "./bindata/manifests/webhook"
	OperatorWebHookPath                = "./bindata/manifests/operator-webhook"
	SystemdServiceOcpPath              = "./bindata/manifests/sriov-config-service/openshift"
	SystemdServiceOcpMachineConfigName = "sriov-config-service"
	ServiceCAConfigMapAnnotation       = "service.beta.openshift.io/inject-cabundle"
	InjectorWebHookName                = "network-resources-injector-config"
	OperatorWebHookName                = "sriov-operator-webhook-config"
	DeprecatedOperatorWebHookName      = "operator-webhook-config"
	PluginPath                         = "./bindata/manifests/plugins"
	DaemonPath                         = "./bindata/manifests/daemon"
	DefaultPolicyName                  = "default"
	ConfigMapName                      = "device-plugin-config"
	DaemonSet                          = "DaemonSet"
	ServiceAccount                     = "ServiceAccount"
	DPConfigFileName                   = "config.json"
	OVSHWOLMachineConfigNameSuffix     = "ovs-hw-offload"

	LinkTypeEthernet   = "ether"
	LinkTypeInfiniband = "infiniband"

	LinkTypeIB  = "IB"
	LinkTypeETH = "ETH"

	DeviceTypeVfioPci   = "vfio-pci"
	DeviceTypeNetDevice = "netdevice"
	VdpaTypeVirtio      = "virtio"
	VdpaTypeVhost       = "vhost"

	ClusterTypeOpenshift  = "openshift"
	ClusterTypeKubernetes = "kubernetes"

	SriovConfBasePath          = "/etc/sriov-operator"
	PfAppliedConfig            = SriovConfBasePath + "/pci"
	SriovSwitchDevConfPath     = SriovConfBasePath + "/sriov_config.json"
	SriovHostSwitchDevConfPath = Host + SriovSwitchDevConfPath

	DrainAnnotationState         = "sriovnetwork.openshift.io/state"
	DrainAnnotationStateRequired = "sriovnetwork.openshift.io/state-required"
	DrainAnnotationTime          = "sriovnetwork.openshift.io/state-time"

	DrainIdle        DrainState = "Idle"
	DrainDisabled    DrainState = "Drain_Disabled"
	DrainRequired    DrainState = "Drain_Required"
	RebootRequired   DrainState = "Reboot_Required"
	DrainMcpPausing  DrainState = "Draining_MCP_Pausing"
	DrainMcpPaused   DrainState = "Draining_MCP_Paused"
	Draining         DrainState = "Draining"
	DrainingComplete DrainState = "Draining_Complete"
	RebootComplete   DrainState = "Reboot_Complete"

	SyncStatusSucceeded  = "Succeeded"
	SyncStatusFailed     = "Failed"
	SyncStatusInProgress = "InProgress"

	MCPPauseAnnotationState = "sriovnetwork.openshift.io/state"
	MCPPauseAnnotationTime  = "sriovnetwork.openshift.io/time"

	CheckpointFileName = "sno-initial-node-state.json"
	Unknown            = "Unknown"

	SysBus                = "/sys/bus"
	SysBusPciDevices      = SysBus + "/pci/devices"
	SysBusPciDrivers      = SysBus + "/pci/drivers"
	SysBusPciDriversProbe = SysBus + "/pci/drivers_probe"
	SysClassNet           = "/sys/class/net"
	ProcKernelCmdLine     = "/proc/cmdline"
	NetClass              = 0x02
	NumVfsFile            = "sriov_numvfs"
	BusPci                = "pci"
	BusVdpa               = "vdpa"

	UdevFolder          = "/etc/udev"
	UdevRulesFolder     = UdevFolder + "/rules.d"
	HostUdevRulesFolder = Host + UdevRulesFolder
	UdevDisableNM       = "/bindata/scripts/udev-find-sriov-pf.sh"
	// nolint:goconst
	NMUdevRule = `SUBSYSTEM=="net", ` +
		`ACTION=="add|change|move", ` +
		`ATTRS{device}=="%s", ` +
		`IMPORT{program}="/etc/udev/disable-nm-sriov.sh $env{INTERFACE} %s"`
	// nolint:goconst
	SwitchdevUdevRule = `SUBSYSTEM=="net", ` +
		`ACTION=="add|move", ` +
		`ATTRS{phys_switch_id}=="%s", ` +
		`ATTR{phys_port_name}=="pf%svf*", ` +
		`IMPORT{program}="/etc/udev/switchdev-vf-link-name.sh $attr{phys_port_name}", ` +
		`NAME="%s_$env{NUMBER}"`

	KernelArgPciRealloc = "pci=realloc"
	KernelArgIntelIommu = "intel_iommu=on"
	KernelArgIommuPt    = "iommu=pt"
)

const (
	// Baremetal platform
	Baremetal PlatformTypes = iota
	// VirtualOpenStack platform
	VirtualOpenStack
)

func (e PlatformTypes) String() string {
	switch e {
	case Baremetal:
		return "Baremetal"
	case VirtualOpenStack:
		return "Virtual/Openstack"
	default:
		return fmt.Sprintf("%d", int(e))
	}
}
