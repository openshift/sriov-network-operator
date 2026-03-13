# SriovNetworkNodePolicy API Reference

The SriovNetworkNodePolicy CRD is the key component of the SR-IOV network operator. This custom resource instructs the operator to:

1. Render the spec of SriovNetworkNodeState CR for selected nodes to configure SR-IOV interfaces
2. Deploy SR-IOV CNI plugin and device plugin on selected nodes  
3. Generate the configuration of SR-IOV device plugin

**NOTE:** In virtual deployments, the VF interface is read-only and some fields have different behavior.

## Basic SriovNetworkNodePolicy Example

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

This example configures Intel XL710 NICs (vendor 8086, device 1583) on nodes labeled with `network-sriov.capable=true`, creating 4 VFs each with vfio-pci driver and MTU 1500.

## SriovNetworkNodePolicy Spec Fields

### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `nodeSelector` | map[string]string | Kubernetes node selector to target specific nodes |
| `resourceName` | string | Name for the device plugin resource pool |

### NIC Selection

| Field | Type | Description |
|-------|------|-------------|
| `nicSelector.vendor` | string | PCI vendor ID (e.g., "8086" for Intel) |
| `nicSelector.deviceID` | string | PCI device ID |
| `nicSelector.pfName` | []string | Physical function names (e.g., ["eno1", "eno2"]) |
| `nicSelector.rootDevices` | []string | PCI addresses (e.g., ["0000:86:00.0"]) |
| `nicSelector.netFilter` | string | Network interface name filter |

### VF Configuration

| Field | Type | Description | Virtual Deployment Notes |
|-------|------|-------------|--------------------------|
| `numVfs` | integer | Number of Virtual Functions to create | No effect (always 1 VF) |
| `deviceType` | string | Driver to bind VFs ("netdevice", "vfio-pci") | Depends on underlying device capabilities |
| `mtu` | integer | MTU size for VFs | Cannot be changed (set by platform) |

### Advanced Configuration

| Field | Type | Description |
|-------|------|-------------|
| `priority` | integer | Policy priority (0 is highest) for conflict resolution |
| `isRdma` | boolean | Enable RDMA capabilities |
| `needVhostNet` | boolean | Enable vhost-net for virtualized workloads |
| `eSwitchMode` | string | Set eSwitch mode ("legacy", "switchdev") |
| `externallyManaged` | boolean | Skip VF creation (user manages VFs) |

### Link Configuration

| Field | Type | Description |
|-------|------|-------------|
| `linkType` | string | Link type ("eth", "ETH", "ib", "IB") |
| `spoofChk` | string | Spoof checking ("on", "off") |
| `trust` | string | VF trust mode ("on", "off") |
| `linkState` | string | VF link state ("auto", "enable", "disable") |
| `maxTxRate` | integer | Maximum transmit rate (Mbps) |
| `minTxRate` | integer | Minimum transmit rate (Mbps) |

## Virtual Deployment Considerations

In virtual environments (VMs):

- **MTU**: Set by the underlying virtualization platform, cannot be changed
- **numVfs**: Has no effect as there is always 1 VF per policy  
- **deviceType**: Depends on whether the device supports native-bifurcating drivers:
  - Mellanox devices: Use `netdevice` (default) for native-bifurcating support
  - Intel devices: Use `vfio-pci` for non-bifurcating devices

```yaml
# Example for virtual deployment with Intel NIC
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: vm-policy
spec:
  deviceType: vfio-pci  # Required for Intel in VMs
  nicSelector:
    rootDevices: ["0000:00:05.0"]  # VF PCI address
  nodeSelector:
    kubernetes.io/hostname: "vm-worker-1"
  numVfs: 1  # Ignored in VMs
  resourceName: intel-vf
```

## Multiple Policies and Priority

When multiple SriovNetworkNodePolicy CRs target the same Physical Function, the `priority` field (0 is highest priority) resolves conflicts.

### Policy Processing Order
1. **Priority** (lowest number first)
2. **Name** (alphabetical order)

### Policy Merging Rules
- Policies with same **priority** are merged if they don't overlap
- Policies with **non-overlapping VF groups** (using #-notation) are merged
- **Overlapping policies**: Only the highest priority policy applies
- **Same priority + overlapping**: Last processed policy wins

### VF Group Notation

Use `#` notation to specify VF ranges:

```yaml
spec:
  nicSelector:
    pfName: ["eno1#0-3"]  # VFs 0, 1, 2, 3
  numVfs: 8
  resourceName: group1
---
spec:
  nicSelector:
    pfName: ["eno1#4-7"]  # VFs 4, 5, 6, 7  
  numVfs: 8
  resourceName: group2
```

## Externally Managed Virtual Functions

Set `externallyManaged: true` when you want to create VFs outside the operator:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: external-vfs
spec:
  externallyManaged: true
  deviceType: vfio-pci
  nicSelector:
    pfName: ["eno1"]
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 4
  resourceName: external-intelnics
```

### Externally Managed Behavior
- **Operator skips**: VF creation/deletion
- **Operator handles**: Driver binding and device plugin configuration
- **User responsibility**: Create VFs before applying policy
- **Policy removal**: VFs are NOT removed

### Use Cases
- VFs needed for host networking (storage, management)
- VFs must exist at boot time
- Integration with other VF management tools

### Creating VFs Externally

Example using systemd service:
```bash
# /etc/systemd/system/create-sriov-vfs.service
[Unit]
Description=Create SR-IOV VFs
Before=kubelet.service

[Service]
Type=oneshot
ExecStart=/bin/bash -c 'echo 4 > /sys/class/net/eno1/device/sriov_numvfs'
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
```

## RDMA Configuration

For RDMA workloads, set `isRdma: true` and ensure proper RDMA mode configuration:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: rdma-policy
spec:
  deviceType: netdevice
  isRdma: true
  nicSelector:
    pfName: ["eno1"]
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 4
  priority: 90
  resourceName: rdma_exclusive_device
```

See [RDMA Configuration Guide](rdma-configuration.md) for complete setup.

## Switchdev Mode

For OVS hardware offload, configure NICs in switchdev mode:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: switchdev-policy
spec:
  deviceType: netdevice
  eSwitchMode: switchdev
  nicSelector:
    pfName: ["eno1"]
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 4
  resourceName: switchdev-nics
```

## Troubleshooting

### Check Policy Status
```bash
kubectl get sriovnetworknodepolicy -n sriov-network-operator
kubectl describe sriovnetworknodepolicy <policy-name> -n sriov-network-operator
```

### Verify Node State
```bash
kubectl get sriovnetworknodestate -n sriov-network-operator
kubectl describe sriovnetworknodestate <node-name> -n sriov-network-operator
```

### Common Issues

1. **Webhook Validation Failures**
   - VF range exceeds maxVfs capability
   - Invalid PCI addresses or device IDs
   - Missing required fields

2. **Policy Conflicts**  
   - Multiple policies targeting same PF with different configs
   - Check priority values and VF group overlaps

3. **Virtual Deployment Issues**
   - Wrong deviceType for VM environment
   - Attempting to change read-only properties (MTU, numVfs)

4. **External VF Management**
   - VFs not created before policy application
   - Incorrect numVfs value vs actual VFs created

### Policy Validation

The operator includes admission webhooks that validate policies:

```bash
# Check webhook logs
kubectl logs deployment/sriov-network-operator -n sriov-network-operator
kubectl logs deployment/sriov-network-operator-webhook -n sriov-network-operator
```

### Node-Level Troubleshooting

For issues with specific nodes, check the config daemon and device plugin logs:

```bash
# Check config daemon logs on specific node
kubectl logs daemonset/sriov-config-daemon -n sriov-network-operator --field-selector spec.nodeName=<node-name>

# Check device plugin logs on specific node  
kubectl logs daemonset/sriov-device-plugin -n sriov-network-operator --field-selector spec.nodeName=<node-name>

# Alternative: Get pod name first, then check logs
kubectl get pods -n sriov-network-operator -l app=sriov-config-daemon --field-selector spec.nodeName=<node-name>
kubectl logs <config-daemon-pod-name> -n sriov-network-operator

kubectl get pods -n sriov-network-operator -l app=sriov-device-plugin --field-selector spec.nodeName=<node-name>
kubectl logs <device-plugin-pod-name> -n sriov-network-operator
```