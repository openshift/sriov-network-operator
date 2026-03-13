# RDMA Configuration Guide

This guide provides complete instructions for configuring RDMA (Remote Direct Memory Access) with the SR-IOV Network Operator.

## Overview

RDMA enables Pod exclusive access to RDMA resources and their associated hardware counters, providing high-performance, low-latency networking for demanding applications like HPC, AI/ML, and high-frequency trading.

## RDMA Architecture

RDMA with SR-IOV requires coordination between multiple components:

1. **Hardware**: RDMA-capable network interface (Mellanox ConnectX series)
2. **Host Configuration**: RDMA subsystem mode configuration
3. **SR-IOV Policy**: Hardware configuration with RDMA enablement
4. **Network Definition**: SR-IOV network with RDMA CNI plugin
5. **Application**: Pods with RDMA resource requests

## Prerequisites

### Hardware Requirements

- **Mellanox ConnectX-4 or newer** network adapters
- **InfiniBand or RoCE** (RDMA over Converged Ethernet) support
- **SR-IOV capabilities** enabled in hardware/BIOS

### Software Requirements

- Kubernetes 1.30+ or OpenShift 4.16+
- SR-IOV Network Operator installed
- RDMA drivers and libraries on nodes
- RDMA CNI plugin (included with operator)

### Verify RDMA Capability

Check if your hardware supports RDMA:

```bash
# Check for RDMA devices
kubectl exec -it <config-daemon-pod> -n sriov-network-operator -- rdma link show

# Verify RDMA subsystem
kubectl exec -it <config-daemon-pod> -n sriov-network-operator -- cat /sys/class/infiniband/*/device/sriov_numvfs
```

## RDMA Modes

The RDMA subsystem can operate in two modes:

### Shared Mode (Default)
- Multiple processes can share RDMA resources
- Lower isolation but higher resource utilization
- Suitable for non-critical workloads

### Exclusive Mode
- RDMA resources exclusively assigned to single process
- Higher isolation and guaranteed performance
- Required for performance-critical applications

## Complete RDMA Setup

### Step 1: Configure RDMA Mode

Set the RDMA mode using SriovNetworkPoolConfig:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkPoolConfig
metadata:
  name: rdma-workers
  namespace: sriov-network-operator
spec:
  maxUnavailable: 1          # Conservative for reboots
  rdmaMode: exclusive        # or "shared"
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
      rdma-capable: "true"     # Custom label for RDMA nodes
```

**Important**: Changing RDMA mode triggers a reboot of all matching nodes.

### Step 2: Create RDMA-Enabled Node Policy

Configure hardware with RDMA support:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: rdma-policy
  namespace: sriov-network-operator
spec:
  deviceType: netdevice      # Use netdevice for Mellanox
  isRdma: true              # Enable RDMA capabilities
  nicSelector:
    vendor: "15b3"          # Mellanox
    deviceID: "1017"        # ConnectX-5
    pfName: ["enp3s0f0"]
  nodeSelector:
    rdma-capable: "true"
  numVfs: 4
  priority: 90
  resourceName: rdma_exclusive_device
```

### Step 3: Create RDMA Network

Define network with RDMA CNI plugin:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: rdma-network
  namespace: default
spec:
  resourceName: rdma_exclusive_device
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

### Step 4: Deploy RDMA Application

Create a pod that uses RDMA resources:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: rdma-app
  namespace: default
  annotations:
    k8s.v1.cni.cncf.io/networks: rdma-network
spec:
  containers:
  - name: rdma-container
    image: mellanox/rdma-perftest:latest
    command: ["sleep", "3600"]
    securityContext:
      capabilities:
        add: ["IPC_LOCK"]
    resources:
      requests:
        openshift.io/rdma_exclusive_device: "1"
        memory: "1Gi"
      limits:
        openshift.io/rdma_exclusive_device: "1"
        memory: "1Gi"
```

## Monitoring and Observability

### RDMA Metrics

Monitor RDMA usage:

```bash
# Check RDMA device status
kubectl exec -it <pod-name> -- rdma link show

# Monitor RDMA counters
kubectl exec -it <pod-name> -- cat /sys/class/infiniband/mlx5_0/ports/1/counters/*

# Check RDMA resource allocation
kubectl describe node <node-name> | grep openshift.io/rdma_exclusive_device
```

## Troubleshooting

### Common Issues

#### 1. RDMA Mode Change Fails

```bash
# Check pool configuration
kubectl describe sriovnetworkpoolconfig rdma-workers -n sriov-network-operator

# Verify node labels
kubectl get nodes --show-labels | grep rdma

# Check operator logs
kubectl logs deployment/sriov-network-operator -n sriov-network-operator
```

#### 2. Pod Cannot Access RDMA

```bash
# Verify RDMA resources
kubectl describe node <node-name> | grep rdma_exclusive_device

# Check device plugin pods
kubectl get pods -n sriov-network-operator | grep device-plugin

# Verify CNI configuration
kubectl get networkattachmentdefinition rdma-network -o yaml
```

## Next Steps

- [OVS Hardware Offload](ovs-hw-offload.md) - Combine RDMA with hardware acceleration
- [Monitoring Guide](monitoring.md) - Set up comprehensive monitoring
- [Troubleshooting](troubleshooting.md) - Solve complex issues