package consts

import (
	"time"
)

type DrainState string

const (
	Chroot = "/host"
	Host   = "/host"

	ResyncPeriod               = 5 * time.Minute
	DaemonRequeueTime          = 30 * time.Second
	DrainControllerRequeueTime = 5 * time.Second

	DefaultConfigName                  = "default"
	ConfigDaemonPath                   = "./bindata/manifests/daemon"
	InjectorWebHookPath                = "./bindata/manifests/webhook"
	OperatorWebHookPath                = "./bindata/manifests/operator-webhook"
	MetricsExporterPath                = "./bindata/manifests/metrics-exporter"
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
	Role                               = "Role"
	RoleBinding                        = "RoleBinding"
	ServiceAccount                     = "ServiceAccount"
	DPConfigFileName                   = "config.json"
	OVSHWOLMachineConfigNameSuffix     = "ovs-hw-offload"

	LinkTypeEthernet   = "ether"
	LinkTypeInfiniband = "infiniband"

	LinkTypeIB  = "IB"
	LinkTypeETH = "ETH"

	LinkAdminStateUp   = "up"
	LinkAdminStateDown = "down"

	UninitializedNodeGUID = "0000:0000:0000:0000"

	DeviceTypeVfioPci   = "vfio-pci"
	DeviceTypeNetDevice = "netdevice"
	VdpaTypeVirtio      = "virtio"
	VdpaTypeVhost       = "vhost"

	RdmaSubsystemModeShared    = "shared"
	RdmaSubsystemModeExclusive = "exclusive"

	SriovConfBasePath          = "/etc/sriov-operator"
	PfAppliedConfig            = SriovConfBasePath + "/pci"
	SriovSwitchDevConfPath     = SriovConfBasePath + "/sriov_config.json"
	SriovHostSwitchDevConfPath = Host + SriovSwitchDevConfPath
	ManagedOVSBridgesPath      = SriovConfBasePath + "/managed-ovs-bridges.json"

	MachineConfigPoolPausedAnnotation       = "sriovnetwork.openshift.io/state"
	MachineConfigPoolPausedAnnotationIdle   = "Idle"
	MachineConfigPoolPausedAnnotationPaused = "Paused"

	SriovDevicePluginLabel         = "sriovnetwork.openshift.io/device-plugin"
	SriovDevicePluginLabelEnabled  = "Enabled"
	SriovDevicePluginLabelDisabled = "Disabled"

	NodeDrainAnnotation             = "sriovnetwork.openshift.io/state"
	NodeStateDrainAnnotation        = "sriovnetwork.openshift.io/desired-state"
	NodeStateDrainAnnotationCurrent = "sriovnetwork.openshift.io/current-state"
	DrainIdle                       = "Idle"
	DrainRequired                   = "Drain_Required"
	RebootRequired                  = "Reboot_Required"
	Draining                        = "Draining"
	DrainComplete                   = "DrainComplete"

	SyncStatusSucceeded  = "Succeeded"
	SyncStatusFailed     = "Failed"
	SyncStatusInProgress = "InProgress"

	DrainDeleted = "Deleted"
	DrainDelete  = "delete"
	DrainEvicted = "Evicted"
	DrainEvict   = "evict"

	PhasePre  = "pre"
	PhasePost = "post"

	MCPPauseAnnotationState = "sriovnetwork.openshift.io/state"
	MCPPauseAnnotationTime  = "sriovnetwork.openshift.io/time"

	OwnerRefAnnotation = "sriovnetwork.openshift.io/owner-ref"

	// NodeStateKeepUntilAnnotation contains name of the "keep until time" annotation for SriovNetworkNodeState object.
	// The "keep until time" specifies the earliest time at which the state object can be removed
	// if the daemon's pod is not found on the node.
	NodeStateKeepUntilAnnotation = "sriovnetwork.openshift.io/keep-state-until"
	// DefaultNodeStateCleanupDelayMinutes contains default delay before removing stale SriovNetworkNodeState CRs
	// (the CRs that no longer have a corresponding node with the daemon).
	DefaultNodeStateCleanupDelayMinutes = 30

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
	HostUdevFolder      = Host + UdevFolder
	UdevRulesFolder     = UdevFolder + "/rules.d"
	HostUdevRulesFolder = Host + UdevRulesFolder
	UdevDisableNM       = "/bindata/scripts/udev-find-sriov-pf.sh"
	UdevRepName         = "/bindata/scripts/switchdev-vf-link-name.sh"
	// nolint:goconst
	PFNameUdevRule = `SUBSYSTEM=="net", ACTION=="add", DRIVERS=="?*", KERNELS=="%s", NAME="%s"`
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

	KernelArgPciRealloc    = "pci=realloc"
	KernelArgIntelIommu    = "intel_iommu=on"
	KernelArgIommuPt       = "iommu=pt"
	KernelArgRdmaShared    = "ib_core.netns_mode=1"
	KernelArgRdmaExclusive = "ib_core.netns_mode=0"

	// Systemd consts
	SriovSystemdConfigPath        = SriovConfBasePath + "/sriov-interface-config.yaml"
	SriovSystemdResultPath        = SriovConfBasePath + "/sriov-interface-result.yaml"
	SriovSystemdSupportedNicPath  = SriovConfBasePath + "/sriov-supported-nics-ids.yaml"
	SriovSystemdServiceBinaryPath = "/var/lib/sriov/sriov-network-config-daemon"

	SriovServiceBasePath        = "/etc/systemd/system"
	SriovServicePath            = SriovServiceBasePath + "/sriov-config.service"
	SriovPostNetworkServicePath = SriovServiceBasePath + "/sriov-config-post-network.service"

	// Feature gates
	// ParallelNicConfigFeatureGate: allow to configure nics in parallel
	ParallelNicConfigFeatureGate = "parallelNicConfig"

	// ResourceInjectorMatchConditionFeatureGate: switch injector to fail policy and add mactch condition
	// this will make the mutating webhook to be called only when a pod has 'k8s.v1.cni.cncf.io/networks' annotation
	ResourceInjectorMatchConditionFeatureGate = "resourceInjectorMatchCondition"

	// MetricsExporterFeatureGate: enable SriovNetworkMetricsExporter on the same node as where the config-daemon run
	MetricsExporterFeatureGate = "metricsExporter"

	// ManageSoftwareBridgesFeatureGate: enables management of software bridges by the operator
	ManageSoftwareBridgesFeatureGate = "manageSoftwareBridges"

	// MellanoxFirmwareResetFeatureGate: enables the firmware reset via mstfwreset before a reboot
	MellanoxFirmwareResetFeatureGate = "mellanoxFirmwareReset"

	// The path to the file on the host filesystem that contains the IB GUID distribution for IB VFs
	InfinibandGUIDConfigFilePath = SriovConfBasePath + "/infiniband/guids"
)
