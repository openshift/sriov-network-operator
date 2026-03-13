# SriovNetwork and OVSNetwork API Reference

## SriovNetwork

A SriovNetwork custom resource represents a layer-2 broadcast domain where SR-IOV devices are attached. It is primarily used to generate a NetworkAttachmentDefinition CR with an SR-IOV CNI plugin configuration.

The SriovNetwork CR contains the `resourceName` which is aligned with the `resourceName` of the `sriovNetworkNodePolicy`. One SriovNetwork object maps to one `resourceName`, but one `resourceName` can be shared by different SriovNetwork CRs.

### Basic SriovNetwork Example

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

### SriovNetwork Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `resourceName` | string | Must match the resourceName in SriovNetworkNodePolicy |
| `networkNamespace` | string | Target namespace for NetworkAttachmentDefinition (defaults to same as SriovNetwork) |
| `ipam` | string | IPAM configuration in JSON format |
| `vlan` | integer | VLAN ID (0 for untagged) |
| `vlanQoS` | integer | VLAN QoS priority |
| `spoofChk` | string | Enable/disable spoof checking ("on", "off") |
| `trust` | string | Enable/disable VF trust mode ("on", "off") |
| `linkState` | string | Set VF link state ("auto", "enable", "disable") |
| `maxTxRate` | integer | Maximum transmit rate (Mbps) |
| `minTxRate` | integer | Minimum transmit rate (Mbps) |
| `capabilities` | string | JSON string of additional capabilities |
| `metaPlugins` | string | Deprecated: use metaPluginsConfig |
| `metaPluginsConfig` | string | CNI meta-plugins configuration |

## Chaining CNI Meta-Plugins

You can add additional capabilities to SR-IOV devices by configuring optional meta-plugins. The `metaPluginsConfig` field contains one or more additional configurations used to build a network configuration list.

### Meta-Plugins Example

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
  metaPluginsConfig: |
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

## RDMA Configuration

For RDMA workloads, configure the SriovNetwork with the RDMA CNI plugin as a meta-plugin.

### RDMA Network Example

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

### RDMA Requirements

1. **RDMA subsystem mode set to `exclusive`** via SriovNetworkPoolConfig
2. **SriovNetworkNodePolicy with `spec.isRdma=true`**
3. **SriovNetwork using RDMA CNI plugin as meta-plugin**

See [RDMA Configuration Guide](../rdma-configuration.md) for complete setup instructions.

## OVSNetwork

An OVSNetwork custom resource represents a layer-2 broadcast domain attached to Open vSwitch that works in hardware-offloading mode. It generates a NetworkAttachmentDefinition CR with an OVS CNI plugin configuration.

The OVSNetwork CR contains the `resourceName` which should reference Virtual Functions of a NIC in switchdev mode. The Physical Function of the NIC should be attached to an OVS bridge before any workload using OVSNetwork starts.

### OVSNetwork Example

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

### OVSNetwork Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `resourceName` | string | Must reference VFs in switchdev mode |
| `networkNamespace` | string | Target namespace for NetworkAttachmentDefinition |
| `ipam` | string | IPAM configuration in JSON format |
| `vlan` | integer | VLAN ID |
| `bridge` | string | OVS bridge name |
| `mtu` | integer | MTU size |
| `trunk` | []integer | List of allowed VLAN IDs for trunk mode |

## Using Networks in Pods

Once you've created SriovNetwork or OVSNetwork resources, reference them in pod annotations:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: example-pod
  annotations:
    k8s.v1.cni.cncf.io/networks: example-network
spec:
  containers:
  - name: app
    image: centos:latest
    command: ["sleep", "3600"]
    resources:
      requests:
        openshift.io/intelnics: "1"
      limits:
        openshift.io/intelnics: "1"
```

### Multiple Networks

```yaml
metadata:
  annotations:
    k8s.v1.cni.cncf.io/networks: |
      [
        {"name": "network1"},
        {"name": "network2", "namespace": "other-namespace"}
      ]
```

## Troubleshooting

### Verify NetworkAttachmentDefinition Creation

```bash
kubectl get networkattachmentdefinition
kubectl describe networkattachmentdefinition <network-name>
```

### Check Resource Availability

```bash
kubectl describe node <node-name> | grep -A 10 "Allocatable:"
kubectl get pods -o wide --all-namespaces | grep sriovdp
```

### Common Issues

1. **ResourceName mismatch**: Ensure SriovNetwork.resourceName matches SriovNetworkNodePolicy.resourceName
2. **Namespace issues**: Check that NetworkAttachmentDefinition is created in the correct namespace
3. **IPAM conflicts**: Verify IPAM subnets don't overlap with existing networks
4. **Meta-plugin errors**: Validate JSON syntax in metaPluginsConfig field