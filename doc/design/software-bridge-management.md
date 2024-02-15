---
title: software bridge management
authors:
  - ykulazhenkov
reviewers:
creation-date: 15-02-2024
last-updated: 15-02-2024
---

# Software bridge management

## Summary

When NIC is configured to switchdev mode, a VF representor net device is created for each VF on it. 
These representors are used by a software switch (OVS, Linux bridge) to control traffic and configure hardware offloads. 
The software bridge is an essential part of using NICs in switchdev mode.

**sriov-network-operator** can set switchdev mode for a NIC and create VFs on it, 
but it doesn't provide any functionality to create and configure software bridges. 

This document contains a proposal to add limited support for software bridges configuration to the **sriov-network-operator**.

This feature assumes integration with [ovs-cni](https://github.com/k8snetworkplumbingwg/ovs-cni) and 
[accelerated-bridge-cni](https://github.com/k8snetworkplumbingwg/accelerated-bridge-cni).

Depends on [_switchdev and systemd modes refactoring_](switchdev-refactoring.md) feature.

## Motivation

SRIOV Legacy mode is no longer actively developed, and we need to encourage users to migrate to switchdev mode,
which is actively developed and will continue to receive new features and improvements.

To promote switching to switchdev configurations, we need to provide a nice UX for the end user.
This requires providing an easy way to configure software switches, which are prerequisites for NICs in switchdev mode.


### Use Cases

* As a user, I expect that **sriov-network-operator** will install `ovs-cni` and `accelerated-bridge-cni` to hosts.
* As a user, I want to create `OVSNetwork` CR, which will result in creation of `NetworkAttachmentDefinition` CR that uses
`ovs-cni` and contains required resource request.
* As a user, I want to create `BridgeNetwork` CR which will result in creation of `NetworkAttachmentDefinition` CR that uses
`accelerated-bridge-cni` and contains the required resource requests.
* As a user, I want to define configuration for software bridges inside the `SriovNetworkNodePolicy` CR and expect that the
operator will create required bridges, configure them, and attach uplinks (physical functions).

### Goals

* handle installation of `ovs-cni` and `accelerated-bridge-cni`
* add `OVSNetwork` and `BridgeNetwork` CRDs as an API for end users to simplify creation of `NetworkAttachmentDefinition` CR
for `ovs-cni` and `accelerated-bridge-cni`
* extend `SriovNetworkNodePolicy` CRD to support configuration of software bridges (bridge-level configuration)
* extend `SriovNetworkNodeState` (spec and status) CRD to support configuration of software bridges (bridge-level configuration)
* support configuration of software bridge in both modes (operator's `configurationMode` setting): `daemon` and `systemd`
* implementation should be compatible with [_Externally Managed PF_](externally-manage-pf.md) feature


### Non-Goals

* replace `SriovNetworkPoolConfig` CRD
* change API for host-level settings, e.g. `ovs-hw-offload`
	
	_**Note:** we may need to extend this API to support additional options_

* add support for VF-lag use-case

## Proposal

1. deploy `ovs-cni` and `accelerated-bridge-cni` with init containers of `sriov-network-config-daemon` Pod
2. define `OVSNetwork` and `BridgeNetwork` CRDs and implement controllers for them which will create `NetworkAttachmentDefinition` CRs
3. extend `SriovNetworkNodePolicy`and `SriovNetworkNodeState` (spec and status) CRDs to support configuration of software bridges (bridge-level configuration)


### Workflow Description

Implementation should be compatible with the following workflows:

* [Fully automatic workflow](#fully-automatic-workflow)
* [NIC configuration only flow](#nic-configuration-only-flow) (Externally managed bridge)
* [Externally managed NIC flow](#externally-managed-nic-flow)

#### Fully automatic workflow

This workflow assumes that **sriov-network-operator** handles PFs and VFs configuration, creation and configuration of software bridge, announcement of SRIOV resources with device plugin and preparation of `NetworkAttachmentDefinition` CR.

1. User creates `SriovNetworkNodePolicy` where:
    * eswitch mode set to `switchdev`
    * configuration for selected software bridge is defined

2. The operator populates `SriovNetworkNodeState` for matching nodes with PF and bridge
configurations 

3. `sriov-network-config-daemon` applies PF configuration, create and configure bridge, attach PF to the bridge, applies VF configuration


    _**Note 1:** if the operator runs in the `systemd` mode then bridge creation should happen in the `pre` phase._

    _**Note 2:** we should create udev rule which will set `NM_UNMANAGED=1` for bridges created by the operator_

4. `sriov-network-config-daemon` should report information about software bridges in the status field of the `SriovNetworkNodeState` CR.

5. SRIOV resources are announced by the Device plugin

6. User creates `OVSNetwork` or `BridgeNetwork` CR to create `NetworkAttachmentDefinition` CR, which relies on resources announced by the Device Plugin


_**Note:** created software bridge should be removed during the PF configuration reset_


#### NIC configuration only flow

This workflow is kept to support existing HW offloading use-case. 

In this case, **sriov-network-operator** handles PFs and VFs configuration, announcement of SRIOV resources with device plugin and preparation of `NetworkAttachmentDefinition` CR.

In some scenarios, it may be compatible with `configurationMode: daemon`, but it is supposed to be used when the operator runs in `systemd` mode.

1. User creates `SriovNetworkNodePolicy` where:
    * eswitch mode set to `switchdev`

    _**Note:** `SriovNetworkNodePolicy` CR should not include bridge configuration_


2. The operator populates `SriovNetworkNodeState` for matching nodes with PF configurations

3. `pre` systemd service creates VFs and configure PF

4. NetworkManager or systemd-networkd or environment-specific scripts create bridge

5. `post` systemd service binds VFs to required driver and proceed with other configuration steps

6. SRIOV resources are announced by the Device plugin

7. User creates `OVSNetwork` or `BridgeNetwork` CR to create `NetworkAttachmentDefinition` CR which relies on resource announced by the Device Plugin

_**Note:** it is possible to use externally created `NetworkAttachmentDefinition` CR that contains configuration for any CNI plugin that support
resources from NICs in switchdev mode._


_**Note 1:** bridge is not removed during the PF configuration reset_

_**Note 2:** information about bridges is not reported in the status field of the `SriovNetworkNodeState` CR_

#### Externally managed NIC flow

In this case, **sriov-network-operator** handles announcement of SRIOV resources with device plugin and preparation of `NetworkAttachmentDefinition` CR.

1. User configures PFs, creates VFs, creates bridge and attaches PFs to the bridge with custom scripts.

2. User creates `SriovNetworkNodePolicy` where:
    * eswitch mode set to `switchdev`
    * numVFs set to be equal or less than amount of precreated VFs
    * `externallyManaged` set to true for PFs

3. `sriov-network-config-daemon` configure VFs

3. SRIOV resources are announced by the Device plugin

4. User creates `OVSNetwork` or `BridgeNetwork` CR to create `NetworkAttachmentDefinition` CR, which relies on resources announced by the Device Plugin

### API Extensions

#### Environment variables for the operator

| Variable                       | Description                                                       |
| ---                            | ---                                                               |
| `OVS_CNI_IMAGE`                | contains full image name for `ovs-cni`                            |
| `ACCELERATED_BRIDGE_CNI_IMAGE` | contains full image name for `accelerated-bridge-cni`             |
| `OVSDB_SOCKET_PATH`            | path to the OVSDB socket, used to path to the sriov config daemon |


#### Feature flags

`manageSoftwareBridges` - control state of the feature (default: `false`)

#### Daemon command line args

`--ovsdb-socket-path` - path to the OVSDB socket, defaults to `/var/run/openvswitch/db.sock`

`--manage-software-bridges` - enables management of the OVS and linux bridges by the operator

#### OVSNetwork CRD `new`

```golang
// OVSNetworkSpec defines the desired state of OvsNetwork
type OVSNetworkSpec struct {
	// Namespace of the NetworkAttachmentDefinition custom resource
	NetworkNamespace string `json:"networkNamespace,omitempty"`
	// OVS Network device plugin endpoint resource name
	ResourceName string `json:"resourceName"`
	// Capabilities to be configured for this network.
	// Capabilities supported: (mac|ips), e.g. '{"mac": true}'
	Capabilities string `json:"capabilities,omitempty"`
	// IPAM configuration to be used for this network.
	IPAM string `json:"ipam,omitempty"`
	// name of the OVS bridge, if not set OVS will automatically select bridge
	// based on VF PCI address
	Bridge string `json:"bridge,omitempty"`
	// Vlan to assign for the OVS port
	Vlan uint `json:"vlan,omitempty"`
	// Mtu for the OVS port
	MTU uint `json:"mtu",omitempty`
	// Trunk configuration for the OVS port
	Trunk []*TrunkConfig `json:"trunk,omitempty"`
	// The type of interface on ovs.
	InterfaceType string `json:"interfaceType,omitempty"`
	// MetaPluginsConfig configuration to be used in order to chain metaplugins
	MetaPluginsConfig string `json:"metaPlugins,omitempty"`
}

// OVSTrunkConfig contains configuration for OVS trunk
type TrunkConfig struct {
	MinID *uint `json:"minID,omitempty"`
	MaxID *uint `json:"maxID,omitempty"`
	ID    *uint `json:"id,omitempty"`
}

// OvsNetworkStatus defines the observed state of OvsNetwork
type OvsNetworkStatus struct {
}

// OvsNetwork is the Schema for the ovsnetworks API
type OvsNetwork struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OvsNetworkSpec   `json:"spec,omitempty"`
	Status OvsNetworkStatus `json:"status,omitempty"`
}

```

#### BridgeNetwork CRD `new`

```golang
// BridgeNetworkSpec defines the desired state of BridgeNetwork
type BridgeNetworkSpec struct {
	// Namespace of the NetworkAttachmentDefinition custom resource
	NetworkNamespace string `json:"networkNamespace,omitempty"`
	// OVS Network device plugin endpoint resource name
	ResourceName string `json:"resourceName"`
	// Capabilities to be configured for this network.
	// Capabilities supported: (mac|ips), e.g. '{"mac": true}'
	Capabilities string `json:"capabilities,omitempty"`
	// IPAM configuration to be used for this network.
	IPAM string `json:"ipam,omitempty"`
	// name of the Linux bridge, if not set will automatically select bridge
	// based on VF PCI address
	Bridge string `json:"bridge,omitempty"`
	// VLAN ID for VF
	Vlan uint `json:"vlan,omitempty"`
	// VLAN Trunk configuration
	Trunk []TrunkConfig `json:"trunk,omitempty"`
	// enable setting matching vlan tags on the bridge uplink interface, default is false
	SetUplinkVlan bool `json:"setUplinkVlan,omitempty"`
	// MTU for VF and representor
	MTU uint `json:"mtu,omitempty"`
	// MetaPluginsConfig configuration to be used in order to chain metaplugins
	MetaPluginsConfig string `json:"metaPlugins,omitempty"`
}

// BridgeNetworkStatus defines the observed state of BridgeNetwork
type BridgeNetworkStatus struct {
}

// BridgeNetwork is the Schema for the ovsnetworks API
type BridgeNetwork struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BridgeNetworkSpec   `json:"spec,omitempty"`
	Status BridgeNetworkStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BridgeNetworkList contains a list of BridgeNetwork
type BridgeNetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BridgeNetwork `json:"items"`
}

```

#### SriovNetworkNodePolicy CRD

```golang
// SriovNetworkNodePolicySpec defines the desired state of SriovNetworkNodePolicy
type SriovNetworkNodePolicySpec struct {
	// ...existing fields...
	// contains spec for the software bridge
	Bridge Bridge `json:"bridge,omitempty"`
}
// contains spec for the bridge
// only one bridge type can be set
type Bridge struct {
	// contains optional config for OVS bridge
	Ovs *OVSConfig `json:"ovs,omitempty"`
	// contains optional config for Linux bridge
	Linux *LinuxBridgeConfig `json:"linux,omitempty"`
}

// OVSConfig optional configuration for OVS bridge and uplink Interface
type OVSConfig struct {
	// contains bridge level settings
	Bridge OVSBridgeConfig `json:"bridge,omitempty"`
	// contains settings for uplink (PF)
	Uplink OVSUplinkConfig `json:"uplink,omitempty"`
}

// OVSBridgeConfig contains some options from the Bridge table in OVSDB
type OVSBridgeConfig struct {
	DatapathType string            `json:"datapathType,omitempty"`
	ExternalIDs  map[string]string `json:"externalIDs,omitempty"`
	OtherConfig  map[string]string `json:"otherConfig,omitempty"`
}

// OVSUplinkConfig contains PF interface configuration for the bridge
type OVSUplinkConfig struct {
	Interface OVSInterfaceConfig `json:"interface,omitempty"`
	// can be extended to support OVSPortConfig which will include
	// settings from the OVS Port table
}

// OVSInterfaceConfig contains some options from the Interface table of the OVSDB for PF
type OVSInterfaceConfig struct {
	Type        string            `json:"type,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
	ExternalIDs map[string]string `json:"externalIDs,omitempty"`
	OtherConfig map[string]string `json:"otherConfig,omitempty"`
}

// LinuxBridgeConfig optional configuration for Linux bridge and uplink interface
type LinuxBridgeConfig struct {
	Bridge BridgeConfig      `json:"bridge,omitempty"`
	Uplink map[string]string `json:"uplink,omitempty"` // TODO clarify required settings
}

// BridgeConfig contains some options for linux bridge
type BridgeConfig struct {
	VlanFiltering bool `json:"vlanFiltering,omitempty"`
	// +kubebuilder:validation:Enum=802.1Q;802.1ad
	VlanProtocol string `json:"vlanProtocol,omitempty"`
}
```

_**Note 1**: multiple policies can match single PF (vf range use-case), bridge settings from the policy with
 the higher priority (which is the one with the lowest `.spec.priority` value) will be applied to PF._

_**Note 2**: multiple NICs can match the same policy on a host. In this case a separate bridge will be created for each NIC._

#### SriovNetworkNodeState CRD

```golang

type SriovNetworkNodeStateSpec struct {
	// ...existing fields...
	Interfaces      Interfaces `json:"interfaces,omitempty"`
	Bridges         Bridges    `json:"bridges,omitempty"`
}

// SriovNetworkNodeStateStatus defines the observed state of SriovNetworkNodeState
type SriovNetworkNodeStateStatus struct {
	Interfaces    InterfaceExts `json:"interfaces,omitempty"`
	Bridges       Bridges       `json:"bridges,omitempty"`
	SyncStatus    string        `json:"syncStatus,omitempty"`
	LastSyncError string        `json:"lastSyncError,omitempty"`
}

// Bridges contains list of bridges
type Bridges struct {
	OVS   []OVSConfigExt         `json:"ovs,omitempty"`
	Linux []LinuxBridgeConfigExt `json:"linux,omitempty"`
}

type OVSConfigExt struct {
	// name of the bridge
	Name    string               `json:"name"`
	// bridge-level configuration for the bridge
	Bridge  OVSBridgeConfig      `json:"bridge,omitempty"`
	// uplink-level bridge configuration for each uplink(PF).
	// in the initial implementation will always contain one element
	Uplinks []OVSUplinkConfigExt `json:"uplinks,omitempty"`
}

type OVSUplinkConfigExt struct {
	// pci address of the PF
	PciAddress string             `json:"pciAddress"`
	// name of the PF interface
	Name       string             `json:"name,omitempty"`
	// configuration from the Interface OVS table for the PF
	Interface  OVSInterfaceConfig `json:"interface,omitempty"`
}

type LinuxBridgeConfigExt struct {
	Name    string                       `json:"name"`
	Bridge  BridgeConfig                 `json:"bridge,omitempty"`
	Uplinks []LinuxBridgeUPlinkConfigExt `json:"uplinks,omitempty"`
}

type LinuxBridgeUPlinkConfigExt struct {
	PciAddress string             `json:"pciAddress"`
	Name       string             `json:"name,omitempty"`
	Uplink     map[string]string  `json:"uplink,omitempty"`
}

type VirtualFunction struct {
	// ...existing fields...
	// contains VF representor name for NICs in switchdev mode
	RepresentorName string `json:"representorName,omitempty"`
}

```
`SriovNetworkNodeState.spec` and `SriovNetworkNodeState.status` should be extended to contain the same `Bridges` struct.

_**Note:** The `Bridges` struct in the `SriovNetworkNodeState.status` can later be extended based on user feedback
 to report additional information required to improve UX._

### Implementation Details/Notes/Constraints

The feature is only supported on baremetal clusters

#### Dependencies on changes in other projects

The proposed implementation requires changes in `ovs-cni` and `accelerated-bridge-cni`. We need to change their behavior when `deviceID` argument is provided in CNI ARGS.
If `deviceID` is set and `bridge` arg is empty, the cni plugin should try to automatically select the right bridge by following the chain: 

VF (PCI address is in `deviceID` arg) > PF > Bond (if PF is part of the bond) > Bridge 

_**Note:** `accelerated-bridge-cni` already has similar logic, but now it selects the bridge from the predefined list of bridges._


#### Phased implementation

The feature assumes phased implementation.

##### Phase 1

Add support for [NIC configuration only flow](#nic-configuration-only-flow) and
[Externally managed NIC flow](#externally-managed-nic-flow) for Open vSwitch

Requirements: 
* add bridge auto-selection logic to `ovs-cni`
* define `OVSNetwork` CRD and implement controller for it

##### Phase 2

Add support for [Fully automatic workflow](#fully-automatic-workflow) for Open vSwitch

Requirements:
* requirements from phase 1
* extend `SriovNetworkNodePolicy` and `SriovNetworkNodeState` CRDs to support configuration for ovs (bridge-level configuration)
* modify code:
    * add support for ovs bridges creation
    * add support for reporting information about configured ovs bridges on the node
    * add support for removing auto-created ovs bridges during the PF reset

##### Phase 3

Add support for [NIC configuration only flow](#nic-configuration-only-flow),
[Externally managed NIC flow](#externally-managed-nic-flow) and [Fully automatic workflow](#fully-automatic-workflow) for Linux bridge

Requirements:
* add bridge auto-selection logic to `accelerated-bridge-cni`
* define `BridgeNetwork` CRD and implement controller for it
* extend `SriovNetworkNodePolicy` and `SriovNetworkNodeState` CRDs to support configuration for linux bridge (bridge-level configuration)
* modify code:
    * add support for linux bridges creation
    * add support for reporting information about configured linux bridges on the node
    * add support for removing auto-created linux bridges during the PF reset

### Upgrade & Downgrade considerations

This feature doesn't contain any breaking changes. 
Automatic upgrades should be safe and will not require any manual steps.

Downgrading without PF configuration reset may be problematic and may keep the node in an inconsistent state. 
It is recommended to reset PFs attached to bridges first and then do a downgrade.

### Test Plan

New functionality should be covered with unit tests. 

Manual and automatic e2e testing will require hardware with NICs that support switchev mode and hardware offloading for software bridges.



### Alternative options

#### Set configuration for software bridges in SriovNetworkPoolConfig CRD

```golang

type SriovNetworkPoolConfigSpec struct {
	// OvsHardwareOffloadConfig describes the OVS HWOL configuration for selected Nodes
	OvsHardwareOffloadConfig OvsHardwareOffloadConfig `json:"ovsHardwareOffloadConfig,omitempty"`
	// NodeSelector only valid for the fields below
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	Bridges []BridgeConf `json:"bridges,omitempty"`
}

type BridgeConf struct {
	// same configuration as in the main option
	Bridge *Bridge `json:"bridge"`
	// NicSelector uses the same type as SriovNetworkNodePolicySpec
	NicSelector SriovNetworkNicSelector `json:"nicSelector"`
}

```

The main problem with this option is that we can achieve the reliable scheduling of workloads only in a complicated way.
The scheduler considers information from the device plugins to ensure that required resources are available on the host before putting workloads on it.
In the main option outlined in this doc, a configuration of the bridge is a part of the `SriovNetworkNodePolicy` and that means that the bridge is for sure available on the host if the host announces resource name defined in `SriovNetworkNodePolicy`.

In that alternative option, there is no warranty that NodeSelector + NicSelector for a configuration of bridges is in sync with the NodeSelector + NicSelectors from a policy, so we can't rely on the sriov resource name from the policy to do a reliable scheduling - host can have SRIOV VFs, but may miss a bridge.
To solve this problem, we can create a device plugin, which will expose information about available bridges in the form of resources, 
e.g., `<prefix>/<bridge_type>_<pool_config_name>`. A user must explicitly request SRIOV + bridge resources while creating a Pod.