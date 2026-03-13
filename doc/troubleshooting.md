# Troubleshooting Guide

This guide helps diagnose and resolve common issues with the SR-IOV Network Operator.

## Quick Diagnostics

### Basic Health Check

```bash
# Check operator status
kubectl get deployment -n sriov-network-operator

# Verify all pods are running
kubectl get pods -n sriov-network-operator

# Check operator logs
kubectl logs deployment/sriov-network-operator -n sriov-network-operator --tail=50
```

### Node Status Check

```bash
# List all node states
kubectl get sriovnetworknodestate -n sriov-network-operator

# Check specific node details
kubectl describe sriovnetworknodestate <node-name> -n sriov-network-operator
```

## Common Issues and Solutions

### 1. Operator Installation Issues

#### Problem: Operator pods not starting

**Symptoms:**
- Pods stuck in `Pending` or `CrashLoopBackOff`
- ImagePullBackOff errors

**Diagnosis:**
```bash
kubectl describe pod <operator-pod> -n sriov-network-operator
kubectl logs <operator-pod> -n sriov-network-operator
```

**Solutions:**
- Verify cluster meets prerequisites
- Check image registry accessibility
- Ensure proper RBAC permissions
- Verify node labels for scheduling

#### Problem: Webhook configuration fails

**Symptoms:**
- ValidatingAdmissionWebhook errors
- Policy creation blocked

**Diagnosis:**
```bash
kubectl get validatingwebhookconfiguration
kubectl describe validatingwebhookconfiguration operator-webhook-config
```

### 2. Hardware Discovery Issues

#### Problem: No SR-IOV devices detected

**Symptoms:**
- Empty status in SriovNetworkNodeState
- No devices listed in node state

**Diagnosis:**
```bash
# Check node labels
kubectl get nodes --show-labels | grep sriov

# Verify hardware directly on node
kubectl debug node/<node-name> -it --image=busybox
# In debug container:
lspci | grep -i ethernet
cat /sys/class/net/*/device/sriov_totalvfs
```

**Solutions:**
- Verify SR-IOV capability in BIOS
- Install Node Feature Discovery (NFD)
- Check hardware compatibility
- Ensure drivers are loaded

#### Problem: Devices detected but not configurable

**Symptoms:**
- Devices show in status but VF creation fails
- Error messages about unsupported operations

**Diagnosis:**
```bash
# Check specific device capabilities
kubectl exec -it <config-daemon-pod> -n sriov-network-operator -- \
  cat /sys/class/net/<interface>/device/sriov_totalvfs

# Check driver information
kubectl exec -it <config-daemon-pod> -n sriov-network-operator -- \
  ethtool -i <interface>
```

**Solutions:**
- Update network drivers
- Verify device supports SR-IOV
- Check firmware version
- Ensure proper kernel modules loaded

### 3. Policy Configuration Issues

#### Problem: SriovNetworkNodePolicy not applied

**Symptoms:**
- Policy exists but no changes on nodes
- Status shows policy but spec unchanged

**Diagnosis:**
```bash
# Check policy status
kubectl get sriovnetworknodepolicy -n sriov-network-operator
kubectl describe sriovnetworknodepolicy <policy-name> -n sriov-network-operator

# Verify node selection
kubectl get nodes -l <node-selector-labels>

# Check controller logs
kubectl logs deployment/sriov-network-operator -n sriov-network-operator | grep -i policy
```

**Solutions:**
- Verify nodeSelector matches target nodes
- Check policy priority conflicts
- Ensure device selection criteria are correct
- Validate policy syntax

#### Problem: Multiple policy conflicts

**Symptoms:**
- Unexpected VF configuration
- Some policies ignored

**Diagnosis:**
```bash
# List all policies affecting a device
kubectl get sriovnetworknodepolicy -n sriov-network-operator -o yaml | grep -A 20 -B 5 "<device-id>"

# Check priority order
kubectl get sriovnetworknodepolicy -n sriov-network-operator --sort-by='.spec.priority'
```

**Solutions:**
- Review policy priorities (0 = highest)
- Use VF range notation (#0-3) for non-overlapping policies
- Consolidate conflicting policies
- Check processing order (priority, then name)

### 4. Virtual Function Creation Issues

#### Problem: VFs not created

**Symptoms:**
- numVfs configured but VFs don't appear
- Error in config daemon logs

**Diagnosis:**
```bash
# Check config daemon logs
kubectl logs daemonset/sriov-config-daemon -n sriov-network-operator

# Verify VF creation on node
kubectl debug node/<node-name> -it --image=busybox
# Check VF count
cat /sys/class/net/<pf>/device/sriov_numvfs
ls /sys/class/net/<pf>/device/virtfn*
```

**Solutions:**
- Check maximum VF limit: `cat /sys/class/net/<pf>/device/sriov_totalvfs`
- Verify sufficient resources (memory, IOMMU groups)
- Check for hardware/firmware limitations
- Ensure no existing VFs prevent reconfiguration

#### Problem: VF driver binding fails

**Symptoms:**
- VFs created but wrong driver bound
- Device plugin not seeing resources

**Diagnosis:**
```bash
# Check VF driver binding
kubectl debug node/<node-name> -it --image=busybox
lspci -vv | grep -A 10 "Virtual Function"

# Check driver modules
lsmod | grep -E "(vfio|uio)"
```

**Solutions:**
- Verify driver modules loaded: `modprobe vfio-pci`
- Check IOMMU enablement: `dmesg | grep -i iommu`
- Ensure correct deviceType in policy
- Rebind manually if needed:
  ```bash
  echo <vf-pci-addr> > /sys/bus/pci/drivers/<old-driver>/unbind
  echo <vf-pci-addr> > /sys/bus/pci/drivers/<new-driver>/bind
  ```

### 5. Network Configuration Issues

#### Problem: NetworkAttachmentDefinition not created

**Symptoms:**
- SriovNetwork exists but no NAD
- Pods can't use SR-IOV network

**Diagnosis:**
```bash
# Check SriovNetwork status
kubectl describe sriovnetwork <network-name> -n sriov-network-operator

# List NetworkAttachmentDefinitions
kubectl get networkattachmentdefinition -A

# Check operator logs for NAD creation
kubectl logs deployment/sriov-network-operator -n sriov-network-operator | grep -i "networkattachmentdefinition"
```

**Solutions:**
- Verify SriovNetwork resourceName matches policy
- Check target namespace exists
- Ensure proper RBAC for NAD creation
- Validate IPAM configuration syntax

#### Problem: Pod network attachment fails

**Symptoms:**
- Pod stuck in ContainerCreating
- CNI errors in pod events

**Diagnosis:**
```bash
# Check pod events
kubectl describe pod <pod-name>

# Check Multus logs
kubectl logs -l app=multus -n kube-system

# Verify device plugin resources
kubectl describe node <node-name> | grep -A 5 "Allocatable"
```

**Solutions:**
- Verify device plugin pods running
- Check resource requests match available resources
- Ensure proper annotation format
- Validate NetworkAttachmentDefinition configuration

### 6. Device Plugin Issues

#### Problem: Device plugin not exposing resources

**Symptoms:**
- `kubectl describe node` shows no SR-IOV resources
- Pods can't request SR-IOV devices

**Diagnosis:**
```bash
# Check device plugin pods
kubectl get pods -l app=sriov-device-plugin -n sriov-network-operator

# Check device plugin logs
kubectl logs -l app=sriov-device-plugin -n sriov-network-operator

# Verify device plugin config
kubectl get configmap sriovdp-config -n sriov-network-operator -o yaml
```

**Solutions:**
- Restart device plugin pods
- Verify VFs are properly created and bound
- Check device plugin configuration map
- Ensure kubelet socket permissions

### 7. Performance Issues

#### Problem: Poor network performance

**Symptoms:**
- Lower than expected throughput
- High latency
- Packet drops

**Diagnosis:**
```bash
# Check interface statistics
kubectl exec -it <pod-name> -- ethtool -S <interface>

# Monitor interrupts
kubectl exec -it <pod-name> -- cat /proc/interrupts

# Check CPU affinity
kubectl exec -it <pod-name> -- cat /proc/irq/*/smp_affinity
```

**Solutions:**
- Tune interrupt affinity
- Configure CPU isolation
- Adjust queue sizes
- Enable hardware offloads
- Use appropriate NUMA topology

### 8. RDMA-Specific Issues

#### Problem: RDMA resources not available

**Symptoms:**
- No RDMA devices in pods
- RDMA applications fail to start

**Diagnosis:**
```bash
# Check RDMA mode
kubectl get sriovnetworkpoolconfig -n sriov-network-operator -o yaml

# Verify RDMA devices
kubectl exec -it <pod-name> -- rdma link show
kubectl exec -it <pod-name> -- ls -la /dev/infiniband/
```

**Solutions:**
- Configure RDMA mode in SriovNetworkPoolConfig
- Set `isRdma: true` in node policy
- Add RDMA CNI plugin to network
- Mount necessary device files

### 9. Virtual Environment Issues

#### Problem: SR-IOV not working in VMs

**Symptoms:**
- No VFs detected in virtual environment
- Nested virtualization problems

**Diagnosis:**
```bash
# Check if running in VM
kubectl debug node/<node-name> -it --image=busybox
dmesg | grep -i "hypervisor detected"

# Check PCI passthrough
lspci | grep -i ethernet
cat /proc/cpuinfo | grep -i hypervisor
```

**Solutions:**
- Enable nested virtualization in hypervisor
- Configure PCI passthrough correctly
- Use appropriate deviceType (vfio-pci for Intel, netdevice for Mellanox)
- Adjust numVfs expectations (always 1 in VMs)

## Diagnostic Commands Reference

### Node-Level Diagnostics

```bash
# Hardware information
lspci | grep -i ethernet
lshw -c network
ethtool -i <interface>

# SR-IOV capabilities
cat /sys/class/net/*/device/sriov_totalvfs
cat /sys/class/net/*/device/sriov_numvfs
ls /sys/class/net/*/device/virtfn*

# Driver information
lsmod | grep -E "(sriov|vfio|igb|ixgbe|i40e|ice|mlx)"
dmesg | grep -i sriov
```

### Kubernetes Diagnostics

```bash
# Operator status
kubectl get all -n sriov-network-operator
kubectl describe deployment sriov-network-operator -n sriov-network-operator

# Resource states
kubectl get sriovoperatorconfig -n sriov-network-operator -o yaml
kubectl get sriovnetworknodepolicy -n sriov-network-operator
kubectl get sriovnetwork -n sriov-network-operator
kubectl get sriovnetworknodestate -n sriov-network-operator

# Device plugin verification
kubectl get nodes -o json | jq '.items[].status.allocatable'
```

### Pod-Level Diagnostics

```bash
# Network interfaces
kubectl exec -it <pod-name> -- ip link show
kubectl exec -it <pod-name> -- ip addr show

# SR-IOV device information
kubectl exec -it <pod-name> -- ls -la /dev/
kubectl exec -it <pod-name> -- lspci

# Performance testing
kubectl exec -it <pod-name> -- iperf3 -s  # Server
kubectl exec -it <pod-name> -- iperf3 -c <server-ip>  # Client
```

## Log Analysis

### Important Log Locations

```bash
# Operator logs
kubectl logs deployment/sriov-network-operator -n sriov-network-operator

# Config daemon logs (per node)
kubectl logs daemonset/sriov-config-daemon -n sriov-network-operator

# Device plugin logs (per node)
kubectl logs -l app=sriov-device-plugin -n sriov-network-operator

# Webhook logs
kubectl logs deployment/sriov-network-operator-webhook -n sriov-network-operator
```

### Common Error Patterns

#### Configuration Errors
```text
"failed to configure VF": Check VF limits and hardware support
"device not found": Verify device selection criteria
"driver binding failed": Check driver modules and IOMMU
```

#### Resource Errors
```text
"insufficient resources": Check device availability
"allocation failed": Verify device plugin status
"no devices available": Check VF creation and binding
```

#### Network Errors
```text
"CNI failed": Check NetworkAttachmentDefinition and Multus
"IPAM error": Verify IPAM configuration syntax
"interface creation failed": Check SR-IOV device availability
```

## Recovery Procedures

### Reset Node Configuration

```bash
# Remove all VFs
kubectl patch sriovnetworknodepolicy <policy-name> -n sriov-network-operator --type='merge' -p='{"spec":{"numVfs":0}}'

# Wait for configuration to apply
kubectl get sriovnetworknodestate -n sriov-network-operator -w

# Reapply configuration
kubectl patch sriovnetworknodepolicy <policy-name> -n sriov-network-operator --type='merge' -p='{"spec":{"numVfs":4}}'
```

### Restart Operator Components

```bash
# Restart operator
kubectl rollout restart deployment/sriov-network-operator -n sriov-network-operator

# Restart config daemon
kubectl rollout restart daemonset/sriov-config-daemon -n sriov-network-operator

# Restart device plugin
kubectl delete pods -l app=sriov-device-plugin -n sriov-network-operator
```

### Clean State Recovery

```bash
# Remove all policies
kubectl delete sriovnetworknodepolicy --all -n sriov-network-operator

# Wait for VFs to be removed
kubectl get sriovnetworknodestate -n sriov-network-operator -w

# Restart all components
kubectl rollout restart deployment -n sriov-network-operator
kubectl rollout restart daemonset -n sriov-network-operator
```

## Getting Help

### Information to Collect

When reporting issues, include:

1. **Environment details:**
   ```bash
   kubectl version
   kubectl get nodes -o wide
   kubectl get sriovoperatorconfig -n sriov-network-operator -o yaml
   ```

2. **Configuration:**
   ```bash
   kubectl get sriovnetworknodepolicy -n sriov-network-operator -o yaml
   kubectl get sriovnetwork -n sriov-network-operator -o yaml
   ```

3. **Status information:**
   ```bash
   kubectl get sriovnetworknodestate -n sriov-network-operator -o yaml
   kubectl get events -n sriov-network-operator --sort-by='.lastTimestamp'
   ```

4. **Logs:**
   ```bash
   kubectl logs deployment/sriov-network-operator -n sriov-network-operator
   kubectl logs daemonset/sriov-config-daemon -n sriov-network-operator
   ```

### Community Resources

- [GitHub Issues](https://github.com/k8snetworkplumbingwg/sriov-network-operator/issues)
- [Kubernetes Network Plumbing WG](https://github.com/k8snetworkplumbingwg)
- [Slack Discussions](https://kubernetes.slack.com/channels/sig-network)

## Preventive Measures

### Regular Maintenance

1. **Monitor resource utilization**
2. **Keep drivers updated**
3. **Regular configuration backups**
4. **Performance baseline testing**
5. **Log rotation and cleanup**

### Best Practices

1. **Test configurations in development first**
2. **Use infrastructure as code for consistency**
3. **Implement proper monitoring and alerting**
4. **Document custom configurations**
5. **Plan maintenance windows for changes**

## Advanced Debugging

### Using Debug Containers

```bash
# Debug node issues
kubectl debug node/<node-name> -it --image=ubuntu

# Debug pod networking
kubectl debug <pod-name> -it --image=nicolaka/netshoot
```

### Performance Profiling

```bash
# CPU profiling
kubectl exec -it <pod-name> -- perf top

# Network profiling
kubectl exec -it <pod-name> -- tcpdump -i <interface>

# Memory profiling
kubectl exec -it <pod-name> -- cat /proc/meminfo
```

This troubleshooting guide covers the most common issues encountered with the SR-IOV Network Operator. For complex or unique problems, consider engaging with the community through the official channels.