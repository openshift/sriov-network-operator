# SriovNetworkNodeState API Reference

The `SriovNetworkNodeState` Custom Resource represents the current SR-IOV configuration state on each node in the cluster. This resource is **read-only** for users and is automatically managed by the SR-IOV Network Operator.

## Resource Overview

- **API Version**: `sriovnetwork.openshift.io/v1`
- **Kind**: `SriovNetworkNodeState`
- **Scope**: Namespaced (typically `sriov-network-operator`)
- **Access**: Read-only for users

## Purpose

The `SriovNetworkNodeState` resource serves as:
- A status report of SR-IOV hardware capabilities on each node
- The current configuration state of SR-IOV interfaces
- A mechanism for the operator to track synchronization status
- A debugging tool for administrators to understand hardware state

## Resource Structure

### Spec

The spec section reflects the desired configuration that should be applied to the node:

```yaml
spec:
  interfaces:
    - pciAddress: "0000:03:00.0"
      numVfs: 4
      mtu: 1500
      name: "eno1"
      linkType: "eth"
      eSwitchMode: "legacy"
      vfGroups:
        - resourceName: "intelnics"
          deviceType: "netdevice"
          vfRange: "0-3"
          policyName: "policy-1"
          mtu: 1500
          isRdma: false
  bridges:
    ovs:
      - name: "br-ex"
        bridge:
          datapath: "netdev"
        uplinks:
          - pciAddress: "0000:03:00.0"
            name: "eno1"
  system:
    rdmaMode: "shared"
```

### Status

The status section shows the actual current state of SR-IOV hardware:

```yaml
status:
  interfaces:
    - name: "eno1"
      mac: "a4:bf:01:12:34:56"
      driver: "i40e"
      pciAddress: "0000:03:00.0"
      vendor: "8086"
      deviceID: "158b"
      mtu: 1500
      numVfs: 4
      linkSpeed: "25000"
      linkType: "eth"
      linkAdminState: "up"
      eSwitchMode: "legacy"
      totalvfs: 64
      Vfs:
        - name: "eno1v0"
          mac: "a6:bf:01:12:34:57"
          assigned: ""
          driver: "i40evf"
          pciAddress: "0000:03:02.0"
          vfID: 0
          vlan: 0
          mtu: 1500
  syncStatus: "Succeeded"
  lastSyncError: ""
```

## Field Reference

### Interface Configuration

| Field | Type | Description |
|-------|------|-------------|
| `pciAddress` | string | PCI address of the physical function (required) |
| `numVfs` | int | Number of virtual functions to create |
| `mtu` | int | Maximum transmission unit size |
| `name` | string | Interface name (e.g., "eno1") |
| `linkType` | string | Link type: "eth", "ib" |
| `eSwitchMode` | string | E-Switch mode: "legacy", "switchdev" |
| `externallyManaged` | bool | Whether interface is managed externally |
| `vfGroups` | []VfGroup | Virtual function group configurations |

### VF Group Configuration

| Field | Type | Description |
|-------|------|-------------|
| `resourceName` | string | Name of the device plugin resource |
| `deviceType` | string | Device type: "netdevice", "vfio-pci" |
| `vfRange` | string | Range of VFs (e.g., "0-3", "0,2,4") |
| `policyName` | string | Name of the policy that created this group |
| `mtu` | int | MTU for VFs in this group |
| `isRdma` | bool | Enable RDMA support |
| `vdpaType` | string | vDPA type if applicable |

### Virtual Function Status

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | VF interface name |
| `mac` | string | MAC address |
| `assigned` | string | Pod/container assignment status |
| `driver` | string | Currently loaded driver |
| `pciAddress` | string | PCI address of the VF |
| `vendor` | string | Vendor ID |
| `deviceID` | string | Device ID |
| `vlan` | int | VLAN tag |
| `mtu` | int | MTU size |
| `vfID` | int | Virtual function index |
| `vdpaType` | string | vDPA type |
| `representorName` | string | Representor interface name |
| `guid` | string | GUID for InfiniBand devices |

### System Configuration

| Field | Type | Description |
|-------|------|-------------|
| `rdmaMode` | string | RDMA subsystem mode: "shared", "exclusive" |

### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `syncStatus` | string | Synchronization status: "Succeeded", "Failed", "InProgress" |
| `lastSyncError` | string | Last error message if sync failed |

## Usage Examples

### Viewing Node States

```bash
# List all node states
kubectl get sriovnetworknodestate -n sriov-network-operator

# Get detailed information for a specific node
kubectl describe sriovnetworknodestate <node-name> -n sriov-network-operator

# View node state in YAML format
kubectl get sriovnetworknodestate <node-name> -n sriov-network-operator -o yaml
```

### Monitoring Sync Status

```bash
# Check sync status across all nodes
kubectl get sriovnetworknodestate -n sriov-network-operator \
  -o custom-columns="NODE:.metadata.name,SYNC:.status.syncStatus,ERROR:.status.lastSyncError"

# Watch for changes
kubectl get sriovnetworknodestate -n sriov-network-operator -w
```

### Troubleshooting Hardware Issues

```bash
# Check available SR-IOV interfaces
kubectl get sriovnetworknodestate <node-name> -n sriov-network-operator \
  -o jsonpath='{.status.interfaces[*].name}'

# View VF allocation
kubectl get sriovnetworknodestate <node-name> -n sriov-network-operator \
  -o jsonpath='{.status.interfaces[?(@.name=="eno1")].Vfs[*].assigned}'
```

## State Synchronization

The operator continuously reconciles the desired state (spec) with the actual hardware state (status):

### Common Status Values

#### Sync Status Values

- `Succeeded`: Configuration applied successfully
- `Failed`: Configuration failed to apply
- `InProgress`: Configuration is being applied
- `NotSelected`: Node not selected by any policy

#### Link Admin State Values

- `up`: Interface is administratively up
- `down`: Interface is administratively down
- `unknown`: State cannot be determined

#### E-Switch Mode Values

- `legacy`: Traditional SR-IOV mode
- `switchdev`: Switch device mode for hardware offload
- `unknown`: Mode cannot be determined

### Annotations

The operator uses several annotations for state tracking:

| Annotation | Purpose |
|------------|---------|
| `sriovnetwork.openshift.io/desired-state` | Hash of desired configuration |
| `sriovnetwork.openshift.io/current-state` | Hash of current configuration |
| `sriovnetwork.openshift.io/last-network-namespace` | Last seen network namespace |

## Troubleshooting

### Sync Status Issues

When `syncStatus` shows `Failed` or `InProgress` for extended periods:

```bash
# Check the operator logs for high-level issues
kubectl logs deployment/sriov-network-operator -n sriov-network-operator

# Check config daemon logs on the specific node
kubectl logs daemonset/sriov-config-daemon -n sriov-network-operator --field-selector spec.nodeName=<node-name>

# Alternative: Get pod name first, then check logs
kubectl get pods -n sriov-network-operator -l app=sriov-config-daemon --field-selector spec.nodeName=<node-name>
kubectl logs <config-daemon-pod-name> -n sriov-network-operator
```

### Common Troubleshooting Commands

```bash
# Check if node state exists for all nodes
kubectl get nodes -o name | xargs -I {} kubectl get sriovnetworknodestate {} -n sriov-network-operator --ignore-not-found

# Compare desired vs current state annotations
kubectl get sriovnetworknodestate <node-name> -n sriov-network-operator \
  -o jsonpath='{.metadata.annotations.sriovnetwork\.openshift\.io/desired-state}{"\n"}{.metadata.annotations.sriovnetwork\.openshift\.io/current-state}'

# Check for configuration drift
kubectl get sriovnetworknodestate <node-name> -n sriov-network-operator \
  -o jsonpath='{.status.lastSyncError}'
```

### Configuration Validation

```bash
# Verify node is selected by policies
kubectl get sriovnetworknodepolicy -n sriov-network-operator -o yaml | grep -A5 nodeSelector

# Check if config daemon is running on the node
kubectl get pods -n sriov-network-operator -l app=sriov-config-daemon --field-selector spec.nodeName=<node-name>

# Monitor real-time state changes
kubectl get sriovnetworknodestate <node-name> -n sriov-network-operator -w
```

## Best Practices

1. **Monitoring**: Set up alerts on `syncStatus` field changes
2. **Debugging**: Use `lastSyncError` for troubleshooting failed configurations
3. **Capacity Planning**: Monitor `totalvfs` vs `numVfs` for resource planning
4. **Hardware Validation**: Check vendor/deviceID combinations for compatibility

## Related Resources

- [SriovNetworkNodePolicy](node-policies-api.md) - Configures the desired state
- [SriovNetwork](sriov-network-api.md) - Consumes the configured resources
- [SriovNetworkPoolConfig](pool-config-api.md) - Groups nodes for configuration