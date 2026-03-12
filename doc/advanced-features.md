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