# Infiniband VF GUID Static Configuration

SR-IOV Network Operator is able to use a static configuration file from the host filesystem to assign GUIDs to IB VFs.

## Prerequisites

- Infiniband NICs
- Static GUID configuration file on the host filesystem

### Configuration file

Config file should be stored at `/etc/sriov-operator/infiniband/guids`. This location is writable across most cloud platforms.

VF to GUID assignment, based on this file, is ordered. VF0 takes the GUID0, VF1 takes the GUID1 etc.

Each PF has its own set of GUIDs.

Example of the config file:

```json
[
  {
    "pci_address": "<pci_address_1>",
    "guids": [
      "02:00:00:00:00:00:00:00",
      "02:00:00:00:00:00:00:01"
    ]
  },
  {
    "pf_guid": "<pf_guid_2>",
    "guidsRange": {
      "start": "02:00:00:00:00:aa:00:02",
      "end": "02:00:00:00:00:aa:00:0a"
    }
  }
]
```

Config file parameters:

* `pci_address` is a PCI address of a PF
* `pf_guid` is a GUID of a PF. Can be obtained with `ibstat` command.
* `guids` is an array of VF GUID strings
* `guidsRange` is an object representing the start and end of a VF GUID range. It has two fields:
    * `start` is a VF GUID range start
    * `end` is a VF GUID range end

Requirements for the config file:

* `pci_address` and `pf_guid` cannot be set at the same time for a single device - should return an error
* if the list contains multiple entries for the same device, the first one shall be taken
* `rangeStart` and `rangeEnd` are both included in the range
* `guids` list and range cannot be both set at the same time for a single device - should return an error
* GUIDs are assigned once and not change throughout the lifecycle of the host

### Deploy SriovNetworkNodePolicy

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: ib-policy
  namespace: sriov-network-operator
spec:
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  resourceName: ib_vfs
  priority: 10
  numVfs: 2
  nicSelector:
    vendor: "15b3"
    rootDevices:
    - 0000:d8.00.0
  deviceType: netdevice
```

## Verify assigned GUIDs

Run ip link tool to verify assigned GUIDs:

```bash
ip link

...
6: ibp216s0f0: <BROADCAST,MULTICAST> mtu 4092 qdisc noop state UP mode DEFAULT group default qlen 256
    link/infiniband 00:00:05:a9:fe:80:00:00:00:00:00:00:b8:59:9f:03:00:f9:8f:86 brd 00:ff:ff:ff:ff:12:40:1b:ff:ff:00:00:00:00:00:00:ff:ff:ff:ff
    vf 0     link/infiniband ... NODE_GUID 02:00:00:00:00:00:00:00, PORT_GUID 02:00:00:00:00:00:00:00, link-state enable, trust off, query_rss off
    vf 1     link/infiniband ... NODE_GUID 02:00:00:00:00:00:00:01, PORT_GUID 02:00:00:00:00:00:00:01, link-state enable, trust off, query_rss off
```

