# SriovNetworkPoolConfig API Reference

The SriovNetworkPoolConfig CRD provides advanced configuration capabilities for managing groups of nodes in SR-IOV network environments. This custom resource allows cluster administrators to define node-level configuration policies that apply to specific sets of nodes selected by label selectors.

## Purpose and Benefits

The SriovNetworkPoolConfig serves multiple purposes:

1. **Node Pool Management**: Groups nodes into logical pools for coordinated configuration updates
2. **Parallel Operations**: Enables controlled parallel draining and configuration updates across multiple nodes
3. **RDMA Configuration**: Provides centralized RDMA mode configuration for selected nodes
4. **Maintenance Windows**: Controls how many nodes can be unavailable simultaneously during updates

## Key Configuration Fields

### Node Selection and Availability

| Field | Type | Description |
|-------|------|-------------|
| `nodeSelector` | metav1.LabelSelector | Specifies which nodes belong to this pool using Kubernetes label selectors |
| `maxUnavailable` | intstr.IntOrString | Controls how many nodes can be unavailable simultaneously (supports integer and percentage) |

### RDMA Configuration

| Field | Type | Description |
|-------|------|-------------|
| `rdmaMode` | string | Configure RDMA subsystem behavior ("shared" or "exclusive") |

## Basic Parallel Draining Configuration

Configure parallel draining to reduce maintenance time:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: worker-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: 2
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

This configuration allows draining a maximum of 2 worker nodes in parallel during SR-IOV configuration updates.

### Percentage-Based Availability

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: percentage-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: "25%"  # Allow 25% of nodes to be unavailable
  nodeSelector:
    matchLabels:
      sr-iov: "enabled"
```

## RDMA Mode Configuration

The `rdmaMode` field configures the RDMA (Remote Direct Memory Access) subsystem behavior for all nodes in the pool.

### RDMA Modes

- **shared**: Multiple processes can share RDMA resources simultaneously (default system behavior)
- **exclusive**: RDMA resources are exclusively assigned to a single process

### Exclusive RDMA Mode Example

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

**Important Notes:**
- Switching RDMA mode triggers a reboot of all nodes in the pool
- Reboot respects the `maxUnavailable` configuration
- RDMA mode affects all Mellanox SR-IOV devices on selected nodes

## Advanced Pool Configurations

### Environment-Specific Pools

```yaml
# Production pool - conservative updates
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: production-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: 1
  rdmaMode: exclusive
  nodeSelector:
    matchLabels:
      environment: "production"
      sriov-rdma: "true"
---
# Development pool - faster updates
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: development-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: "50%"
  nodeSelector:
    matchLabels:
      environment: "development"
```

### Complex Node Selection

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: gpu-sriov-pool
  namespace: sriov-network-operator
spec:
  maxUnavailable: 2
  rdmaMode: exclusive
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
      accelerator: "gpu"
    matchExpressions:
    - key: "sriov-hardware"
      operator: In
      values: ["mellanox", "intel"]
```

## Pool Membership Rules

### Important Constraints

1. **Exclusive Membership**: Each node can only belong to one pool
2. **Conflict Resolution**: If a node matches multiple pools, it will not be drained by any pool
3. **Default Behavior**: Nodes not in any pool have default `maxUnavailable: 1`

### Checking Pool Membership

```bash
# List all pools
kubectl get sriovnetworkpoolconfig -n sriov-network-operator

# Check which nodes match a pool
kubectl get nodes --selector="node-role.kubernetes.io/worker="

# Verify pool configuration
kubectl describe sriovnetworkpoolconfig <pool-name> -n sriov-network-operator
```

## Operations and Maintenance

### Monitoring Pool Status

```bash
# Check pool configuration
kubectl get sriovnetworkpoolconfig -n sriov-network-operator -o wide

# Monitor node drain status
kubectl get nodes -o custom-columns=NAME:.metadata.name,STATUS:.status.conditions[-1].type,SCHEDULABLE:.spec.unschedulable

# Check operator logs for pool operations
kubectl logs deployment/sriov-network-operator -n sriov-network-operator -f
```

### Updating Pool Configuration

When updating pool configurations:

1. **RDMA Mode Changes**: Trigger node reboots respecting `maxUnavailable`
2. **maxUnavailable Changes**: Take effect immediately for new operations
3. **nodeSelector Changes**: Nodes may move between pools (ensure no conflicts)

### Maintenance Windows

Plan maintenance considering pool configurations:

```yaml
# Maintenance-friendly configuration
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: maintenance-pool
spec:
  maxUnavailable: 3  # Allow more nodes during maintenance
  nodeSelector:
    matchLabels:
      maintenance-window: "enabled"
```

## Troubleshooting

### Common Issues

1. **Node Not Draining**
   - Check if node belongs to multiple pools
   - Verify nodeSelector matches intended nodes
   - Check for PodDisruptionBudgets blocking drain

2. **RDMA Mode Not Applied**
   - Verify Mellanox hardware is present
   - Check for conflicting RDMA configurations
   - Monitor operator logs for RDMA operations

3. **Unexpected Reboots**
   - RDMA mode changes trigger reboots
   - Check pool configuration changes
   - Review maintenance schedules

### Debugging Commands

```bash
# Check node labels
kubectl get nodes --show-labels

# Verify pool node selection
kubectl get nodes --selector="<pool-selector>"

# Check for conflicting pools
kubectl get sriovnetworkpoolconfig -n sriov-network-operator -o yaml

# Monitor operator behavior
kubectl logs deployment/sriov-network-operator -n sriov-network-operator --since=1h

# Check node drain status
kubectl describe node <node-name> | grep -A 10 "Taints"
```

```bash
# Check config daemon logs on specific node
kubectl logs daemonset/sriov-config-daemon -n sriov-network-operator --field-selector spec.nodeName=<node-name>

# Check device plugin logs on specific node  
kubectl logs daemonset/sriov-device-plugin -n sriov-network-operator --field-selector spec.nodeName=<node-name>
```

### Performance Considerations

- **Large Clusters**: Use percentage-based `maxUnavailable` for scalability
- **Network Dependencies**: Consider application network requirements when setting `maxUnavailable`
- **RDMA Workloads**: Plan for reboots when changing RDMA modes
- **Resource Constraints**: Higher `maxUnavailable` may impact cluster resource availability

## Best Practices

1. **Start Conservative**: Begin with low `maxUnavailable` values and increase as confidence grows
2. **Environment Separation**: Use different pools for production and development environments
3. **Monitoring**: Implement monitoring for pool operations and node availability
4. **Documentation**: Document pool configurations and their intended use cases
5. **Testing**: Test pool configurations in non-production environments first