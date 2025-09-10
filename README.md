# sriov-network-operator

The Sriov Network Operator is designed to help the user to provision and configure SR-IOV CNI plugin and Device plugin in the Openshift cluster.

## Motivation

SR-IOV network is an optional feature of an Openshift cluster. To make it work, it requires different components to be provisioned and configured accordingly. It makes sense to have one operator to coordinate those relevant components in one place, instead of having them managed by different operators. And also, to hide the complexity, we should provide an elegant user interface to simplify the process of enabling SR-IOV.

## Features

- Initialize the supported SR-IOV NIC types on selected nodes.
- Provision/upgrade SR-IOV device plugin executable on selected node.  
- Provision/upgrade SR-IOV CNI plugin executable on selected nodes.
- Manage configuration of SR-IOV device plugin on host.
- Generate net-att-def CRs for SR-IOV CNI plugin
- Supports operation in a virtualized Kubernetes deployment
  - Discovers VFs attached to the Virtual Machine (VM)
  - Does not require attached of associated PFs
  - VFs can be associated to SriovNetworks by selecting the appropriate PciAddress as the RootDevice in the SriovNetworkNodePolicy

## Quick Start

For more detail on installing this operator, refer to the [quick-start](doc/quickstart.md) guide.

## API

The SR-IOV network operator introduces following new CRDs:

- SriovNetwork

- OVSNetwork

- SriovNetworkNodeState

- SriovNetworkNodePolicy

### SriovNetwork

A custom resource of SriovNetwork could represent the a layer-2 broadcast domain where some SR-IOV devices are attach to. It is primarily used to generate a NetworkAttachmentDefinition CR with an SR-IOV CNI plugin configuration. 

This SriovNetwork CR also contains the ‘resourceName’ which is aligned with the ‘resourceName’ of SR-IOV device plugin. One SriovNetwork obj maps to one ‘resoureName’, but one ‘resourceName’ can be shared by different SriovNetwork CRs.

This CR should be managed by cluster admin. Here is an example:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: example-network
  namespace: example-namespace
spec:
  ipam: |
    {
      "type": "host-local",
      "subnet": "10.56.217.0/24",
      "rangeStart": "10.56.217.171",
      "rangeEnd": "10.56.217.181",
      "routes": [{
        "dst": "0.0.0.0/0"
      }],
      "gateway": "10.56.217.1"
    }
  vlan: 0
  resourceName: intelnics
```

#### Chaining CNI metaplugins

It is possible to add additional capabilities to the device configured via the SR-IOV configuring optional metaplugins.

In order to do this, the `metaPlugins` field must contain the array of one or more additional configurations used to build a [network configuration list](https://github.com/containernetworking/cni/blob/master/SPEC.md#network-configuration-lists), as per the following example:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: example-network
  namespace: example-namespace
spec:
  ipam: |
    {
      "type": "host-local",
      "subnet": "10.56.217.0/24",
      "rangeStart": "10.56.217.171",
      "rangeEnd": "10.56.217.181",
      "routes": [{
        "dst": "0.0.0.0/0"
      }],
      "gateway": "10.56.217.1"
    }
  vlan: 0
  resourceName: intelnics
  metaPlugins : |
    {
      "type": "tuning",
      "sysctl": {
        "net.core.somaxconn": "500"
      }
    },
    {
      "type": "vrf",
      "vrfname": "red"
    }
```

#### Configuring SriovNetwork with the RDMA CNI Plugin

RDMA CNI enables Pod exclusive access to RDMA resources (and their associated hardware counters).
To use RDMA CNI the following is required:

**1. RDMA subsystem mode set to `exclusive`**

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: rdma-workers
  namespace: sriov-network-operator
spec:
  maxUnavailable: 1
  rdmaMode: exclusive
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

**2. SriovNetwork is using RDMA CNI plugin as a meta plugin**

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: rdma-network
  namespace: sriov-network-operator
spec:
  resourceName: rdma_exclusive_device
  networkNamespace: default
  ipam: |
    {
      "type": "host-local",
      "subnet": "10.10.10.0/24",
      "rangeStart": "10.10.10.171",
      "rangeEnd": "10.10.10.181"
    }
  metaPluginsConfig: |
    {
      "type": "rdma"
    }
```

This configuration:
- Uses `metaPluginsConfig` to inject the RDMA CNI plugin
- Allows pods using this network exclusive access to RDMA resources including their associated hardware counters
- Requires the nodes to be configured with RDMA mode set to exclusive
- Works with SR-IOV network policies that have `isRdma: true` specified

**3. SriovNetworkNodePolicy with `spec.isRdma=true` is used**

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: policy-1
  namespace: sriov-network-operator
spec:
  deviceType: netdevice
  nicSelector:
    pfName: ["eno1"]
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 4
  priority: 90
  resourceName: rdma_exclusive_device
```

**4. Pod references the SriovNetwork from step 2**

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: vdpa-pod1
  namespace: vdpa
  annotations:
    k8s.v1.cni.cncf.io/networks: policy-1
spec:
  containers:
  - name: vdpa-pod
    image: centos:latest
    imagePullPolicy: IfNotPresent
    command:
      - sleep
      - "3600"
```

### OVSNetwork

A custom resource of OVSNetwork could represent the a layer-2 broadcast domain attached to Open vSwitch that works in HW-offloading mode. 
It is primarily used to generate a NetworkAttachmentDefinition CR with an OVS CNI plugin configuration. 

The OVSNetwork CR also contains the `resourceName` which is aligned with the `resourceName` of SR-IOV device plugin. One OVSNetwork obj maps to one `resourceName`, but one `resourceName` can be shared by different OVSNetwork CRs.

It is expected that `resourceName` contains name of the resource pool which holds Virtual Functions of a NIC in the switchdev mode. 
A Physical function of the NIC should be attached to an OVS bridge before any workload which uses OVSNetwork starts.

Example:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: OVSNetwork
metadata:
  name: example-network
  namespace: example-namespace
spec:
  ipam: |
    {
      "type": "host-local",
      "subnet": "10.56.217.0/24",
      "rangeStart": "10.56.217.171",
      "rangeEnd": "10.56.217.181",
      "routes": [{
        "dst": "0.0.0.0/0"
      }],
      "gateway": "10.56.217.1"
    }
  vlan: 100
  bridge: my-bridge
  mtu: 2500
  resourceName: switchdevnics
```

### SriovNetworkNodeState

The custom resource to represent the SR-IOV interface states of each host, which should only be managed by the operator itself.

- The ‘spec’ of this CR represents the desired configuration which should be applied to the interfaces and SR-IOV device plugin.
- The ‘status’ contains current states of those PFs (baremetal only), and the states of the VFs. It helps user to discover SR-IOV network hardware on node, or attached VFs in the case of a virtual deployment.

The spec is rendered by sriov-policy-controller, and consumed by sriov-config-daemon. Sriov-config-daemon is responsible for updating the ‘status’ field to reflect the latest status, this information can be used as input to create SriovNetworkNodePolicy CR.

An example of SriovNetworkNodeState CR:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodeState
metadata:
  name: worker-node-1
  namespace: sriov-network-operator
spec:
  interfaces:
  - deviceType: vfio-pci
    mtu: 1500
    numVfs: 4
    pciAddress: 0000:86:00.0
status:
  interfaces:
  - deviceID: "1583"
    driver: i40e
    mtu: 1500
    numVfs: 4
    pciAddress: 0000:86:00.0
    maxVfs: 64
    vendor: "8086"
    Vfs:
      - deviceID: 154c
      driver: vfio-pci
      pciAddress: 0000:86:02.0
      vendor: "8086"
      - deviceID: 154c
      driver: vfio-pci
      pciAddress: 0000:86:02.1
      vendor: "8086"
      - deviceID: 154c
      driver: vfio-pci
      pciAddress: 0000:86:02.2
      vendor: "8086"
      - deviceID: 154c
      driver: vfio-pci
      pciAddress: 0000:86:02.3
      vendor: "8086"
  - deviceID: "1583"
    driver: i40e
    mtu: 1500
    pciAddress: 0000:86:00.1
    maxVfs: 64
    vendor: "8086"
```

From this example, in status field, the user can find out there are 2 SRIOV capable NICs on node 'work-node-1'; in spec field, user can learn what the expected configure is generated from the combination of SriovNetworkNodePolicy CRs.  In the virtual deployment case, a single VF will be associated with each device.

### SriovNetworkNodePolicy

This CRD is the key of SR-IOV network operator. This custom resource should be managed by cluster admin, to instruct the operator to:

1. Render the spec of SriovNetworkNodeState CR for selected node, to configure the SR-IOV interfaces.  In virtual deployment, the VF interface is read-only.
2. Deploy SR-IOV CNI plugin and device plugin on selected node.
3. Generate the configuration of SR-IOV device plugin.

An example of SriovNetworkNodePolicy CR:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: policy-1
  namespace: sriov-network-operator
spec:
  deviceType: vfio-pci
  mtu: 1500
  nicSelector:
    deviceID: "1583"
    rootDevices:
    - 0000:86:00.0
    vendor: "8086"
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 4
  priority: 90
  resourceName: intelnics
```

In this example, user selected the nic from vendor '8086' which is intel, device module is '1583' which is XL710 for 40GbE, on nodes labeled with 'network-sriov.capable' equals 'true'. Then for those PFs, create 4 VFs each, set mtu to 1500 and the load the vfio-pci driver to those virtual functions.  

In a virtual deployment: 
- The mtu of the PF is set by the underlying virtualization platform and cannot be changed by the sriov-network-operator.
- The numVfs parameter has no effect as there is always 1 VF
- The deviceType field depends upon whether the underlying device/driver is [native-bifurcating or non-bifurcating](https://doc.dpdk.org/guides/howto/flow_bifurcation.html) For example, the supported Mellanox devices support native-bifurcating drivers and therefore deviceType should be netdevice (default).  The support Intel devices are non-bifurcating and should be set to vfio-pci.

#### Multiple policies

When multiple SriovNetworkNodeConfigPolicy CRs are present, the `priority` field
(0 is the highest priority) is used to resolve any conflicts. Conflicts occur
only when same PF is referenced by multiple policies. The final desired
configuration is saved in `SriovNetworkNodeState.spec.interfaces`.

Policies processing order is based on priority (lowest first), followed by `name`
field (starting from `a`). Policies with same **priority** or **non-overlapping
VF groups** (when #-notation is used in pfName field) are merged, otherwise only
the highest priority policy is applied. In case of same-priority policies and
overlapping VF groups, only the last processed policy is applied.

When using #-notation to define VF group, no actions are taken on virtual functions that
are not mentioned in any policy (e.g. if a policy defines a `vfio-pci` device group for a device, when 
it is deleted the VF are not reset to the default driver).

#### Externally Manage virtual functions

When `ExternallyManage` is request on a policy the operator will only skip the virtual function creation.
The operator will only bind the virtual functions to the requested driver and expose them via the device plugin.
Another difference when this field is requested in the policy is that when this policy is removed the operator
will not remove the virtual functions from the policy.

*Note:* This means the user must create the virtual functions before they apply the policy or the webhook will reject
the policy creation.

It's possible to use something like nmstate kubernetes-nmstate or just a simple systemd file to create
the virtual functions on boot.

This feature was created to support deployments where the user want to use some of the virtual funtions for the host
communication like storage network or out of band managment and the virtual functions must exist on boot and not only
after the operator and config-daemon are running.

#### Disabling SR-IOV Config Daemon plugins

It is possible to disable SR-IOV network operator config daemon plugins in case their operation
is not needed or un-desirable.

As an example, some plugins perform vendor specific firmware configuration
to enable SR-IOV (e.g `mellanox` plugin). certain deployment environments may prefer to perform such configuration
once during node provisioning, while ensuring the configuration will be compatible with any sriov network node policy
defined for the particular environment. This will reduce or completely eliminate the need for reboot of nodes during SR-IOV
configurations by the operator.

This can be done by setting SriovOperatorConfig `default` CR `spec.disablePlugins` with the list of desired plugins
to disable.

**Example**:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  ...
  disablePlugins:
    - mellanox
  ...
```

> **NOTE**: Currently only `mellanox` plugin can be disabled.

## Feature Gates

Feature gates are used to enable or disable specific features in the operator.

> **NOTE**: As features mature and graduate to stable status, default settings may change, and feature gates might be removed in future releases. Keep this in mind when configuring feature gates and ensure your environment is compatible with any updates.

### Available Feature Gates

1. **Parallel NIC Configuration** (`parallelNicConfig`)
  - **Description:** Allows the configuration of NICs in parallel, which can potentially reduce the time required for network setup.
  - **Default:** Disabled

2. **Resource Injector Match Condition** (`resourceInjectorMatchCondition`)
  - **Description:** Switches the resource injector's webhook failure policy from "Ignore" to "Fail" by utilizing the `MatchConditions` feature introduced in Kubernetes 1.28. This ensures the webhook only targets pods with the `k8s.v1.cni.cncf.io/networks` annotation, improving reliability without affecting other pods.
  - **Default:** Disabled

3. **Metrics Exporter** (`metricsExporter`)
  - **Description:** Enables the metrics exporter on the same node where the config-daemon is running. This helps in collecting and exporting metrics related to SR-IOV network devices.
  - **Default:** Disabled

4. **Manage Software Bridges** (`manageSoftwareBridges`)
  - **Description:** Allows the operator to manage software bridges. This feature gate is useful for environments where bridge management is required.
  - **Default:** Disabled

5. **Mellanox Firmware Reset** (`mellanoxFirmwareReset`)
  - **Description:** Enables the firmware reset via `mstfwreset` before a system reboot. This feature is specific to Mellanox network devices and is used to ensure that the firmware is properly reset during system maintenance.
  - **Default:** Disabled

### Enabling Feature Gates

To enable a feature gate, add it to your configuration file or command line with the desired state. For example, to enable the `resourceInjectorMatchCondition` feature gate, you would specify:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    resourceInjectorMatchCondition: true
  ...
```

## SriovNetworkPoolConfig Configuration

The `SriovNetworkPoolConfig` CRD provides advanced configuration capabilities for managing groups of nodes in SR-IOV network environments.
This custom resource allows cluster administrators to define node-level configuration policies that apply to specific sets of nodes selected by label selectors.


The `SriovNetworkPoolConfig` CRD serves multiple purposes:

1. **Node Pool Management**: Groups nodes into logical pools for coordinated configuration updates
2. **Parallel Operations**: Enables controlled parallel draining and configuration updates across multiple nodes
3. **RDMA Configuration**: Provides centralized RDMA mode configuration for selected nodes

### Key Configuration Fields

#### Parallel Draining

It is possible to drain more than one node at a time using this operator.


The configuration is done via the SriovNetworkPoolConfig, selecting a number of nodes using the node selector and how many
nodes in parallel from the pool the operator can drain in parallel. maxUnavailable can be a number or a percentage.

- **nodeSelector**: Specifies which nodes belong to this pool using Kubernetes label selectors
- **maxUnavailable**: Controls how many nodes can be unavailable simultaneously during updates (supports both integer and percentage values)

> **NOTE**: every node can only be part of one pool, if a node is selected by more than one pool, then it will not be drained

> **NOTE**: If a node is not part of any pool it will have a default configuration of maxUnavailable 1


#### RDMA Mode Configuration

The `rdmaMode` field allows you to configure the RDMA (Remote Direct Memory Access) subsystem behavior for all nodes in the pool:

- **shared**: Multiple processes can share RDMA resources simultaneously
- **exclusive**: RDMA resources are exclusively assigned to a single process


### Configuration Examples

#### Basic Parallel Draining Configuration

The following configuration will allow to drain maximum 2 nodes in parallel for all the nodes requesting drain or reboot in the cluster.
This will apply only to nodes with the matching label selector.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: worker
  namespace: sriov-network-operator
spec:
  maxUnavailable: 2
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

### RDMA Mode

The RDMA mode setting affects how RDMA resources are managed across the nodes in the pool.
The RDMA mode configuration is applied during node configuration updates and affects all The Mellanox SR-IOV devices on the selected nodes.

*NOTE:* swtiching rdma mode will trigger a reboot to all the nodes in the pool based on the maxUnavailable configuration

#### Exclusive RDMA Mode Configuration

The following configuration will switch the host RDMA mode to `Exclusive`.
This will apply only to nodes with the matching label selector, and done one node at a time.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: rdma-workers
  namespace: sriov-network-operator
spec:
  maxUnavailable: 1
  rdmaMode: exclusive
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

## Components and design

This operator is split into 2 components:

- controller
- sriov-config-daemon

The controller is responsible for:

1. Read the SriovNetworkNodePolicy CRs and SriovNetwork CRs as input.
2. Render the manifests for SR-IOV CNI plugin and device plugin daemons.
3. Render the spec of SriovNetworkNodeState CR for each node.

The sriov-config-daemon is responsible for:

1. Discover the SRIOV NICs on each node, then sync the status of SriovNetworkNodeState CR.
2. Take the spec of SriovNetworkNodeState CR as input to configure those NICs.

## Workflow

![SRIOV Network Operator work flow](doc/images/workflow.png)
