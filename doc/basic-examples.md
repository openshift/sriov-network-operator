# Basic SR-IOV Configuration Examples

This guide provides common deployment patterns and basic configuration examples for the SR-IOV Network Operator.

## Prerequisites

Before starting, ensure you have:

- SR-IOV-capable hardware (see [supported hardware](supported-hardware.md))
- Kubernetes/OpenShift cluster with SR-IOV Operator installed
- Multus CNI deployed
- Node Feature Discovery (NFD) for automatic hardware detection

## Basic Workflow

The typical SR-IOV configuration follows this pattern:

1. **Hardware Discovery**: Check available SR-IOV devices
2. **Node Policy**: Configure hardware with SriovNetworkNodePolicy
3. **Network Definition**: Create SriovNetwork for workloads
4. **Pod Configuration**: Use the network in pods

## Example 1: Basic Intel NIC Configuration

### Step 1: Discover Hardware

Check your nodes for SR-IOV-capable devices:

```bash
# List nodes with SR-IOV capability
kubectl get nodes -l feature.node.kubernetes.io/network-sriov.capable=true

# Check device details on a node
kubectl describe sriovnetworknodestate <node-name> -n sriov-network-operator
```

### Step 2: Create Node Policy

Configure Intel X710 NICs to create 4 VFs:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: intel-policy
  namespace: sriov-network-operator
spec:
  deviceType: netdevice
  nicSelector:
    vendor: "8086"      # Intel
    deviceID: "1572"    # X710 for 10GbE SFP+
    pfName: ["ens1f0"]  # Physical function name
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 4
  resourceName: intel_sriov_netdevice
  priority: 10
```

### Step 3: Create Network

Define a network using the configured VFs:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: intel-sriov-net
  namespace: default
spec:
  resourceName: intel_sriov_netdevice
  ipam: |
    {
      "type": "host-local",
      "subnet": "192.168.1.0/24",
      "rangeStart": "192.168.1.100",
      "rangeEnd": "192.168.1.200",
      "gateway": "192.168.1.1"
    }
  vlan: 100
```

### Step 4: Use in Pod

Deploy a pod using the SR-IOV network:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: intel-sriov-pod
  namespace: default
  annotations:
    k8s.v1.cni.cncf.io/networks: intel-sriov-net
spec:
  containers:
  - name: test-container
    image: centos:8
    command: ["sleep", "3600"]
    resources:
      requests:
        openshift.io/intel_sriov_netdevice: "1"
      limits:
        openshift.io/intel_sriov_netdevice: "1"
```

## Example 2: DPDK Workload with vfio-pci

For DPDK applications requiring userspace drivers:

### Node Policy for DPDK

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: dpdk-policy
  namespace: sriov-network-operator
spec:
  deviceType: vfio-pci     # Userspace driver for DPDK
  nicSelector:
    vendor: "8086"
    deviceID: "1583"       # XL710 for 40GbE
    pfName: ["ens2f0"]
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 2
  resourceName: intel_sriov_dpdk
  priority: 10
```

### DPDK Network

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: dpdk-sriov-net
  namespace: default
spec:
  resourceName: intel_sriov_dpdk
  # No IPAM - DPDK handles networking in userspace
```

### DPDK Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: dpdk-app
  namespace: default
  annotations:
    k8s.v1.cni.cncf.io/networks: dpdk-sriov-net
spec:
  containers:
  - name: dpdk-container
    image: dpdk-app:latest
    securityContext:
      capabilities:
        add: ["IPC_LOCK", "SYS_RESOURCE"]
    resources:
      requests:
        openshift.io/intel_sriov_dpdk: "1"
        hugepages-1Gi: "2Gi"
      limits:
        openshift.io/intel_sriov_dpdk: "1"
        hugepages-1Gi: "2Gi"
    volumeMounts:
    - name: hugepages
      mountPath: /mnt/huge
  volumes:
  - name: hugepages
    emptyDir:
      medium: HugePages
```

## Example 3: Mellanox ConnectX Configuration

### Mellanox Node Policy

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: mellanox-policy
  namespace: sriov-network-operator
spec:
  deviceType: netdevice
  nicSelector:
    vendor: "15b3"        # Mellanox
    deviceID: "1017"      # ConnectX-5
    pfName: ["enp3s0f0"]
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 8
  resourceName: mlx_sriov_netdevice
  isRdma: true           # Enable RDMA capabilities
  priority: 10
```

### Mellanox Network

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: mellanox-sriov-net
  namespace: default
spec:
  resourceName: mlx_sriov_netdevice
  ipam: |
    {
      "type": "host-local",
      "subnet": "10.10.10.0/24",
      "rangeStart": "10.10.10.10",
      "rangeEnd": "10.10.10.100"
    }
```

## Example 4: Multiple Networks per Pod

Configure a pod with multiple SR-IOV interfaces:

### Additional Network

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: management-net
  namespace: default
spec:
  resourceName: intel_sriov_netdevice
  ipam: |
    {
      "type": "host-local",
      "subnet": "172.16.0.0/24"
    }
  vlan: 200
```

### Multi-Network Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: multi-net-pod
  namespace: default
  annotations:
    k8s.v1.cni.cncf.io/networks: |
      [
        {"name": "intel-sriov-net"},
        {"name": "management-net"}
      ]
spec:
  containers:
  - name: app
    image: centos:8
    command: ["sleep", "3600"]
    resources:
      requests:
        openshift.io/intel_sriov_netdevice: "2"
      limits:
        openshift.io/intel_sriov_netdevice: "2"
```

## Example 5: Namespace-Specific Networks

Create networks in specific namespaces via the operator namespace:

### Network in Custom Namespace

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: app-specific-net
  namespace: sriov-network-operator
spec:
  resourceName: intel_sriov_netdevice
  networkNamespace: my-app-namespace    # Target namespace
  ipam: |
    {
      "type": "host-local",
      "subnet": "10.20.0.0/24"
    }
```

### Pod in Custom Namespace

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: app-pod
  namespace: my-app-namespace
  annotations:
    k8s.v1.cni.cncf.io/networks: app-specific-net
spec:
  containers:
  - name: app
    image: my-app:latest
    resources:
      requests:
        openshift.io/intel_sriov_netdevice: "1"
      limits:
        openshift.io/intel_sriov_netdevice: "1"
```

## Verification and Troubleshooting

### Check Configuration Status

```bash
# Verify node policies
kubectl get sriovnetworknodepolicy -n sriov-network-operator

# Check node state
kubectl get sriovnetworknodestate -n sriov-network-operator

# Verify networks
kubectl get sriovnetwork -n sriov-network-operator

# Check NetworkAttachmentDefinitions
kubectl get net-attach-def
```

### Test Pod Connectivity

```bash
# Get pod details
kubectl get pod <pod-name> -o wide

# Check interfaces in pod
kubectl exec <pod-name> -- ip link show

# Test connectivity
kubectl exec <pod-name> -- ping <target-ip>
```

### Common Issues

1. **No VFs created**: Check hardware compatibility and node policy
2. **Pod scheduling fails**: Verify resource requests match available VFs
3. **Network not attached**: Check NetworkAttachmentDefinition creation
4. **Performance issues**: Consider NUMA topology and CPU isolation

## Performance Optimization

### CPU Isolation

Isolate CPUs for SR-IOV workloads:

```yaml
# In pod spec
spec:
  containers:
  - name: app
    resources:
      requests:
        cpu: "2"
        memory: "2Gi"
        openshift.io/intel_sriov_netdevice: "1"
      limits:
        cpu: "2"
        memory: "2Gi"
        openshift.io/intel_sriov_netdevice: "1"
```

### NUMA Topology

Use Topology Manager for NUMA-aware scheduling:

```bash
# Enable Topology Manager on nodes
echo 'topologyManagerPolicy: "single-numa-node"' >> /var/lib/kubelet/config.yaml
systemctl restart kubelet
```

## Next Steps

- [RDMA Configuration](rdma-configuration.md) - Set up RDMA workloads
- [OVS Hardware Offload](ovs-hw-offload.md) - Configure hardware acceleration
- [Advanced Features](advanced-features.md) - Parallel operations and feature gates
- [Troubleshooting](troubleshooting.md) - Solve common issues