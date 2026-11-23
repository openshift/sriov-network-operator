# Advanced Features Configuration

This guide covers advanced features and configurations available in the SR-IOV Network Operator.

## Feature Gates

Feature gates enable or disable specific operator features. They are configured through the SriovOperatorConfig custom resource.

### Available Feature Gates

#### 1. Parallel NIC Configuration (`parallelNicConfig`)

**Description**: Allows configuration of NICs in parallel, reducing network setup time.

**Default**: Disabled

**Use Case**: Large clusters with many SR-IOV devices requiring faster deployment.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    parallelNicConfig: true
```

**Impact**:
- Faster node configuration updates
- Reduced maintenance windows
- Higher resource usage during configuration

#### 2. Resource Injector Match Condition (`resourceInjectorMatchCondition`)

**Description**: Switches webhook failure policy from "Ignore" to "Fail" using Kubernetes 1.30+ MatchConditions feature.

**Default**: Disabled

**Requirements**: Kubernetes 1.30+

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    resourceInjectorMatchCondition: true
```

**Benefits**:
- Improved webhook reliability
- Only targets pods with SR-IOV network annotations
- Prevents webhook interference with other pods

#### 3. Metrics Exporter (`metricsExporter`)

**Description**: Enables metrics collection and export on config-daemon nodes.

**Default**: Disabled

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    metricsExporter: true
```

**Exposed Metrics**:
- SR-IOV device utilization
- VF allocation status
- Configuration success/failure rates
- Hardware health indicators

#### 4. Manage Software Bridges (`manageSoftwareBridges`)

**Description**: Allows the operator to manage software bridges automatically.

**Default**: Disabled

**Use Case**: Environments requiring automated bridge management for complex topologies.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    manageSoftwareBridges: true
```

#### 5. Mellanox Firmware Reset (`mellanoxFirmwareReset`)

**Description**: Enables firmware reset via `mstfwreset` before system reboot for Mellanox devices.

**Default**: Disabled

**Use Case**: Environments with Mellanox devices requiring firmware reset during maintenance.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    mellanoxFirmwareReset: true
```

**Warning**: This feature may extend reboot times and should be tested thoroughly.

#### 6. Dynamic Resource Allocation (`dynamicResourceAllocation`)

**Description**: Enables the Dynamic Resource Allocation (DRA) driver instead of the traditional SR-IOV device plugin. When enabled, the operator deploys the DRA driver DaemonSet and creates SriovResourcePolicy and DeviceAttributes resources so SR-IOV VFs are advertised via Kubernetes **ResourceSlices** and **ResourceClaims** (the default DRA allocation path), not via node extended resources unless the Kubernetes **`DRAExtendedResource`** feature gate is enabled. It is disabled by default in Kubernetes 1.34–1.35 (alpha), enabled by default in 1.36 (beta), and stable (GA) in 1.37.

**Default**: Disabled (operator uses the SR-IOV device plugin)

**Use Case**: Clusters that want to use the DRA API for SR-IOV device allocation (e.g. ResourceClaimTemplate in pods) or that rely on DRA for scheduling and lifecycle.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    dynamicResourceAllocation: true
```

**Mode-switch restriction**: The webhook rejects enabling or disabling DRA while any `SriovNetworkNodePolicy` exists in the operator namespace. Removing the gate or the entire `featureGates` map also disables DRA. Stop affected SR-IOV workloads and prevent their recreation, save and remove those policies, and wait for policy cleanup and node reconfiguration before switching. When leaving DRA, also release the affected SR-IOV ResourceClaims while the DRA driver is still running.

**First Release of the DRA Integration**

Provider selection comes directly from `spec.featureGates.dynamicResourceAllocation`. The operator deploys the selected provider and then removes the previous provider without waiting for rollout. It does not persist provider mode or switch progress in status, and policy admission does not wait for the switch to finish. You must verify that the previous provider's DaemonSet and pods are gone and the selected provider has completed its rollout **before creating or reapplying node policies**. The operator config's `Ready` condition reports reconciliation, not completion of provider rollout. If a wait fails or times out, resolve the failure and repeat the checks before proceeding.

After completing workload shutdown, policy removal, and node cleanup, use the following commands. Set the namespace for your installation. Run each command in order and stop on any error.

To switch from device plugin to DRA:

```bash
SRIOV_NAMESPACE=sriov-network-operator
kubectl -n "$SRIOV_NAMESPACE" patch sriovoperatorconfig default --type=merge \
  -p '{"spec":{"featureGates":{"dynamicResourceAllocation":true}}}'
kubectl -n "$SRIOV_NAMESPACE" wait --for=delete daemonset/sriov-device-plugin --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" wait --for=delete pod -l app=sriov-device-plugin --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" wait --for=create daemonset/sriov-dra-driver --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" rollout status daemonset/sriov-dra-driver --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" get daemonset sriov-dra-driver
kubectl -n "$SRIOV_NAMESPACE" get pods -l app=sriov-dra-driver -o wide
```

Verify that DRA has a nonzero desired pod count covering the managed nodes and that all its pods are Ready. The DRA driver can become Ready before policies exist. Only then create DRA-compatible sriov node policies, wait for node configuration and device advertisement, and recreate workloads.

To switch from DRA to device plugin, first stop workloads, release their SR-IOV ResourceClaims while DRA is still running, remove policies, and wait for cleanup:

```bash
SRIOV_NAMESPACE=sriov-network-operator
kubectl -n "$SRIOV_NAMESPACE" patch sriovoperatorconfig default --type=merge \
  -p '{"spec":{"featureGates":{"dynamicResourceAllocation":false}}}'
kubectl -n "$SRIOV_NAMESPACE" wait --for=delete daemonset/sriov-dra-driver --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" wait --for=delete pod -l app=sriov-dra-driver --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" wait --for=create daemonset/sriov-device-plugin --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" rollout status daemonset/sriov-device-plugin --timeout=300s
kubectl -n "$SRIOV_NAMESPACE" get daemonset sriov-device-plugin
kubectl -n "$SRIOV_NAMESPACE" get pods -l app=sriov-device-plugin -o wide
```

With no node policies, the device-plugin DaemonSet normally has `DESIRED=0` and no pods because it selects only nodes with configured SR-IOV resources. In that case, a deployed DaemonSet with a completed rollout and no remaining DRA pods is the pre-policy checkpoint. Reapply device-plugin-compatible policies, wait for node configuration, and run `kubectl rollout status` again. Verify device-plugin pods are Ready on the policy-selected nodes and resources are advertised before restarting workloads.

These examples use [kubectl wait](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_wait/) and [kubectl rollout status](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_rollout/kubectl_rollout_status/). See [upgrade and downgrade considerations](design/DRA-integration.md#upgrade--downgrade-considerations) for the full migration sequence.

Keep the validation webhook enabled during switching. It checks policy presence when the gate changes; it does not verify workload termination, cleanup completion, or provider readiness. Bypassing the webhook also bypasses the policy-presence protection. See [Validations in DRA mode](#validations-in-dra-mode) for the full admission rules.

**Subsequent Release of the DRA Integration**

Concurrent DRA and device-plugin operation is planned for a subsequent release of the DRA integration and requires changes in both providers. The First Release of the DRA Integration supports one provider at a time and uses the manual switching procedure above.

**Validations in DRA mode**

The validation webhook checks the following before admitting configuration or policy changes:

- **Switching providers:** enabling or disabling `dynamicResourceAllocation` requires that no node policies exist in the operator namespace, including the deprecated `default` policy. Removing the gate also disables DRA. Unrelated config updates remain allowed.
- **Policy location and config:** node policies must be in the operator namespace, and the default operator config must exist. Policy deletion is allowed. The deprecated `default` policy in the operator namespace is exempt from policy-content validation.
- **Resource names:** `resourceName` must contain 1–63 characters, start and end with a letter or digit, and contain only letters, digits, hyphens, or underscores. Different names must not produce the same DeviceClass name after lowercasing and replacing underscores with hyphens; for example, `intel_nic` and `intel-nic` conflict even when their policies select different nodes.
- **Supported selectors:** DRA rejects `nicSelector.netFilter` and VF ranges in `nicSelector.pfNames`, such as `ens1f0#2-5`. Use whole PF names.
- **Shared resource compatibility:** policies sharing a `resourceName` cannot mix `netdevice` and `vfio-pci`, because they share one DeviceClass. Different vendors' netdevices may share a resource.
- **Existing SR-IOV policy rules:** normal NIC and node selection, supported hardware, VF count limits, overlapping PF/root-device selections, externally managed PF constraints, consistent `excludeTopology`, and RDMA, switchdev, vDPA, and bridge compatibility checks still apply. DRA does not bypass these validations.

The controller repeats resource-name, selector, and shared-resource compatibility checks before changing node configuration or publishing DRA resources, so bypassing admission does not make an invalid DRA policy usable. During reconciliation, it also protects objects owned by users and waits for the current node configuration to be applied before advertising its VFs. These checks apply to ordinary policy changes as well as initial setup.

The webhook does **not** check provider rollout, workload termination, ResourceClaim release, or completion of node cleanup, and it does not block policy creation while a switch is in progress. Cluster DRA API support, runtime CDI support, and Multus integration are installation prerequisites rather than admission checks.

**What happens when DRA is enabled**

1. After workloads and policies have been cleaned up, you enable `spec.featureGates.dynamicResourceAllocation`. The webhook checks that no policies remain.
2. The operator reads that gate directly, installs the DRA driver and its supporting resources, and requests removal of the device plugin. It stores no provider mode or switch progress in status and does not wait for rollout.
3. You wait for the device-plugin DaemonSet and pods to disappear and for the DRA driver to finish rolling out, using the commands above. With no policies, the config daemon lets the DRA driver start without configured VFs.
4. You create DRA-compatible node policies. The operator validates them and computes each node's desired SR-IOV configuration. The config daemon drains affected consumers when required, configures the VFs, and coordinates driver restart through the wait-for-config init container.
5. The operator creates `DeviceAttributes` for each resource pool and publishes per-node `SriovResourcePolicy` configuration only after that node has applied the current VF configuration. It creates DeviceClasses for selecting those resources. The DRA driver advertises available VFs through `ResourceSlices`.
6. After node configuration and device advertisement are ready, you create workloads using ResourceClaims or ResourceClaimTemplates with the base DeviceClass (`sriovnetwork.k8snetworkplumbingwg.io`). Per-resource DeviceClasses also support extended-resource requests when the cluster enables Kubernetes' separate `DRAExtendedResource` feature. Later policy edits repeat the configuration and publication checks; removed policies trigger draining, reconfiguration, and withdrawal of their devices.

**Draining native claim consumers**: During a partial node drain, the operator resolves pods' `spec.resourceClaims` and evicts pods with an allocation result belonging to the SR-IOV DRA driver, as well as pods requesting SR-IOV extended resources. Claims allocated only by other drivers do not trigger eviction. Claim lookup errors fail the drain. The operator waits for affected pods to terminate before allowing device reconfiguration. Existing DaemonSet and mirror-pod drain exclusions still apply.

**Interaction with `blockDevicePluginUntilConfigured`**: That gate controls only the traditional device plugin. In DRA mode, the driver always uses the wait-for-config init container, even when `blockDevicePluginUntilConfigured` is `false`. The config daemon restarts the DRA driver before applying configuration and removes its wait annotation afterward. On nodes with no configured interfaces, the daemon still clears the annotation so the driver can start.

If the Kubernetes **DRAExtendedResource** feature gate is enabled on the cluster, per-resourceName DeviceClasses can also be requested via pod `resources.limits` using their **`extendedResourceName`** (same values as device-plugin mode), in addition to ResourceClaimTemplate.

**Current limitation — VF ranges**: A policy with a ranged `spec.nicSelector.pfNames` entry (for example, `ens1f0#2-5`) is rejected by the operator in DRA mode before it writes `SriovNetworkNodeState` or publishes a `SriovResourcePolicy`. The same validation runs during reconciliation, including when admission validation has been bypassed. Use whole PF names such as `ens1f0` in DRA mode. Use device-plugin mode when partitioning a PF into VF ranges; DRA range support is deferred until the driver supports it natively.

**Requirements**: Kubernetes **1.34+** with DRA (`resource.k8s.io` APIs); **Multus CNI** with DRA integration; container runtime with **CDI** support (NRI is not required in MULTUS mode); **`dra-driver-sriov`** image configured (e.g. `images.sriovDraDriver` in Helm). The operator does not enforce cluster version or runtime capabilities. See [DRA integration design](design/DRA-integration.md#dependencies) for prerequisites, upstream Multus/driver changes, and constraints.

### Feature Gate Best Practices

1. **Test in Development**: Always test feature gates in non-production environments
2. **Gradual Rollout**: Enable features on a subset of nodes initially
3. **Monitor Impact**: Track metrics and logs after enabling features
4. **Document Changes**: Maintain records of enabled features and their purposes

## Parallel Node Operations

### SriovNetworkPoolConfig

The SriovNetworkPoolConfig enables parallel node operations and advanced pool management.

#### Basic Parallel Configuration

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: parallel-workers
  namespace: sriov-network-operator
spec:
  maxUnavailable: 3           # Allow 3 nodes to be updated simultaneously
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

#### Percentage-Based Configuration

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: percentage-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: "25%"       # Allow 25% of matching nodes
  nodeSelector:
    matchLabels:
      environment: "development"
```

#### Environment-Specific Pools

```yaml
# Production pool - conservative
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: production-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: 1
  nodeSelector:
    matchLabels:
      environment: "production"
      sriov-enabled: "true"
---
# Staging pool - more aggressive
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: staging-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: "50%"
  nodeSelector:
    matchLabels:
      environment: "staging"
      sriov-enabled: "true"
```

### Pool Membership Rules

- **Exclusive membership**: Each node can only belong to one pool
- **Conflict resolution**: Nodes matching multiple pools will not be drained
- **Default behavior**: Nodes not in any pool use `maxUnavailable: 1`

## Plugin Management

### Disabling Config Daemon Plugins

Disable specific plugins when their operation is not needed or conflicts with external tools.

#### Currently Supported Plugins

**Mellanox Plugin**: Handles Mellanox-specific firmware configuration.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  disablePlugins:
    - mellanox
```

**Use Cases**:
- Firmware pre-configured during node provisioning
- External firmware management tools
- Environments requiring custom firmware settings

## Externally Managed Virtual Functions

### Configuration

Use `externallyManaged: true` when VFs are created outside the operator:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: external-vfs-policy
  namespace: sriov-network-operator
spec:
  externallyManaged: true
  deviceType: vfio-pci
  nicSelector:
    pfName: ["ens1f0"]
  nodeSelector:
    kubernetes.io/hostname: "worker-1"
  numVfs: 8
  resourceName: external_vfs
```

### External VF Creation Methods

#### Using systemd Service

```bash
# /etc/systemd/system/sriov-vfs.service
[Unit]
Description=Create SR-IOV VFs
Before=kubelet.service
After=network.target

[Service]
Type=oneshot
ExecStart=/bin/bash -c 'echo 8 > /sys/class/net/ens1f0/device/sriov_numvfs'
ExecStop=/bin/bash -c 'echo 0 > /sys/class/net/ens1f0/device/sriov_numvfs'
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
```

#### Using Kubernetes-nmstate

```yaml
apiVersion: nmstate.io/v1
kind: NodeNetworkConfigurationPolicy
metadata:
  name: sriov-vfs
spec:
  nodeSelector:
    kubernetes.io/hostname: "worker-1"
  desiredState:
    interfaces:
    - name: ens1f0
      type: ethernet
      state: up
      sriov:
        total-vfs: 8
        vfs:
        - id: 0
          spoofchk: false
        - id: 1
          spoofchk: false
```

## Advanced Webhook Configuration

### Resource Injector Webhook

Configure the resource injector webhook for enhanced functionality:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    resourceInjectorMatchCondition: true
  webhookConfig:
    failurePolicy: "Fail"        # Strict mode
    timeoutSeconds: 30
    admissionReviewVersions: ["v1", "v1beta1"]
```

## Network Attachment Definition Management

### Advanced NAD Configuration

Control NetworkAttachmentDefinition generation:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: advanced-network
  namespace: sriov-network-operator
spec:
  resourceName: intel_sriov_netdevice
  networkNamespace: production
  capabilities: |
    {
      "ips": true,
      "mac": true
    }
  metaPluginsConfig: |
    {
      "type": "tuning",
      "capabilities": {
        "mac": true
      },
      "sysctl": {
        "net.core.somaxconn": "1024",
        "net.ipv4.tcp_congestion_control": "bbr"
      }
    },
    {
      "type": "bandwidth",
      "ingressRate": "100M",
      "egressRate": "100M"
    }
```

## Performance Optimization Features

### NUMA-Aware Scheduling

Configure for NUMA topology awareness:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: numa-optimized
  namespace: sriov-network-operator
spec:
  deviceType: netdevice
  nicSelector:
    pfName: ["ens1f0"]
  nodeSelector:
    numa-optimized: "true"
  numVfs: 8
  resourceName: numa_optimized_vfs
```

### CPU Isolation Integration

```yaml
# Pod with CPU isolation
apiVersion: v1
kind: Pod
metadata:
  name: isolated-sriov-pod
  annotations:
    k8s.v1.cni.cncf.io/networks: advanced-network
    cpu-load-balancing.crio.io: "disable"
    cpu-quota.crio.io: "disable"
spec:
  containers:
  - name: app
    image: performance-app:latest
    resources:
      requests:
        numa_optimized_vfs: "1"
        cpu: "4"
        memory: "8Gi"
      limits:
        numa_optimized_vfs: "1"
        cpu: "4"
        memory: "8Gi"
  nodeSelector:
    node.alpha.kubernetes.io/isolated: "true"
```

## Security Features

### RBAC Configuration

Fine-grained RBAC for SR-IOV resources:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: sriov-network-operator
  name: sriov-network-manager
rules:
- apiGroups: ["sriovnetwork.openshift.io"]
  resources: ["sriovnetworks"]
  verbs: ["get", "list", "create", "update", "patch"]
- apiGroups: ["sriovnetwork.openshift.io"]
  resources: ["sriovnetworknodepolicies"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: sriov-network-manager-binding
  namespace: sriov-network-operator
subjects:
- kind: User
  name: network-admin
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: sriov-network-manager
  apiGroup: rbac.authorization.k8s.io
```

## Monitoring and Observability

### Operator Metrics Configuration

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    metricsExporter: true
  metricsConfig:
    port: 8080
    path: "/metrics"
    interval: "30s"
```

### Custom Monitoring

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: sriov-operator-metrics
  namespace: sriov-network-operator
spec:
  selector:
    matchLabels:
      app: sriov-network-operator
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

## Migration and Upgrades

### Version Compatibility

Check feature gate compatibility:

```bash
# Check current operator version
kubectl get deployment sriov-network-operator -n sriov-network-operator -o jsonpath='{.spec.template.spec.containers[0].image}'

# Verify feature gate support
kubectl explain sriovoperatorconfig.spec.featureGates
```

### Safe Migration Process

1. **Backup configurations**:
```bash
kubectl get sriovoperatorconfig -o yaml > sriov-config-backup.yaml
kubectl get sriovnetworknodepolicy -A -o yaml > sriov-policies-backup.yaml
```

2. **Test feature gates**:
```bash
# Enable in test environment first
kubectl patch sriovoperatorconfig default -n sriov-network-operator --type='merge' -p='{"spec":{"featureGates":{"parallelNicConfig":true}}}'
```

3. **Monitor rollout**:
```bash
# Watch operator logs
kubectl logs deployment/sriov-network-operator -n sriov-network-operator -f

# Check node state
kubectl get sriovnetworknodestate -n sriov-network-operator
```

## Troubleshooting Advanced Features

### Feature Gate Issues

```bash
# Check operator config
kubectl get sriovoperatorconfig default -n sriov-network-operator -o yaml

# Verify feature gate application
kubectl logs deployment/sriov-network-operator -n sriov-network-operator | grep -i "feature"

# Check webhook configuration
kubectl get validatingwebhookconfiguration operator-webhook-config -o yaml
```

### Pool Configuration Problems

```bash
# Check pool membership
kubectl get nodes --show-labels | grep -E "(sriov|pool)"

# Verify pool selection
kubectl get sriovnetworkpoolconfig -n sriov-network-operator -o yaml

# Monitor pool operations
kubectl describe sriovnetworkpoolconfig <pool-name> -n sriov-network-operator
```

## Best Practices

### Feature Gate Management

1. **Document Decisions**: Maintain records of why features are enabled
2. **Environment Consistency**: Keep feature gates consistent across environments
3. **Regular Review**: Periodically review enabled features for relevance
4. **Testing Protocol**: Establish testing procedures for new features

### Performance Optimization

1. **Baseline Measurements**: Record performance before enabling features
2. **Incremental Changes**: Enable one feature at a time
3. **Resource Monitoring**: Track CPU, memory, and network impact
4. **Rollback Plan**: Prepare procedures to disable features if needed

### Security Considerations

1. **Principle of Least Privilege**: Enable only necessary features
2. **Regular Audits**: Review feature gate configurations periodically
3. **Access Control**: Restrict who can modify feature gates
4. **Monitoring**: Track configuration changes and their impact

## Next Steps

- [Pool Configuration](api/pool-config-api.md) - Detailed pool management
- [RDMA Configuration](rdma-configuration.md) - RDMA-specific features
- [Monitoring Guide](monitoring.md) - Comprehensive monitoring setup
- [Troubleshooting](troubleshooting.md) - Advanced troubleshooting techniques
