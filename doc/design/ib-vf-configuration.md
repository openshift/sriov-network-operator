---
title: IB VF GUID Configuration
authors:
  - almaslennikov
reviewers:
  - ykulazhenkov
creation-date: 11-03-2024
last-updated: 13-03-2024
---

# IB VF GUID Configuration

## Summary
Allow SR-IOV Network Operator to use a static configuration file from the host filesystem to assign GUIDs to IB VFs

## Motivation
We have customers using the SR-IOV operator to create IB VFs, and they need a way to automate GUID assignment,
so that IB VFs are automatically bound to the required PKeys and no additional manual configuration is needed.
We would like SR-IOV Network Operator to configure VFs with the set of assigned guids based on provided configuration.
In this use case, the GUID configuration is static, GUIDs are generated and assigned to pkeys by the user (e.g network administrator).
Now the GUIDs are assigned by the sriov-network-config-daemon randomly.

### Use Cases

### Goals

* SR-IOV Network Operator will configure GUIDs for Infiniband VFs based on a json file on the host.

### Non-Goals

* Dynamic GUID allocation is out of scope of this proposal


### Assumptions

* Per node IB GUID configuration is static and created in advance
* Static config file is available at the same path on every host
* The contents of the config file are expected not to change throughout the host's lifecycle

## Proposal

Mount a configuration file as a part of `/host` hostPath in the config daemon and read it to retrieve the GUID configuration.

### Workflow Description

In the [`pkg/host/internal/sriov/sriov.go:configSriovVFDevices`](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/82a6d6fdce71bd88a0d9368fb1750488e9a8e4e2/pkg/host/internal/sriov/sriov.go#L458) read from the static IB config file
and assign the GUIDs according to the configuration. Each PF is described by either PCI address
or the PF GUID and has either a list or a range of VF GUIDs.

1. A script creating the config file is deployed to the host and creates a static GUID config file
2. SR-IOV network operator reads the file and assign GUIDs when IB VFs are created
3. Users employ [PKey selector](https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pull/517) in sriov-network-device-plugin to create PKey-specific resource pools
   3.1. Note: When a VF gets assigned with GUID, it communicates with subnet manager and receives the PKey associated with that GUID. This PKey is stored in the sysfs and device-plugin reads it to populate pools.
4. Users create an IB network CR
5. Users create pods referencing the IB network and request resources from the specific pools.

### Implementation Details/Notes/Constraints

There can be fewer VFs created than GUIDs. To persist the dynamic nature of the SR-IOV Network operator,
it’s proposed not to return an error in this case but assign as many GUIDs as possible.
To ensure that nothing breaks when users add/remove VFs, the GUID distribution order should always be the same for each individual host.

Parallel NIC configuration should be supported by making the GUID immutable. VF id should be used to determine the correct GUID for each VF.

If there are fewer GUIDs than VFs, should fail with an error.

If the contents of the file are invalid, should fail with an error.

If config file doesn't exist in the predefined location, proceed as usual and assign random GUIDs to VFs.

### Config file

Config file can be stored at `/etc/sriov-operator/infiniband/guids`. This location is writable across most cloud platforms.

VF to GUID assignment, based on this file, is ordered. VF0 takes the GUID, VF1 takes the GUID1 etc.

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

```go
type ibPfGUIDJSONConfig struct {
    PciAddress string     `json:"pciAddress,omitempty"`
    PfGUID     string     `json:"pfGuid,omitempty"`
    GUIDs      []string   `json:"guids,omitempty"`
    GUIDsRange *GUIDRange `json:"guidsRange,omitempty"`
}

type GUIDRange struct {
    Start string `json:"start,omitempty"`
    End string   `json:"end,omitempty"`
}
```

Requirements for the config file:

* `pci_adress` and `pf_guid` cannot be set at the same time for a single device - should return an error
* if the list contains multiple entries for the same device, the first one shall be taken
* `rangeStart` and `rangeEnd` are both included in the range
* `guids` list and range cannot be both set at the same time for a single device - should return an error

### Test Plan

* Unit tests will be implemented for new logic.

## Alternative solution

The alternative solution is also based on the GUID configuration file being deployed on the host.
The difference here is that GUID assignment is done on the cni level when a VF is allocated to a pod.
ib-sriov-cni manages a host-local per-PF pool of allocated/free GUIDs and dynamically allocates the next free GUID to an allocated VF.

### Workflow:

1. A script is deployed to the host and creates a static GUID config file. This step is out of scope of the operator and can also be carried out manually. The script would usually need to be custom and based on the specific GUID provisioning system in place.
2. SR-IOV network operator creates IB VFs with random GUIDs (as done now)
3. ib-sriov-cni is deployed, reads the config file and creates a cached GUID pool
4. Users create a single resource pool / per PF resource pools for IB VFs
5. Users create an IB network CR (and provide a PKey here)
6. Users create a pod requesting an IB VF
7. ib-sriov-cni allocates a next free GUID from the pool depending on the PKey and assigns it to the allocated VF

## Comparison between the two alternatives

The SR-IOV Network Operator approach:
* Easier to implement and less error-prone
* Manages the whole lifecycle of the VF (GUID is assigned at creation and never changes throughout the lifecycle)
* Operator has better visibility into the amount of configured VFs

The IB-SRIOV-CNI approach:
* Offers more flexibility (Only when a VF is requested for an IB network will it be assigned a GUID)
* Easier to maintain complex use cases
    * 2 PFs on the node evenly split between 2 PKeys. The CNI approach will require 2 per-PF resource pools and 4 network attachments. The operator approach will require 4 resource pools and 4 network attachments, one for each PKey-PF pair.