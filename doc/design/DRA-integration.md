---
title: DRA (Dynamic Resource Allocation) Integration
authors:
  - rollandf
  - wizhaoredhat
reviewers:
  - SchSeba
  - adrianchiris
creation-date: 11-02-2026
last-updated: 24-09-2026
---

# DRA (Dynamic Resource Allocation) Integration

## Summary

This design proposes integrating Kubernetes Dynamic Resource Allocation (DRA) framework into the SR-IOV Network Operator as an alternative to the traditional device plugin approach. The integration will allow users to leverage the DRA driver for SR-IOV (`dra-driver-sriov`) instead of `sriov-network-device-plugin` for exposing and allocating SR-IOV Virtual Functions to workloads.

This will be implemented as an opt-in feature controlled by a feature flag, allowing gradual adoption and testing while maintaining backward compatibility with existing device plugin-based deployments.

## Implementation Status (2026-09-24)

Core DRA integration is **implemented** behind `featureGates.dynamicResourceAllocation` in:
- `controllers/sriovoperatorconfig_controller.go` — deploy DRA driver or device plugin
- `controllers/sriovnetworknodepolicy_controller.go` — sync `DeviceAttributes`, `SriovResourcePolicy`, extended-resource `DeviceClass`
- `controllers/helper.go` / `bindata/manifests/dra-driver/` — DRA driver DaemonSet, RBAC, base DeviceClass
- `pkg/daemon/daemon.go` — wait-for-config pod annotation coordination

Items marked *(Not yet implemented)* in this document remain planned work.

## Motivation

The Kubernetes Device Plugin framework has several limitations that the Dynamic Resource Allocation (DRA) framework addresses:

1. **Limited Resource Modeling**: Device plugins can only expose simple countable resources (e.g., `intel.com/sriov: 10`). They cannot express complex device characteristics, NUMA topology, or filtering criteria.

2. **Static Allocation**: Device plugins advertise extended resources so the scheduler can filter nodes, but actual device assignment happens in the kubelet during container creation (device plugin `Allocate` RPC) after the pod is scheduled - not as a pre-scheduling allocation step.

3. **No Resource Sharing**: Device plugins don't support controlled sharing or partitioning of resources across multiple containers or pods.

4. **Limited Filtering**: Device plugin selectors are basic and don't support the advanced filtering capabilities needed for heterogeneous SR-IOV environments (vendor ID, PCI address, NUMA node, PF name, driver type, etc.).

5. **No Cross-Driver Resource Matching**: Device plugins operate in isolation and cannot coordinate resource allocation across different device types. DRA enables matching properties between resources from different DRA drivers, allowing workloads to request optimally aligned hardware. For example, a pod can request both a GPU and a SR-IOV VF that share the same PCIe root complex (matched by `pcieRoot` attribute) to minimize latency and maximize data transfer efficiency—critical for high-performance computing, AI/ML workloads, and network-intensive applications.

6. **Kubernetes Evolution**: DRA is the future direction for device resource management in Kubernetes (stable in 1.34+), and the device plugin framework may eventually be deprecated.

The DRA driver for SR-IOV (`dra-driver-sriov`) provides:
- Opt-in device advertisement via `SriovResourcePolicy` CRDs (devices only advertised when matching a policy)
- Flexible attribute assignment via `DeviceAttributes` CRDs (decoupled from device selection)
- Native Kubernetes resource claim model with `ResourceClaimTemplate`
- Better integration with container runtimes via CDI (Container Device Interface) and NRI (Node Resource Interface)
- More flexible device configuration and allocation policies
- Support for both kernel networking and userspace (VFIO/DPDK) use cases

### Use Cases

1. **As a cluster administrator**, I want to use DRA for SR-IOV resource management to benefit from advanced filtering and resource modeling capabilities.

2. **As a platform engineer**, I want to define fine-grained SR-IOV resource pools based on hardware characteristics (vendor, device ID, NUMA node, PF name) using declarative CRDs.

3. **As a workload developer**, I want to request SR-IOV VFs using native Kubernetes ResourceClaims instead of device plugin resource requests:
   ```yaml
   resourceClaims:
   - name: vf
     resourceClaimTemplateName: sriov-vf-claim
   ```

4. **As a system administrator**, I want to migrate from device plugin to DRA.

5. **As an operator maintainer**, I want to support both device plugin and DRA modes in the same operator to accommodate different user requirements and migration timelines.

6. **As a cluster administrator**, I want to leverage DRA's improved scheduling and allocation capabilities for complex multi-VF workloads.

### Goals

- Add support for deploying and managing the DRA driver for SR-IOV as an alternative to the device plugin
- Introduce a feature flag in `SriovOperatorConfig` to enable/disable DRA mode
- Maintain backward compatibility with existing device plugin-based deployments
- Support both device plugin and DRA modes in the same operator (not simultaneously on the same cluster)
- Enable opt-in device advertisement via `SriovResourcePolicy` and attribute assignment via `DeviceAttributes` CRDs when in DRA mode
- Document migration path from device plugin to DRA
- Ensure the operator continues to manage SR-IOV configuration on nodes (VF creation, driver binding, etc.) regardless of the mode

### Non-Goals

- Running device plugin and DRA driver simultaneously on the same cluster (at least in the initial implementation)
- Automatic migration of existing device plugin configurations to DRA configurations
- Support for Kubernetes versions below 1.34 (first stable DRA release)
- Changes to the core SR-IOV configuration functionality (VF creation, driver binding, etc.)
- Implementing DRA driver features within the operator itself (we will use the existing `dra-driver-sriov` project)
- Deprecating or removing device plugin support in the near term
- Enforcing Kubernetes version checks in the operator (cluster admins must ensure K8s 1.34+ for DRA)

## Proposal

### DeviceClass Management Strategy

The operator will manage DeviceClass resources in two tiers:

**1. Basic DeviceClass (Phase 1 - Core):**
- Single DeviceClass named `sriovnetwork.k8snetworkplumbingwg.io`
- Created automatically when DRA feature gate is enabled
- Matches all devices from the DRA driver: `device.driver == 'sriovnetwork.k8snetworkplumbingwg.io'`
- Enables users to create ResourceClaims with CEL selectors for specific resources
- This is the equivalent of deploying the DRA driver's helm chart DeviceClass
- **Recommendation:** Always create this in Phase 1 as part of DRA driver deployment

**2. Per-ResourceName DeviceClasses (implemented when DRA is enabled):**
- One DeviceClass per unique `SriovNetworkNodePolicy.Spec.ResourceName`
- Each has `extendedResourceName` set to `ResourcePrefix/resourceName` (same as device plugin extended resource name)
- CEL selector matches devices with that resourceName attribute (set via DeviceAttributes)
- When the Kubernetes `DRAExtendedResource` feature gate is enabled on the cluster, pods can request these via `resources.limits`; otherwise users request via ResourceClaimTemplate and the basic DeviceClass
- For a `deviceType: vfio-pci` policy, the generated class also supplies a `VfConfig` with `driver: vfio-pci`. The driver uses this configuration to include `/dev/vfio/<group>` and `/dev/vfio/vfio` in the container's CDI specification. The `resourceFilters.drivers` selector alone does not configure container device access.
- Policies sharing a resource name must agree on VFIO versus netdevice mode, even when they select different nodes. Admission rejects mixed pools in DRA mode; reconciliation also rejects them if admission was bypassed. An omitted device type is treated as netdevice.

For example, `resourceName: dpdk_vfio` with resource prefix `openshift.io` generates:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: dpdk-vfio
spec:
  extendedResourceName: openshift.io/dpdk_vfio
  selectors:
  - cel:
      expression: >-
        device.driver == "sriovnetwork.k8snetworkplumbingwg.io" &&
        device.attributes["k8s.cni.cncf.io"].resourceName == "openshift.io/dpdk_vfio"
  config:
  - opaque:
      driver: sriovnetwork.k8snetworkplumbingwg.io
      parameters:
        apiVersion: sriovnetwork.k8snetworkplumbingwg.io/v1alpha1
        kind: VfConfig
        driver: vfio-pci
```

Pods request `openshift.io/dpdk_vfio` through container resource limits. Kubernetes
generates the ResourceClaim and includes the class configuration in its allocation
result. The operator reconciles the entire DeviceClass spec, so manual class edits
are not persistent. Workloads needing their own configuration can use an explicit
ResourceClaim or ResourceClaimTemplate with the basic DeviceClass instead.

### Workflow Description

#### Current Device Plugin Workflow

1. User creates `SriovNetworkNodePolicy` to configure SR-IOV devices
2. Operator configures nodes (creates VFs, binds drivers, etc.)
3. Operator deploys `sriov-network-device-plugin` DaemonSet
4. Device plugin discovers VFs and exposes them as node resources (e.g., `openshift.io/sriov-nic: 8`)
5. User creates `SriovNetwork` CR which generates `NetworkAttachmentDefinition`
6. User requests resources in Pod spec via `resources.limits`
7. Kubelet allocates devices via device plugin and launches Pod

#### Proposed DRA Workflow

1. User creates `SriovNetworkNodePolicy` to configure SR-IOV devices (unchanged)
2. Operator configures nodes (creates VFs, binds drivers, etc.) (unchanged)
3. **[NEW]** Operator deploys `dra-driver-sriov` DaemonSet instead of device plugin (when DRA mode enabled)
4. **[NEW]** Operator creates basic DeviceClass (`sriovnetwork.k8snetworkplumbingwg.io`)
5. **[NEW]** Operator automatically generates `SriovResourcePolicy` and `DeviceAttributes` CRs from `SriovNetworkNodePolicy` specs (one per node)
6. **[NEW]** DRA driver discovers VFs, advertises only devices matching policies, and applies attributes from `DeviceAttributes`
7. User creates `SriovNetwork` or `SriovIBNetwork` CR which generates `NetworkAttachmentDefinition` (unchanged)
8. **[NEW]** User creates `ResourceClaimTemplate` to request SR-IOV VFs using the basic DeviceClass
9. **[NEW]** User references ResourceClaim in Pod spec via `resourceClaims` field
10. **[NEW]** Kubelet works with DRA driver to allocate VFs and launches Pod

**Example ResourceClaimTemplate:**

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  namespace: vf-test9
  name: vf-test9
spec:
  spec:
    devices:
      requests:
      - name: vf
        exactly:
          deviceClassName: sriovnetwork.k8snetworkplumbingwg.io
          count: 1
```

**Example with Resource Filtering (using CEL selector):**

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  namespace: default
  name: intel-nic-claim
spec:
  spec:
    devices:
      requests:
      - name: vf
        exactly:
          deviceClassName: sriovnetwork.k8snetworkplumbingwg.io
          count: 2
          selectors:
          - cel:
              # Attribute value is buildExtendedResourceName(resourceName), e.g. ResourcePrefix/resourceName
              expression: device.attributes["k8s.cni.cncf.io"].resourceName == "openshift.io/intel_nic"
```

**Example Pod using ResourceClaim:**

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: sriov-workload
  namespace: vf-test9
  annotations:
    k8s.v1.cni.cncf.io/networks: sriov-network  # NetworkAttachmentDefinition from SriovNetwork CR
spec:
  containers:
  - name: app
    image: your-app:latest
    resources:
      claims:
      - name: vf
  resourceClaims:
  - name: vf
    resourceClaimTemplateName: vf-test9
```

#### Feature Flag Control

The DRA integration will be controlled via the existing `featureGates` mechanism in the `SriovOperatorConfig` CR:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    dynamicResourceAllocation: false  # default: false (use device plugin)
```

**Note**: DRA driver deployment configuration (image, CDI root, etc.) will be set via Helm values and environment variables on the operator deployment, not in the API. Interface naming is handled by Multus; the driver's standalone interface-prefix setting is not exposed.

When `featureGates.dynamicResourceAllocation: true`:
- Operator deploys DRA driver instead of device plugin
- Operator creates/manages DRA-related resources (DeviceClass, ServiceAccount, RBAC, etc.)
- Operator auto-generates `SriovResourcePolicy` and `DeviceAttributes` CRs from policies
- DRA driver only advertises devices that match a `SriovResourcePolicy` (opt-in model)
- Workloads request SR-IOV devices via **ResourceClaim** / **ResourceClaimTemplate** by default (not node extended resources). If the cluster enables the Kubernetes **`DRAExtendedResource`** feature gate, pods may instead use `resources.limits` with the same extended resource names as device-plugin mode (see [Extended Resource Allocation Support](#extended-resource-allocation-support-optional-enhancement)).

### API Extensions

#### SriovOperatorConfig Extension

**Feature Gate Addition**

Add a new feature gate constant to `pkg/consts/constants.go`:

```go
// DynamicResourceAllocationFeatureGate: enables DRA driver instead of device plugin
DynamicResourceAllocationFeatureGate = "dynamicResourceAllocation"
```

Add the feature gate to the default states in `pkg/featuregate/featuregate.go`:

```go
var DefaultFeatureStates = map[string]bool{
    consts.ParallelNicConfigFeatureGate:                false,
    consts.ResourceInjectorMatchConditionFeatureGate:   false,
    consts.MetricsExporterFeatureGate:                  false,
    consts.ManageSoftwareBridgesFeatureGate:            false,
    consts.BlockDevicePluginUntilConfiguredFeatureGate: true,
    consts.MellanoxFirmwareResetFeatureGate:            true,
    consts.DynamicResourceAllocationFeatureGate:        false, // NEW: default to device plugin mode
}
```

**DRA Driver Configuration via Helm/Environment Variables**

DRA driver configuration will be provided via Helm chart values and passed to the operator as environment variables, similar to how device plugin images and other deployment settings are currently configured. The operator will read these environment variables when deploying the DRA driver.

Helm values (as implemented):
```yaml
# values.yaml
images:
  sriovDevicePlugin: <existing device plugin image>
  sriovDraDriver: ghcr.io/k8snetworkplumbingwg/dra-driver-sriov:latest  # DRA driver image

draDriver:
  cdiRoot: "/var/run/cdi"
```

`draDriver.cdiRoot` controls only the host directory mounted into the DRA driver at its container path `/var/run/cdi`; it does not configure the container runtime's CDI search directories. If this value is changed, the configured host directory must also be included in the CDI search path of the container runtime on every DRA node.

The operator passes the DRA driver image and these settings into the DRA driver DaemonSet (image, env vars, etc.). Resource policies and DeviceAttributes are created in the operator's namespace (`sriov-network-operator`); no separate `resourcePolicyNamespace` value.

#### New CRDs: SriovResourcePolicy and DeviceAttributes

The DRA driver project uses a **two-CRD architecture** that decouples device selection from attribute assignment:

- **`SriovResourcePolicy`**: Selects which devices to advertise based on node + NIC selectors. Devices are only advertised when they match a policy (opt-in model). Optionally references `DeviceAttributes` via a label selector.
- **`DeviceAttributes`**: Defines arbitrary key/value attributes (including `resourceName`) applied to devices matched by policies. Linked to policies via labels.

Both CRDs from the DRA driver project will be included in the operator's Helm chart under `deployment/sriov-network-operator-chart/crds/`, similar to how the `NetworkAttachmentDefinition` CRD from Multus is currently included (`k8s.cni.cncf.io_networkattachmentdefinitions_crd.yaml`).

The manifest-based `make install` / `make deploy-setup` path also installs these two vendored DRA CRDs; `make uninstall` removes them.

The CRDs will be named:
- `sriovnetwork.k8snetworkplumbingwg.io_sriovresourcepolicies.yaml`
- `sriovnetwork.k8snetworkplumbingwg.io_deviceattributes.yaml`

`make manifests` copies these CRDs from the `dra-driver-sriov` module version selected by `go.mod` into the Helm chart and `test/util/crds/`. `make check-manifests` checks both copies for drift. Update the DRA dependency and run `make manifests` together when upgrading the driver API.

The deployed driver image must also be compatible with that CRD/API version. Select and test a corresponding image through `images.sriovDraDriver` or `SRIOV_DRA_DRIVER_IMAGE`, preferably using an immutable digest. The default image reference is floating; manifest generation does not pin or verify the deployed image.

**CRD Schema Summary:**

- **SriovResourcePolicy**: `spec.nodeSelector` (Kubernetes `corev1.NodeSelector`: `nodeSelectorTerms` / `matchExpressions`), `spec.configs[]` with `deviceAttributesSelector` (label selector) and `resourceFilters[]`. Each `ResourceFilter` supports: `vendors`, `devices`, `pfNames`, `pfPciAddresses`, `pciAddresses`, `drivers` (all string arrays), and `linkType` (`eth`/`ib`/`ethernet`/`infiniband`). Note: there is no `rootDevices` field; policy `rootDevices` (PF PCI addresses) are mapped to `pfPciAddresses` by the operator. The operator generates a single-term selector matching `kubernetes.io/hostname` `In` `[<kubernetes.io/hostname label value>]`. The generated `metadata.name` is the node name (and matches the `SriovNetworkNodeState` name).

- **DeviceAttributes**: `spec.attributes` is a map of qualified attribute name (e.g. `k8s.cni.cncf.io/resourceName`) to a value. Values use the Kubernetes resource API shape: exactly one of `bool`, `bools`, `int`, `ints`, `string`, `strings`, `version`, or `versions` (CRD schema keys are lowercase). List fields (`bools`, `ints`, `strings`, `versions`) require the Kubernetes `DRAListTypeAttributes` feature gate. The operator uses `resourceapi.QualifiedName` and `resourceapi.DeviceAttribute` from `k8s.io/api/resource/v1` when building these CRs (scalar fields only).

**Key behavioral change:** A device matched by a `SriovResourcePolicy` is advertised **regardless** of whether any `DeviceAttributes` are attached. Attributes are purely additive metadata. If no policies exist, zero devices are advertised.

**Automatic Generation from SriovNetworkNodePolicy**

The operator will automatically generate `SriovResourcePolicy` and `DeviceAttributes` CRs from `SriovNetworkNodePolicy` CRs when the DRA feature gate is enabled. This is analogous to how the operator currently generates the `device-plugin-config` ConfigMap from policies in device plugin mode.

Current behavior (device plugin mode):
- `SriovNetworkNodePolicyReconciler.syncDevicePluginConfigMap()` reads all policies
- For each node, it calls `renderDevicePluginConfigData()` to convert policy specs into device plugin resource configs
- Generates a ConfigMap with per-node JSON configuration

New behavior (DRA mode, implemented):
- `SriovNetworkNodePolicyReconciler.syncDeviceAttributes()` and `syncSriovResourcePolicies()` generate DRA CRs from policies
- For each unique `resourceName`, generate a `DeviceAttributes` CR with the extended resource name attribute and a pool label
- For each node, generate a `SriovResourcePolicy` CR with device selection filters and a `deviceAttributesSelector` pointing to the corresponding `DeviceAttributes`
- The cluster `default` SriovNetworkNodePolicy is excluded from DRA generation (same as device-plugin config)
- Map `SriovNetworkNodePolicy` fields to `SriovResourcePolicy` + `DeviceAttributes` fields:

| SriovNetworkNodePolicy | SriovResourcePolicy | DeviceAttributes |
|------------------------|---------------------|------------------|
| `Spec.ResourceName` | `Spec.Configs[].DeviceAttributesSelector` (label ref) | `Spec.Attributes["k8s.cni.cncf.io/resourceName"]` (extended resource name: `ResourcePrefix/resourceName`) |
| `Spec.NicSelector.Vendor` | `Spec.Configs[].ResourceFilters[].Vendors` | - |
| `Spec.NicSelector.DeviceID` | `Spec.Configs[].ResourceFilters[].Devices` (VF device ID) | - |
| `Spec.NicSelector.PfNames` | `Spec.Configs[].ResourceFilters[].PfNames` (whole PF names only; VF ranges unsupported) | - |
| `Spec.NicSelector.RootDevices` | `Spec.Configs[].ResourceFilters[].PfPciAddresses` | - |
| `Spec.NodeSelector` | Used by `p.Selected(node)` to decide if a config entry is included | - |
| Generated `Spec.NodeSelector` on CR | Always `kubernetes.io/hostname` `In` `[<kubernetes.io/hostname label value>]` (not a copy of policy nodeSelector) | - |
| Device driver (vfio-pci) | `Spec.Configs[].ResourceFilters[].Drivers` (`vfio-pci`) | - |
| `Spec.LinkType` (`eth`/`ETH`/`ib`/`IB`) | `Spec.Configs[].ResourceFilters[].LinkType` (`eth`/`ib`; omitted for vfio-pci; driver normalizes to `ethernet`/`infiniband`) | - |
| `Spec.NicSelector.NetFilter` | **Not mapped** (device plugin resolves to `pciAddresses`; DRA path has no equivalent) | - |

**Notes:**
- `resourceFilters.drivers` is set for `deviceType: vfio-pci`, but the DRA driver does not filter on driver yet.
- Each node's `SriovResourcePolicy` create/update waits for the current revisions of NIC-matching policies to be rendered into its `SriovNetworkNodeState` and for the daemon-owned `Ready` condition to confirm that NodeState generation. Readiness and rendering use the same NodeState snapshot. Pending nodes retain their existing policies without blocking ready nodes; obsolete policies are still deleted. Policies matching no discovered NIC have no revision stamp and do not block publication. DeviceAttributes may be synchronized before readiness.
- VF ranges in PF selectors (for example, `ens1f0#2-5`) are currently unsupported in DRA mode because the DRA driver matches PF names literally. Admission rejects ranged selectors when DRA is enabled. If a policy bypasses admission, reconciliation rejects it before writing node configuration and policy status reports `Ready=False` with reason `UnsupportedConfiguration`. Whole PF names, including alternative interface names, remain supported. Use device-plugin mode for VF-range partitioning until native range support is available in the DRA driver.
- Existing namespaced DRA objects are reconciled only when they carry the operator's generated-by label or a controller reference to the current `SriovOperatorConfig`. Name collisions with unmarked user objects return a conflict; label drift on owned objects is repaired.

Example auto-generated `DeviceAttributes` (attribute value is the extended resource name, e.g. `ResourcePrefix/resourceName` or plain `resourceName` if no prefix):
```yaml
apiVersion: sriovnetwork.k8snetworkplumbingwg.io/v1alpha1
kind: DeviceAttributes
metadata:
  name: intel-nic-attrs  # resourceNameToDeviceClassName(resourceName) + "-attrs"
  namespace: sriov-network-operator
  labels:
    sriovnetwork.openshift.io/generated-by: sriov-network-operator
    sriovnetwork.openshift.io/resource-pool: intel-nic
spec:
  attributes:
    k8s.cni.cncf.io/resourceName:
      string: "openshift.io/intel_nic"   # extended resource name (ResourcePrefix/resourceName)
```

Example auto-generated `SriovResourcePolicy` (policy `rootDevices` are mapped to `pfPciAddresses`; no `rootDevices` field in the CRD). The operator adds a controller `ownerReferences` entry for the default `SriovOperatorConfig` using that resource's live, cluster-specific UID; it is omitted here so the example remains valid:
```yaml
apiVersion: sriovnetwork.k8snetworkplumbingwg.io/v1alpha1
kind: SriovResourcePolicy
metadata:
  name: worker-1  # Same metadata.name as SriovNetworkNodeState / Node
  namespace: sriov-network-operator
  labels:
    sriovnetwork.openshift.io/generated-by: sriov-network-operator
    sriovnetwork.openshift.io/node: worker-1
spec:
  nodeSelector:
    nodeSelectorTerms:
    - matchExpressions:
      - key: kubernetes.io/hostname
        operator: In
        values:
        - worker-1
  configs:
  - deviceAttributesSelector:
      matchLabels:
        sriovnetwork.openshift.io/resource-pool: intel-nic
    resourceFilters:
    - vendors: ["8086"]       # From policy's nicSelector.vendor
      devices: ["154c"]       # From policy's nicSelector.deviceID (VF device ID)
      pfNames: ["ens1f0"]     # From policy's nicSelector.pfNames
      pfPciAddresses: ["0000:08:00.0"]  # From policy's nicSelector.rootDevices (PF PCI addresses)
  # If multiple policies apply to worker-1, each gets its own config entry
```

**Note**: The CRDs will always be installed as part of the operator's Helm chart, regardless of whether the DRA feature gate is enabled. However, the operator will only create/manage `SriovResourcePolicy` and `DeviceAttributes` CRs when the DRA feature gate is enabled.

#### ResourceClaimTemplate Examples

Users request SR-IOV VFs by creating `ResourceClaimTemplate` resources. Device requests use the `exactly` field (Kubernetes resource API v1). See also `deployment/sriov-network-operator-chart/examples/pod.yaml` for working examples.

**1. Basic Single VF Request:**

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  namespace: vf-test9
  name: vf-test9
spec:
  spec:
    devices:
      requests:
      - name: vf
        exactly:
          deviceClassName: sriovnetwork.k8snetworkplumbingwg.io
          count: 1
```

**2. Request Multiple VFs:**

Request multiple VFs for load balancing or high availability:
```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  namespace: default
  name: multi-vf
spec:
  spec:
    devices:
      requests:
      - name: vf
        exactly:
          deviceClassName: sriovnetwork.k8snetworkplumbingwg.io
          count: 4
```

**3. Using ResourceClaim in Pod:**

Reference the ResourceClaimTemplate in your Pod (container must also declare `resources.claims`):
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: sriov-workload
  namespace: vf-test9
  annotations:
    k8s.v1.cni.cncf.io/networks: sriov-network
spec:
  containers:
  - name: app
    image: your-app:latest
    resources:
      claims:
      - name: vf
  resourceClaims:
  - name: vf
    resourceClaimTemplateName: vf-test9
```

**4. Complete Example with Deployment:**

Using ResourceClaim with a Deployment:
```yaml
---
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  namespace: production
  name: web-server-nic
spec:
  spec:
    devices:
      requests:
      - name: vf
        exactly:
          deviceClassName: sriovnetwork.k8snetworkplumbingwg.io
          count: 1
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-server
  namespace: production
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web-server
  template:
    metadata:
      labels:
        app: web-server
      annotations:
        k8s.v1.cni.cncf.io/networks: sriov-network
    spec:
      containers:
      - name: nginx
        image: nginx:latest
        ports:
        - containerPort: 80
        resources:
          claims:
          - name: vf
      resourceClaims:
      - name: vf
        resourceClaimTemplateName: web-server-nic
```

**Note:** The DRA driver will only advertise VFs that match `SriovResourcePolicy` CRs created by the operator, and will apply attributes from corresponding `DeviceAttributes` CRs (including `resourceName`). Users don't need to specify CEL selectors for basic usage - the device selection and attribute assignment are handled by the `SriovResourcePolicy` and `DeviceAttributes` CRs generated from `SriovNetworkNodePolicy` resources.

#### Changes to SriovNetwork

`SriovNetwork` and `SriovIBNetwork` CRs will continue to work in DRA mode, generating `NetworkAttachmentDefinition` resources. However, the `resourceName` field will have different semantics:

- **Device Plugin Mode**: `resourceName` maps to device plugin resource (e.g., `openshift.io/sriov-nic`)
- **DRA Mode**: `resourceName` is mapped to a `DeviceAttributes` CR and linked via `SriovResourcePolicy` to create resource pools

**Not yet implemented:** validation or warnings when users try to use device plugin-style resource requests in pods when DRA mode is enabled. When per-resourceName DeviceClasses with `extendedResourceName` are present and the cluster `DRAExtendedResource` feature gate is enabled, pods may continue using `resources.limits` with the same extended resource names as device plugin mode.

### Implementation Details/Notes/Constraints

#### Component Architecture

```
┌──────────────────────────────────────────────────────────┐
│                  SRIOV Network Operator                  │
│                                                          │
│  ┌─────────────────────────────────────────────────────┐ │
│  │         SriovOperatorConfig Controller              │ │
│  │  (watches featureGates.dynamicResourceAllocation)   │ │
│  └─────────────────┬───────────────────────────────────┘ │
│                     │                                    │
│         ┌───────────┴──────────┐                         │
│         │                      │                         │
│         v                      v                         │
│  ┌──────────────┐      ┌──────────────────┐              │
│  │ Device Plugin│      │   DRA Driver     │              │
│  │  DaemonSet   │      │   DaemonSet      │              │
│  │   (legacy)   │      │     (new)        │              │
│  └──────────────┘      └──────────────────┘              │
│                                                          │
│  ┌─────────────────────────────────────────────────────┐ │
│  │    Node Configuration Controllers (unchanged)       │ │
│  │  - SriovNetworkNodePolicy Controller                │ │
│  │  - Config Daemon (VF creation, driver binding)      │ │
│  └─────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

#### Implementation Phases

Phases 1–2 are largely **complete** in the current codebase. Phase 3+ (integration/e2e, validation webhook, metrics) remain open.

**Phase 1: Core Infrastructure**
1. Add `DynamicResourceAllocationFeatureGate` constant to `pkg/consts/constants.go`
2. Add feature gate to default states in `pkg/featuregate/featuregate.go`
3. Vendor `SriovResourcePolicy` and `DeviceAttributes` CRDs from DRA driver project into Helm chart (`crds/` directory)
4. Import `SriovResourcePolicy` and `DeviceAttributes` types from DRA driver project into operator (for CR creation)
5. Add DRA driver configuration environment variables to operator deployment
6. Update Helm chart with DRA driver image and configuration values
7. Create DRA driver deployment manifests in `bindata/manifests/dra-driver/`:
   - DaemonSet for kubelet plugin
   - ServiceAccount
   - RBAC (ClusterRole, ClusterRoleBinding)
   - Basic DeviceClass (`sriovnetwork.k8snetworkplumbingwg.io`)
8. Add logic to operator to deploy DRA driver OR device plugin based on feature gate
9. *(Not yet implemented)* Add validation to prevent switching modes on a running cluster with active workloads

**Phase 2: Policy to SriovResourcePolicy/DeviceAttributes Conversion & DRA Driver Protection**
1. Add `syncSriovResourcePolicies()` and `syncDeviceAttributes()` methods to `SriovNetworkNodePolicyReconciler`
2. Implement `DeviceAttributes` generation logic:
   - For each unique `resourceName` across all policies, create a `DeviceAttributes` CR
   - The `DeviceAttributes` CR contains the `resourceName` as a string attribute
   - Label each `DeviceAttributes` CR with a pool identifier (e.g., `sriovnetwork.openshift.io/resource-pool: <name>`)
3. Implement per-node policy generation logic:
   - Create `SriovResourcePolicy` CRs based on policies (one per node with applicable policies)
   - Name policies with the same metadata.name as the Node / SriovNetworkNodeState
   - Each config entry uses `deviceAttributesSelector` to reference the corresponding `DeviceAttributes` by label
   - Update in place when policies change; delete obsolete per-node policies and orphaned `DeviceAttributes` when no longer needed
4. Implement `renderSriovResourcePolicyForNode()` to convert policy specs to `SriovResourcePolicy` spec for a specific node
5. Add init container to DRA driver DaemonSet (always present when DRA is enabled):
   - Run `sriov-network-config-daemon wait-for-config` (sets pod annotation; config daemon removes it when config is done)
   - Same pod-annotation mechanism as device plugin, but **not** gated by `BlockDevicePluginUntilConfiguredFeatureGate`
6. Implement DRA driver pod restart logic when configuration changes (config daemon restarts DRA driver pod before applying config whenever DRA mode is enabled).
7. Handle owner references and lifecycle management for auto-generated `SriovResourcePolicy` and `DeviceAttributes` CRs
8. Add labels to distinguish auto-generated vs user-created CRs (including node label for policies)
9. Ensure proper cleanup when policies are deleted or when switching back to device plugin mode

**Phase 3: Integration & Testing**
1. Ensure required upstream PRs are merged:
   - Multus CNI DRA support ([multus-cni#1455](https://github.com/k8snetworkplumbingwg/multus-cni/pull/1455))
   - DRA driver Multus integration ([dra-driver-sriov#7](https://github.com/k8snetworkplumbingwg/dra-driver-sriov/pull/7))
2. Add e2e tests for DRA mode
3. Test automatic `SriovResourcePolicy` and `DeviceAttributes` generation from policies
4. Test synchronization: verify DRA driver pod init waits for config daemon to remove wait-for-config annotation before starting
5. Test configuration updates: verify DRA driver pod restart → reconfig → init container waits → driver starts flow
6. Test that init container properly blocks until config daemon removes the pod annotation
7. Test interoperability with existing `SriovNetwork` CRs
8. Validate CNI integration works correctly with Multus DRA support
9. Test both kernel driver and VFIO modes
10. Test race condition scenarios (verify init container prevents premature scheduling)
11. Performance testing and optimization

**Phase 4: Documentation & Migration**
1. Document DRA mode configuration
2. Document automatic `SriovResourcePolicy` and `DeviceAttributes` generation behavior
3. Provide migration guide from device plugin to DRA
4. Add troubleshooting guide
5. Update quickstart guide with DRA examples

**Phase 5: Advanced Features**
1. Support for user-created `SriovResourcePolicy` and `DeviceAttributes` CRs that coexist with auto-generated ones
2. Integration with operator metrics and monitoring
3. Enhanced validation and defaulting for DRA configurations
4. Support for merging multiple policies into a single `SriovResourcePolicy` CR where appropriate
5. (Optional) Extended resource allocation support (see dedicated section below)

#### Key Implementation Considerations

**1. Feature Gate Usage**
- The `SriovOperatorConfigReconciler` controller will check the feature gate using its `FeatureGate` interface:
  ```go
  if r.FeatureGate.IsEnabled(consts.DynamicResourceAllocationFeatureGate) {
      // Deploy DRA driver DaemonSet and related resources
      err = r.syncDRADriver(ctx, defaultConfig)
  } else {
      // Deploy device plugin DaemonSet (default behavior)
      err = r.syncPluginDaemonObjs(ctx, defaultConfig)
  }
  ```
- The feature gate is initialized from the `SriovOperatorConfig.Spec.FeatureGates` map during controller reconciliation
- Changes to the feature gate in the `SriovOperatorConfig` CR trigger reconciliation in the operator config controller
- The controller will also need to clean up the previously deployed resources when switching modes (remove device plugin when enabling DRA, remove DRA driver when disabling DRA)

**2. DRA Driver Integration and DeviceClass Management**

The operator will deploy the DRA driver similarly to how it deploys the device plugin, including all necessary resources.

**Basic DRA Driver Deployment:**
- Container image reference (configured via Helm)
- DaemonSet for kubelet plugin
- ServiceAccount and RBAC permissions
- **Basic DeviceClass** for the driver

**Basic DeviceClass:**
The operator deploys the basic DeviceClass from `bindata/manifests/dra-driver/001-deviceclass.yaml`:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: sriovnetwork.k8snetworkplumbingwg.io
spec:
  selectors:
  - cel:
      expression: "device.driver == 'sriovnetwork.k8snetworkplumbingwg.io'"
```

This basic DeviceClass:
- Allows users to create ResourceClaims for any devices from the DRA driver
- Does NOT include extended resource mapping (per-resourceName DeviceClasses provide that)
- Matches all devices published by the DRA driver
- Is cluster-scoped; the operator does not set `ownerReferences` (cleanup on DRA disable is via bindata sync/delete)
- Is analogous to the device plugin DaemonSet and ConfigMap that are currently deployed

**Configuration:**
- The operator reads DRA driver configuration from environment variables (set via Helm)
- When deploying the DRA driver DaemonSet (`sriov-dra-driver`), the operator passes configuration as:
  - Container image reference
  - Command-line arguments to the DRA driver
  - Environment variables in the DRA driver container spec (including `CONFIGURATION_MODE=MULTUS`; NRI plugin settings are not used)
  - Volume mounts for CDI root and other paths
- DRA driver node placement uses `ConfigDaemonNodeSelector` only (unlike device plugin, it does not require `sriovnetwork.openshift.io/device-plugin=Enabled` node label)

**2. CRD Management**
- The `SriovResourcePolicy` and `DeviceAttributes` CRDs will be vendored from the DRA driver project into the operator's Helm chart
- Add the CRD files as:
  - `deployment/sriov-network-operator-chart/crds/sriovnetwork.k8snetworkplumbingwg.io_sriovresourcepolicies.yaml`
  - `deployment/sriov-network-operator-chart/crds/sriovnetwork.k8snetworkplumbingwg.io_deviceattributes.yaml`
- The CRDs will always be installed with the operator (like `NetworkAttachmentDefinition` from Multus)
- This approach is simpler than conditional CRD installation and allows users to prepare configurations before enabling DRA
- Consider adding a script or documentation for updating the vendored CRDs when the DRA driver project releases new versions

**3. Synchronization with Node Configuration**

This is a critical aspect of DRA integration. The DRA driver must not publish resources until the SR-IOV configuration is complete on each node.

**Problem Statement:**
- The SR-IOV config daemon applies configuration per-node (creates VFs, binds drivers, etc.)
- The DRA driver discovers VFs and advertises only those matching `SriovResourcePolicy` CRs, applying attributes from `DeviceAttributes` CRs
- If the DRA driver runs before configuration is applied, it may:
  - Publish incomplete or incorrect resource information
  - Not find any VFs because they haven't been created yet
  - Cause pods to be scheduled prematurely

**Solution: Reuse Device Plugin Protection Mechanism**

Instead of managing `SriovResourcePolicy`/`DeviceAttributes` CR lifecycle via node labels, reuse the existing proven pod-annotation mechanism from device plugin mode:

**Approach:**
1. **Create/update `SriovResourcePolicy` and `DeviceAttributes` CRs from policies** (policies per-node, attributes per-resourceName)
   - Specs are updated in place when policies change
   - Obsolete per-node policies and unused `DeviceAttributes` are deleted during reconcile
   - They define what resources should be discovered and how they should be attributed when the DRA driver runs

2. **Control DRA driver pod lifecycle** (pod annotation, always active in DRA mode)
   - When configuration changes, delete the DRA driver pod on affected nodes
   - Config daemon applies SR-IOV configuration
   - DRA driver pod restarts with init container that waits for the wait-for-config annotation to be removed
   - Only after the annotation is removed, the DRA driver discovers VFs and publishes resources

**Implementation Details:**

**Init Container in DRA Driver DaemonSet (same pattern as device plugin):**

The init container runs the config daemon's `wait-for-config` command (no kubectl, no node label). The init container sets a pod annotation (`sriovnetwork.openshift.io/device-plugin-wait-config`) on the DRA driver pod; the config daemon removes this annotation after SR-IOV configuration is applied on the node. Once the annotation is removed, the init container exits and the DRA driver starts.

```yaml
initContainers:
- name: sriov-dra-driver-init
  image: <config-daemon-image>
  command:
  - sriov-network-config-daemon
  - wait-for-config
  - --pod-name=$(POD_NAME)
  - --pod-namespace=$(POD_NAMESPACE)
  env:
  - name: POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  - name: POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
```

**Workflow:**

1. **Initial Setup:**
   - Operator creates `DeviceAttributes` CRs (one per unique `resourceName`) and `SriovResourcePolicy` CRs (one per node) based on policies
   - DRA driver pods start; init container sets pod annotation `sriovnetwork.openshift.io/device-plugin-wait-config` and blocks
   - Config daemon completes configuration and removes that annotation from the DRA driver pod
   - Init container exits, DRA driver starts and discovers VFs

2. **Configuration Update:**
   - Config daemon detects policy change and restarts DRA driver pod (or operator/daemon triggers restart)
   - New DRA driver pod starts with init container setting the wait-for-config annotation and blocking
   - Config daemon applies new configuration, then removes the annotation
   - Init container exits, DRA driver discovers VFs with updated configuration

3. **Signal:**
   - Pod annotation `sriovnetwork.openshift.io/device-plugin-wait-config`: init container sets it; config daemon removes it when configuration is done (same as device plugin). No node label or kubectl in the wait path.

**Implementation (as built):** see `buildDeviceAttributesCR()`, `buildPolicyConfig()`, and `renderSriovResourcePolicyForNode()` in `controllers/sriovnetworknodepolicy_dra.go`.

```go
// buildDeviceAttributesCR(name, resourceName) — one DeviceAttributes per unique resourceName
// buildPolicyConfig(p, nodeState) — one Config (DeviceAttributesSelector + ResourceFilter) per policy
// renderSriovResourcePolicyForNode(...) — one SriovResourcePolicy per node with applicable policies
//
// Per-node SriovResourcePolicy publication waits for the matching NodeState revision to be Ready.
// Pool label uses resourceNameToDeviceClassName(resourceName), not simple underscore replacement.
```

**Naming Convention:**
- One `SriovResourcePolicy` per node: `metadata.name` = node name (e.g. `worker-1`, same as `SriovNetworkNodeState`)
- One `DeviceAttributes` per unique `resourceName`: `<device-class-name>-attrs` (e.g., `intel-nic-attrs` where device-class-name is `resourceNameToDeviceClassName(resourceName)`)
- `SriovResourcePolicy` uses node selector to target exactly one node
- `DeviceAttributes` are linked to policies via label `sriovnetwork.openshift.io/resource-pool`

**State Machine:**

```
Initial State:
  1. Operator creates/updates NodeState and DeviceAttributes CRs
  2. DRA driver pod starts; init container sets wait-for-config annotation and blocks
  3. Config daemon applies configuration, removes the annotation, and reports
     Ready with the applied NodeState generation
  4. Operator publishes each node's SriovResourcePolicy when its revision is confirmed
  5. Once running, the DRA driver consumes policies and DeviceAttributes to publish
     matched devices; driver startup and policy reconciliation run independently

When Policy Changes:
  1. Operator updates DeviceAttributes and the NodeState policy revision
  2. Config daemon restarts DRA driver pod on affected nodes
  3. New pod's init container sets wait-for-config annotation and blocks
  4. Config daemon applies new SR-IOV configuration, removes the annotation,
     and reports Ready with the applied NodeState generation
  5. Operator updates each confirmed node's SriovResourcePolicy independently,
     retaining pending nodes' existing policies and deleting obsolete CRs
  6. Once running, the DRA driver consumes policy updates and discovers VFs
```

**Benefits:**
- DRA driver only sees VFs that are properly configured
- Prevents race conditions similar to device plugin issue
- Clean synchronization using Kubernetes-native state tracking
- Per-node granularity matches SR-IOV configuration model

**Comparison to Device Plugin Approach:**

| Aspect | Device Plugin | DRA (implemented) |
|--------|--------------|-------------------|
| Signal mechanism | Init sets pod annotation; daemon removes it | Same: init sets pod annotation; daemon removes it |
| Blocking | Init waits until annotation removed | Same |
| State source | Pod annotation `sriovnetwork.openshift.io/device-plugin-wait-config` | Same annotation |
| Config resources | ConfigMap (per-node data) | SriovResourcePolicy CRs (per-node) + DeviceAttributes CRs (per-resourceName) |
| Config lifecycle | ConfigMap always present | Specs updated in place; obsolete generated CRs deleted during reconcile |

**Key Benefits:**
- Reuses the same protection mechanism as the device plugin (pod annotation; config daemon removes it when config is done)
- `SriovResourcePolicy` and `DeviceAttributes` specs are updated in place on policy changes; obsolete CRs are deleted during reconcile
- Consistent behavior for both device plugin and DRA modes

**4. Mode Switching and Resource Management**

See the [DRA user guide](../advanced-features.md#6-dynamic-resource-allocation-dynamicresourceallocation) for the validation list, plain-English enablement flow, and commands to verify provider removal and readiness.
- The validation webhook rejects enabling or disabling DRA while any `SriovNetworkNodePolicy` exists in the operator namespace. This includes creating a DRA-enabled operator config, setting the gate to `false`, or removing the gate or the entire `featureGates` map when DRA was enabled. Updates that leave the mode unchanged remain allowed. This restriction does not verify that workloads have stopped or that policy cleanup and node reconfiguration have finished.
- Users must stop affected workloads and prevent their recreation before switching. When leaving DRA, release affected SR-IOV ResourceClaims while the DRA driver is still running. The webhook does not check these prerequisites.
- Provider selection comes directly from `spec.featureGates.dynamicResourceAllocation`; no accepted-provider state or switch progress is stored in status. Controllers and the config daemon use the configured gate. The webhook protects mode changes while policies exist; the config controller does not duplicate that protection when admission is bypassed.
- For the **First Release of the DRA Integration**, the operator deploys the selected provider and then removes the previous provider in the same reconciliation, without waiting for readiness. Users must wait for the old DaemonSet and pods to disappear and the new provider rollout to complete before creating policies. Policy admission does not block creation during a switch. A failed deployment or incomplete switch requires the user to resolve the failure before proceeding; the operator config's `Ready` condition alone is not a rollout-completion signal.
- A **Subsequent Release of the DRA Integration** is planned to support concurrent DRA and device-plugin operation and requires work in both providers. The first release implements only the manual switching procedure; see the [kubectl examples](../advanced-features.md#6-dynamic-resource-allocation-dynamicresourceallocation).
- Policies must be created in the operator namespace. Admission rejects creation and updates elsewhere, allows deletion, and controllers list policies only in the operator namespace.
- When switching to DRA mode:
  - Operator stops updating `device-plugin-config` ConfigMap and retains it for reuse when returning to device-plugin mode
  - Operator starts creating/updating `DeviceAttributes` CRs (per unique `resourceName`) and per-node `SriovResourcePolicy` CRs from policies (for nodes with a `SriovNetworkNodeState`; skips nodes where node state is not yet created)
  - Operator creates per-resourceName DeviceClasses with `extendedResourceName` via `syncExtendedResourceDeviceClasses()`
- When switching to device plugin mode:
  - Operator deletes all auto-generated `SriovResourcePolicy`, `DeviceAttributes`, and extended-resource `DeviceClass` CRs (label `sriovnetwork.openshift.io/generated-by: sriov-network-operator`)
  - Operator deploys device plugin DaemonSet and removes DRA driver DaemonSet
  - Operator resumes updating `device-plugin-config` ConfigMap

**4. Resource Naming**
- Extended resource name (device plugin and DRA attribute value): `<prefix>/<resourceName>` with underscores preserved (e.g. `openshift.io/intel_nic`)
- DeviceClass metadata.name: DNS-safe form via `resourceNameToDeviceClassName()` (e.g. `intel-nic`)
- DRA also supports ResourceClaim-based allocation via DeviceClass + optional CEL selectors

**5. CNI Integration**
- Both modes use SR-IOV CNI plugin (unchanged)
- `NetworkAttachmentDefinition` generation remains the same
- DRA driver integrates with CNI via the `VfConfig` parameters
- **Requires:** Multus CNI with DRA support ([multus-cni#1455](https://github.com/k8snetworkplumbingwg/multus-cni/pull/1455))
- **Requires:** DRA driver with Multus integration ([dra-driver-sriov#7](https://github.com/k8snetworkplumbingwg/dra-driver-sriov/pull/7))

**6. Metrics and Monitoring**
- The upstream DRA driver can expose a gRPC health service when `HEALTHCHECK_PORT` is set to a positive port
- **Operator deployment:** `HEALTHCHECK_PORT` stays `-1` (health service off) and the DaemonSet has no liveness/readiness probes. The driver pod runs with `hostNetwork: true` (same as the SR-IOV device plugin). Binding a fixed health port on the host network namespace risks port conflicts with other hostNetwork workloads on the node (config daemon, device plugin, node services); the operator does not coordinate per-node port assignment or enforce non-overlap at admission time
- *(Not yet implemented)* Operator DRA-specific metrics (CR counts, allocation events, etc.)

**7. RBAC Requirements**
- DRA driver requires RBAC permissions (similar to device plugin):
  - Access to `resourceslices.resource.k8s.io` API (for publishing devices)
  - Access to `resourceclaims.resource.k8s.io` API (for reading claims)
  - Read access to `deviceclasses.resource.k8s.io` API (to read the basic DeviceClass)
  - Read/List/Watch permissions for `SriovResourcePolicy` and `DeviceAttributes` CRDs
- Operator requires RBAC permissions:
  - Create/Update/Delete `DeviceClass` resources (for basic DeviceClass)
  - Create/Update/Delete `SriovResourcePolicy` CRs (for auto-generated policies)
  - Create/Update/Delete `DeviceAttributes` CRs (for auto-generated attributes)

**8. Node Configuration and Synchronization**
- Node configuration (VF creation, driver binding, etc.) remains unchanged
- The config daemon continues to work the same way in both modes:
  - Reads desired state from `SriovNetworkNodeState.Spec`
  - Applies SR-IOV configuration (creates VFs, binds drivers, etc.)
  - Removes the wait-for-config pod annotation when configuration is complete (so init container can exit)
- In DRA mode:
  - DRA driver DaemonSet includes init container (same pattern as device plugin)
  - Init container sets pod annotation and blocks until config daemon removes it
  - `SriovResourcePolicy` and `DeviceAttributes` CRs stay in place
  - When config changes, DRA driver pod is restarted; init container blocks until config complete
- **Advantage:** Reuses the same synchronization mechanism as the device plugin (pod annotation)

#### Dependencies

- Kubernetes 1.34+ (stable DRA support; cluster admin prerequisite — not enforced by operator)
- Operator builds against `k8s.io/api` v0.36.x; validate against your cluster version
- `dra-driver-sriov` container image (operator `go.mod` pins a pseudo-version; Helm default is `:latest` — pin both for production)
- Container runtime with CDI support (containerd, CRI-O)
- NRI is not required in MULTUS mode; Multus handles network configuration

**Required Upstream Changes for Multus Integration:**

1. **Multus CNI - DRA Support**
   - PR: [k8snetworkplumbingwg/multus-cni#1455](https://github.com/k8snetworkplumbingwg/multus-cni/pull/1455)
   - Description: Adds DRA support to Multus CNI for integration with DRA-allocated network devices
   - Status: Required for proper CNI integration with DRA driver

2. **DRA Driver SR-IOV - Multus Integration**
   - PR: [k8snetworkplumbingwg/dra-driver-sriov#7](https://github.com/k8snetworkplumbingwg/dra-driver-sriov/pull/7)
   - Description: Enhances DRA driver to work seamlessly with Multus CNI
   - Status: Required for NetworkAttachmentDefinition integration

#### Constraints

1. **No Mixed Mode**: Concurrent device allocation by DRA and device plugin is unsupported in this release. Their DaemonSets can briefly coexist during a switch, while all SR-IOV workloads and policies must remain removed
2. **Kubernetes Version**: DRA mode requires Kubernetes 1.34 or later (cluster admin prerequisite; not enforced by the operator)
3. **Runtime Requirements**: DRA driver requires CDI support; NRI is not required in MULTUS mode
4. **Migration Complexity**: Migrating existing workloads from device plugin to DRA requires pod recreation
5. **Configuration Mapping**: Not all device plugin configurations map 1:1 to DRA configurations:
   - `NicSelector.NetFilter` is not mapped to DRA `resourceFilters`. The webhook rejects creating or updating a policy with `netFilter` while DRA is enabled. Enabling DRA requires removing all policies in the operator namespace first, including those using `netFilter`. If admission validation is bypassed, the controller rejects `netFilter` policies before writing NodeState, and aggregate status reports `UnsupportedConfiguration` for the affected policies.
   - `resourceFilters.drivers` is emitted for `deviceType: vfio-pci`, but `dra-driver-sriov` does not filter on driver yet (TODO in driver)
   - The cluster `default` SriovNetworkNodePolicy is excluded from DRA generation

### Upgrade & Downgrade Considerations

#### Upgrade Scenarios

**Upgrading Operator (Device Plugin Mode → Device Plugin Mode)**
- Before upgrading an existing release with Helm, apply the updated CRD definitions in place. For releases that will enable DRA, apply both driver CRDs from `deployment/sriov-network-operator-chart/crds/`
- Do not delete and recreate existing CRDs; in-place updates preserve existing custom resources
- Device plugin continues to work as before

**Enabling DRA on Existing Cluster**
1. Ensure Kubernetes version is 1.34+
2. Stop affected SR-IOV workloads and prevent their controllers from recreating them during the migration. Wait for the pods to terminate
3. Save and remove all `SriovNetworkNodePolicy` objects in the operator namespace, and wait for policy cleanup and node reconfiguration to complete
4. Enable the feature gate in `SriovOperatorConfig`:
   ```yaml
   spec:
     featureGates:
       dynamicResourceAllocation: true
   ```
5. Wait for the device-plugin DaemonSet **and pods** to be removed and for the DRA driver DaemonSet rollout to complete, with Ready pods on all managed nodes. Use the [kubectl examples](../advanced-features.md#6-dynamic-resource-allocation-dynamicresourceallocation). The operator installs DRA before removing the device plugin but does not wait for rollout; users must complete these checks before reapplying policies.
6. Reapply DRA-compatible policies. The operator auto-generates `SriovResourcePolicy` and `DeviceAttributes` CRs (CRDs are already installed). Wait for node configuration and device advertisement to complete
7. Recreate workloads using ResourceClaims instead of device plugin resources

**Upgrading Operator (DRA Mode → DRA Mode)**
- DRA driver image may be updated
- Existing ResourceClaims continue to work
- `SriovResourcePolicy` and `DeviceAttributes` CRs are preserved

#### Downgrade Scenarios

##### Disabling DRA on Existing Cluster
1. Stop affected SR-IOV workloads using DRA and prevent their controllers from recreating them during the migration. Wait for the pods to terminate
2. Release the affected SR-IOV ResourceClaims and wait for their cleanup while the DRA driver is still running
3. Save and remove all `SriovNetworkNodePolicy` objects in the operator namespace. Wait for policy cleanup and node reconfiguration to complete; an empty policy list alone does not prove completion
4. Disable the feature gate in `SriovOperatorConfig` (removing the gate also disables it):
   ```yaml
   spec:
     featureGates:
       dynamicResourceAllocation: false
   ```
5. Wait for the DRA driver DaemonSet **and pods** to be removed and the device-plugin DaemonSet to be deployed with a completed rollout. With no policies, `DESIRED=0` and no device-plugin pods is expected; pods start on configured nodes after policies are reapplied. Use the [kubectl examples](../advanced-features.md#6-dynamic-resource-allocation-dynamicresourceallocation). The operator installs the device plugin before removing DRA without waiting for readiness. It resumes updating the retained `device-plugin-config`; `SriovResourcePolicy` and `DeviceAttributes` CRDs remain installed.
6. Reapply device-plugin-compatible policies and wait for node configuration and device advertisement to complete
7. Recreate workloads using device plugin resource requests

##### Rolling Back Operator Version
- Before rolling back, complete the full [Disabling DRA on Existing Cluster](#disabling-dra-on-existing-cluster) sequence: stop affected workloads, release their ResourceClaims, remove the DRA policies, and wait for the old provider to be removed and the replacement rollout to complete.
- If rolling back to a version that doesn't support the DRA feature gate:
  - Must first disable DRA mode (`dynamicResourceAllocation: false`)
  - Wait for the device plugin to be deployed
  - Then rollback operator version
- Validation should prevent rollback if DRA feature gate is enabled

#### Safety Measures

1. *(Not yet implemented)* **Validation Webhook**: Prevent feature gate switching if active workloads exist
2. *(Not yet implemented)* **Status Reporting**: Add status fields to `SriovOperatorConfig` to show current mode and any transition errors
3. **Feature Gate Consistency**: Ensure feature gate changes are properly synchronized across operator components
4. **Documentation**: Clear migration documentation with step-by-step instructions
5. **Backup Recommendations**: Recommend backing up ResourceClaim definitions before disabling DRA

### Extended Resource Allocation Support (Optional Enhancement)

#### Overview

Kubernetes 1.34 introduces an alpha feature called **Extended Resource Allocation by DRA** (controlled by the `DRAExtendedResource` feature gate). This feature allows `DeviceClass` resources to specify an `extendedResourceName`, enabling pods to request DRA-managed devices using traditional extended resource syntax instead of `ResourceClaim` objects.

**Key Benefits:**
- **Backward compatibility**: Existing pod specs using `resources.limits` can work with DRA without modification when the cluster `DRAExtendedResource` feature gate is enabled
- **Seamless migration**: Users can switch from device plugin to DRA cluster-wide without rewriting pod specifications
- **Consistent resource names**: Per-resourceName DeviceClasses use the same `extendedResourceName` values as device plugin mode (`ResourcePrefix/resourceName`)

**Cluster mode:** Device plugin and DRA driver are mutually exclusive at the cluster level (`dynamicResourceAllocation` feature gate). Per-node mixed mode (device plugin on some nodes, DRA on others) is not supported; see **Documentation and Migration** below and Open Questions item 2.

#### Kubernetes Feature Details

**Feature state (Kubernetes `DRAExtendedResource` feature gate):** disabled by default in v1.34–v1.35 (alpha), enabled by default in v1.36 (beta), and stable in v1.37. See the [Kubernetes feature gate reference](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/).

**Requirements:** Enable `DRAExtendedResource` on:
- kube-apiserver
- kube-scheduler
- kube-controller-manager
- kubelet

**Two Usage Patterns:**

1. **Explicit Extended Resource Name in DeviceClass:**
```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: intel-sriov-nic
spec:
  selectors:
  - cel:
      expression: device.driver == 'sriovnetwork.k8snetworkplumbingwg.io'
  extendedResourceName: openshift.io/intel_nic  # Maps to device plugin resource name
```

Pods can then request:
```yaml
resources:
  limits:
    openshift.io/intel_nic: 2  # Works like device plugin!
```

2. **Implicit Extended Resource with Special Prefix:**
```yaml
resources:
  limits:
    deviceclass.resource.kubernetes.io/intel-sriov-nic: 2  # Uses DeviceClass name
```

#### Proposed Integration with SR-IOV Network Operator

**Note:** This is separate from the basic DeviceClass created in Phase 1. The basic DeviceClass (`sriovnetwork.k8snetworkplumbingwg.io`) enables ResourceClaim usage. This section describes optional per-resourceName DeviceClasses with extended resource mapping.

**Automatic Per-ResourceName DeviceClass Generation**

When DRA is enabled, the operator automatically creates a `DeviceClass` for each unique `resourceName` in `SriovNetworkNodePolicy` CRs (in addition to the basic DeviceClass). Each has `extendedResourceName` set to `ResourcePrefix/resourceName` and a CEL selector that matches the resourceName attribute (which is set to the same extended name in `DeviceAttributes`). When the Kubernetes `DRAExtendedResource` feature gate is enabled on the cluster, pods can request these via `resources.limits`; otherwise users request via ResourceClaimTemplate.

**Mapping:**

| SriovNetworkNodePolicy | DeviceClass |
|------------------------|-------------|
| `Spec.ResourceName` | `metadata.name` and `spec.extendedResourceName` |
| Policy filtering criteria | `spec.selectors` CEL expressions |

**Example DeviceClass Generation:**

From this policy:
```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: policy-intel-nic
  namespace: sriov-network-operator
spec:
  resourceName: intel_nic
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  nicSelector:
    vendor: "8086"
    deviceID: "1572"
    pfNames: ["ens1f0"]
  numVfs: 8
```

Generate this DeviceClass (extended resource name uses `ResourcePrefix/resourceName`, e.g. `openshift.io/intel_nic`; CEL matches that same value):
```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: intel-nic  # resourceNameToDeviceClassName(resourceName)
  labels:
    sriovnetwork.openshift.io/generated-by: sriov-network-operator
    sriovnetwork.openshift.io/resource-name: intel_nic
spec:
  extendedResourceName: openshift.io/intel_nic   # buildExtendedResourceName(resourceName)
  selectors:
  - cel:
      expression: |
        device.driver == "sriovnetwork.k8snetworkplumbingwg.io" &&
        has(device.attributes["k8s.cni.cncf.io"].resourceName) &&
        device.attributes["k8s.cni.cncf.io"].resourceName == "openshift.io/intel_nic"
```

**Pod Compatibility:**

With this DeviceClass, existing pod specs continue to work:
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: sriov-pod
  # NetworkAttachmentDefinition still works the same way
  annotations:
    k8s.v1.cni.cncf.io/networks: sriov-network
spec:
  containers:
  - name: app
    image: myapp:latest
    resources:
      limits:
        openshift.io/intel_nic: 2  # Same as device plugin!
      requests:
        openshift.io/intel_nic: 2
```

#### Implementation Considerations

**1. Feature Gate Coordination**

No additional feature gate. When DRA is enabled (`dynamicResourceAllocation: true`), the operator creates per-resourceName DeviceClasses with `extendedResourceName`. Using them for pod `resources.limits` requires the Kubernetes `DRAExtendedResource` feature gate on the cluster.

**2. DeviceClass Lifecycle Management**

Two types of DeviceClass:

**A. Basic DeviceClass (Phase 1):**
- Name: `sriovnetwork.k8snetworkplumbingwg.io` (fixed)
- Created when DRA feature gate is enabled
- Deleted when DRA feature gate is disabled
- No extended resource mapping
- Allows users to create ResourceClaims for any SR-IOV devices

**B. Per-ResourceName DeviceClasses (implemented when DRA is enabled):**
- One `DeviceClass` per unique `resourceName` across all policies
- Created/updated when policies change (when DRA is enabled) via `syncExtendedResourceDeviceClasses()`
- Deleted when no policies use that `resourceName` anymore, or when DRA is disabled
- Includes extended resource mapping (`extendedResourceName`)
- Naming: convert `resourceName` to valid DeviceClass name (e.g., `intel_nic` → `intel-nic`)
- Cluster-scoped; identified and cleaned up by label `sriovnetwork.openshift.io/generated-by: sriov-network-operator` (no `ownerReferences`)

**3. CEL Expression Generation**

The operator generates CEL expressions that match the DRA driver's device attributes:
```go
func buildDeviceClassCEL(resourceName string) string {
    extendedName := buildExtendedResourceName(resourceName) // e.g. openshift.io/intel_nic
    return fmt.Sprintf(
        `device.driver == "sriovnetwork.k8snetworkplumbingwg.io" && ` +
        `has(device.attributes["k8s.cni.cncf.io"].resourceName) && ` +
        `device.attributes["k8s.cni.cncf.io"].resourceName == "%s"`,
        extendedName,
    )
}
```

**4. Conflict Prevention**

- **Implemented:** The admission webhook rejects `SriovNetworkNodePolicy` objects whose `resourceName` normalizes to the same DeviceClass name as another policy (`resourceNameToDeviceClassName()`), regardless of whether the `dynamicResourceAllocation` feature gate is enabled. In DRA mode, the policy controller rejects the entire policy set before changing node state or DRA objects if names collide, a resource name is empty, or device types conflict, including when the webhook is disabled. Admission and reconciliation share this validation.
- *(Not yet implemented)* Validate during mode transitions that `extendedResourceName` values do not conflict with lingering device plugin state before DRA is fully active
- *(Not yet implemented)* Add status field to indicate if DeviceClass creation succeeded or failed due to conflicts

**5. Documentation and Migration**

Document the migration path:
- **Extended resource compatibility**: When cluster `DRAExtendedResource` is enabled, existing pod specs using `resources.limits` may work without changes (same extended resource names as device plugin)
- **Cluster-wide mode switch**: DRA and device plugin are mutually exclusive at the cluster level; gradual per-node mixed mode is not supported
- **Rollback safety**: Stop affected workloads, release their SR-IOV ResourceClaims, remove policies, and wait for cleanup and node reconfiguration before disabling DRA. Reapply policies and recreate workloads using device plugin resource requests

#### Advantages

1. **Extended resource compatibility**: When cluster `DRAExtendedResource` is enabled, existing pod specs using `resources.limits` may work without changes
2. **Minimal pod spec changes**: ResourceClaim remains the primary DRA allocation path when extended resources are unavailable
3. **Cluster-wide adoption**: DRA replaces device plugin cluster-wide when the feature gate is enabled
4. **Future-Proof**: Prepares for eventual device plugin deprecation without breaking changes

#### Limitations and Caveats

1. **Alpha Feature**: Requires enabling `DRAExtendedResource` feature gate (K8s 1.34+)
2. **No Advanced Features**: Pods using extended resource syntax don't benefit from DRA's advanced features:
   - Cannot specify complex device selection criteria
   - Cannot use multiple device classes in one claim
   - Limited to simple count-based allocation
3. **Operator Complexity**: Requires managing `SriovResourcePolicy`, `DeviceAttributes`, and `DeviceClass` resources
4. **CEL Expression Maintenance**: Must ensure CEL expressions stay in sync with DRA driver behavior

#### Future Enhancements

1. **Automatic Detection**: Detect if `DRAExtendedResource` feature gate is enabled in cluster
2. **Hybrid Mode**: Support both extended resource and ResourceClaim patterns simultaneously
3. **Migration Tool**: Provide tool to convert pods from extended resource to ResourceClaim when users want advanced features
4. **Metrics**: Track usage of extended resource vs ResourceClaim patterns

#### Recommendation

Per-resourceName DeviceClasses with `extendedResourceName` are **implemented** when DRA is enabled. Using them for pod `resources.limits` still requires the Kubernetes `DRAExtendedResource` feature gate on the cluster. ResourceClaim via the basic DeviceClass remains the primary allocation path when that cluster gate is off.

---

### Test Plan

#### Implemented Unit Tests

Coverage exists in:
- `controllers/sriovnetworknodepolicy_dra_test.go` — `buildPolicyConfig` (including `linkType`), `buildDeviceAttributesCR`, `buildDeviceClassCEL`, `syncDeviceAttributes`, `syncSriovResourcePolicies`, `syncExtendedResourceDeviceClasses`, cleanup paths
- `controllers/helper_test.go` — `syncDRADriverObjs`, DRA driver node selector
- `pkg/daemon/daemon_test.go` — DRA driver pod annotation unblock/restart/wait logic

Key behaviors covered:
1. Feature gate default state (`dynamicResourceAllocation: false`)
2. `DeviceAttributes` CR created per unique `resourceName`
3. `SriovResourcePolicy` CR created per node with correct `deviceAttributesSelector`, filters (including `linkType`), and hostname nodeSelector
4. Extended-resource `DeviceClass` CR created per unique `resourceName`
5. Obsolete generated CRs deleted on policy/node changes and DRA disable
6. DRA driver init/wait uses pod annotation `sriovnetwork.openshift.io/device-plugin-wait-config` (not node labels)

#### Planned Integration / E2E Tests

Not yet implemented in `test/e2e/`:
1. Deploy operator with DRA enabled; verify CRDs, DRA driver DaemonSet (`sriov-dra-driver`), basic DeviceClass, and auto-generated policy/attribute CRs
2. Verify DRA driver discovers only policy-matched VFs and applies `DeviceAttributes`
3. Create ResourceClaim referencing basic DeviceClass; verify pod allocation and CNI connectivity
4. Test kernel driver and VFIO modes
5. Test policy update → DRA driver pod restart → init waits for annotation removal → driver rediscovery
6. Test feature gate switching (device plugin ↔ DRA), including cleanup of generated CRs
7. Test extended-resource pod requests when cluster `DRAExtendedResource` is enabled
8. *(Not yet implemented)* Mode-switch validation with active workloads
9. *(Not yet implemented)* Negative test: deploy DRA on Kubernetes < 1.34 (operator does not enforce this today)

#### Performance / Upgrade Tests (planned)

1. Measure allocation latency (DRA vs device plugin)
2. Test scalability with large numbers of VFs and ResourceClaims
3. Test operator upgrade with DRA mode enabled
4. Test enabling/disabling DRA on existing clusters

---

## Open Questions

1. **Feature Gate Naming**: Should the feature gate be named `dynamicResourceAllocation` or something shorter like `DRA`?
   - Current proposal: `dynamicResourceAllocation` (more descriptive, follows Kubernetes convention)
   - Alternative: `enableDRA` (shorter but less clear)
   - Need to align with Kubernetes feature gate naming conventions

2. **Should we support gradual migration** where some nodes use device plugin and others use DRA?
   - **Decision: No** — cluster-wide mutual exclusion is implemented today
   - Per-node mixed mode is not supported in the initial implementation

3. **How should we handle the resourceName semantic difference** between device plugin and DRA modes?
   - Document clearly that they serve different purposes
   - Consider adding a new field specifically for DRA resource names

4. ~~**Should SriovResourcePolicy/DeviceAttributes creation be automatic** based on SriovNetworkNodePolicy?~~
   - **Decision: Yes, auto-generate from policies** (similar to device-plugin-config ConfigMap)
   - Simplifies migration and provides consistent user experience
   - Users can still create additional manual `SriovResourcePolicy` and `DeviceAttributes` CRs for advanced scenarios

5. **What's the recommended image versioning strategy** for dra-driver-sriov?
   - Pin operator release to a tagged `dra-driver-sriov` image (override Helm `images.sriovDraDriver`)
   - Refresh vendored CRDs when bumping the driver dependency in `go.mod`

6. **Should we add a migration tool** to convert device plugin configurations to DRA?
   - Nice to have but not essential for initial implementation
   - Document manual migration process first

7. **Feature Gate Stability**: Should the feature gate start as alpha, beta, or go directly to stable?
   - Recommendation: Start as alpha (disabled by default) for initial releases
   - Move to beta (consider enabling by default) after proving stability
   - Eventually move to stable and deprecate device plugin mode

8. **Extended Resource Allocation**: Should we implement extended resource allocation support in the initial release?
   - **Decision: Implemented in operator** — per-resourceName DeviceClasses with `extendedResourceName` are synced when DRA is enabled
   - **Cluster dependency**: Pod `resources.limits` usage still requires Kubernetes `DRAExtendedResource` feature gate on apiserver/scheduler/kubelet
   - ResourceClaim via basic DeviceClass remains the fallback when cluster extended-resource allocation is unavailable
