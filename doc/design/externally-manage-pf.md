---
title: Externally Manage PF
authors:
  - SchSeba
reviewers:
  - zeeke
  - adrianchiris
creation-date: 12-07-2023
last-updated: 12-07-2023
---

# Externally Manage PF

## Summary

Allow the SR-IOV network operator to configure and allocate a subset of virtual functions from
a physical function that is configured externally from SR-IOV network operator.

## Motivation

The feature is needed to allow the operator to only configure a subset of virtual functions.
This allows a third party component like nmstate, kubernetes-nmstate, NetworkManager to handle the creation
and the usage of the virtual functions on the system. Some of the examples are using the virtual function as the primary
nic for the k8s SDN network or a storage network.

Before this change the SR-IOV network operator is the only component that should use/configure VFs. not allowing the user
to use some of the VFs for host networking.

### Use Cases

* As a user I want to use a virtual function for SDN network, for SDN the network need to be configured before
k8s is deployed and these VFs should be available at system startup before pods start running
* As a user I want to create the virtual functions via nmstate
* As a user I want pods to use virtual functions from a pre-configured PF
* As a user I want to allocate virtual functions to pods from a PF with custom configuration/driver
* As a user I want to use virtual functions to be configured for the storage subsystem before k8s is deployed / pods spinning up at system startup

### Goals

* Allow the SR-IOV network operator to handle the configuration and pod allocation of some or all virtual functions
while PF configuration are managed by an external entity
* Allow the user to Allocate the number of virtual functions he wants for the system and the subset he wants for pods

### Non-Goals

* Supporting switchdev mode (may change in the future if there is a request)
* Supporting the creation of the VFs on boot by the operator possible to use operator systemd mode for that

## Proposal

Create a sub-flow in the SR-IOV network operator where the user can request a configuration for all/subset of virtual functions
without any changes in the PF level.

The operator will first validate the requested PF contains the requested amount of virtual functions allocated, it
will also validate the requested MTU is configured as expected on the PF.
If that is not the case the `sriovNetworkNodeState.status.SyncStatus` field will be report a `Failed`

Then the operator will configure the subset of virtual functions with the requested driver and will update the device plugin
configmap with the expected information to create the relevant pools.

Existing sriov network config daemon flow:
1. Apply the `numOfVfs`
2. Configure the MTU on the PF
3. Copy the Administrative mac address from the VFs
4. Bind the right driver for the VF
5. restart sriov network device plugin

Externally manage sriov network config daemon flow:
1. Copy the Administrative mac address from the VFs
2. Bind the right driver for the VF
3. restart sriov network device plugin

In both flows:
* In case of Infiniband link type it will generate random node and port GUID for the interface.
* In case of RDMA (both for ETH and IB) it will perform an unbind/bind of the VF driver to set RDMA Node/Port GUID.

### Workflow Description

The user will allocate the virtual functions on the system with any third party tool like nmstate, Kubnernetes-nmstate,
systemd scripts, etc..

The user must perform the sriov allocation/configuration before kubelet starts or more specifically
before SR-IOV Network operator configuration daemon starts running on the node.

Then the user will be able to create a policy telling the operator that the PF is externally managed by the user.

If the user want to create the virtual functions after the SR-IOV Network config daemon is already running on the system he will need
to disable the webhook. the policy will be on failed state until the virtual functions needed for the policy exist
on the node. the SR-IOV Network config daemon will continue to reconcile until the virtual functions exists

#### Policy Example:
```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: sriov-nic-1
  namespace: sriov-network-operator
spec:
  deviceType: netdevice
  nicSelector:
    pfNames: ["ens3f0#5-9"]
  nodeSelector:
    node-role.kubernetes.io/worker: ""
  numVfs: 10
  priority: 99
  resourceName: sriov_nic_1
  externallyManaged: true
```

The PF and VFs 0-4 are externally managed. 
For example nmstate will create 10 vfs, but will only consume VF 0 and 4 in its configuration. Nmstate will also manage the MTU and other parameters of the PF.

#### Another Policy Example:
In this case we allocate all the virtual functions from the PF

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: sriov-nic-2
  namespace: sriov-network-operator
spec:
  deviceType: netdevice
  nicSelector:
    pfNames: ["ens3f0"]
  nodeSelector:
    node-role.kubernetes.io/worker: ""
  numVfs: 10
  priority: 99
  resourceName: sriov_nic_1
  externallyManaged: true
```

The SR-IOV network operator will use all the 10 virtual functions created externally by the user.
One if the main use cases for this is if the user want to do some custom configuration to the PF and VFs like loading
out of tree drivers or other stuff that the operator doesn't support.

#### Validation
The SR-IOV network operator will do a validation webhook to check if the requested `numVfs` is equal to what the user allocate
if not it will reject the policy creation.

The SR-IOV network operator will do a validation webhook to check if the requested MTU is lower or equal to what exist on the PF 
if not it will reject the policy creation.


*Note:* Same validation will be done in the SR-IOV config-daemon container to cover cases where the user doesn't want to deploy"
the webhook and to cover scale-up adding new nodes. If the verification failed in the policy apply stage
the `sriovNetworkNodeState.status.SyncStatus` field will be report a `Failed` status and the error description will 
get exposed in `sriovNetworkNodeState.status.LastSyncError`


#### Configuration

The SR-IOV network operator config daemon will reconcile on the SriovNetworkNodeState update and will follow the regular
flow of virtual functions *SKIPPING* only the Virtual function allocation.

The SR-IOV network operator will update the SR-IOV Network Device Plugin with the pool information

Another change with the operator beavior is when we delete a policy with had `externallyManaged: true` the SR-IOV network operator
will *NOT* reset the `numVfs`

### API Extensions

For SriovNetworkNodePolicy

```golang
// SriovNetworkNodePolicySpec defines the desired state of SriovNetworkNodePolicy
type SriovNetworkNodePolicySpec struct {
...
+ // don't create the virtual function only assign to the driver and allocated them to device plugin. Defaults to false.
+ ExternallyManaged bool `json:"externallyManaged,omitempty"`
}
```

For SriovNetworkNodeState

```golang
type Interface struct {
...
+ ExternallyManaged bool      `json:"externallyManaged,omitempty"`
}
```

### Implementation Details/Notes/Constraints

#### Webhook
For the webhook we add more validations when the policy contains `ExternallyManaged: true`
* `numVfs` in the policy equal is equal or lower the number of virtual functions on the system
* `MTU` in the policy equals or lower the MTU we discover on the PF
* `LinkType` in the policy equals the link type we discover on the PF

#### Controller/Manager

The changes in the manager for this feature are minimal we only copy the `ExternallyManaged` boolean from the policy
to the generated `nodeState.Spec`

#### Config Daemon

This is where most of the changes for this feature are implemented.

* do a validation same as on the webhook to check the PF have everything we need to apply the requested
policy, by checking the `numVfs`, `MTU` and `LinkType`.
* skip all the PF configuration like `numVfs`, `MTU` and `LinkType`. he will only perform the virtual function 
driver binding, administrative mac allocation and MTU. 
* in case of Infiniband link type it will generate random node and port GUID for the interface
* in case of RDMA (both for ETH and IB) it will perform an unbind/bind of the VF driver to set RDMA Node/Port GUID.
* reset the device plugin so kubelet will be able to discover the SR-IOV devices.

*NOTE:* The config-daemon will also save on the node a cache (file) of the last applied policy. this is needed to be able and understand
if we need to reset the PF configuration(`ExternallyManaged` was false) or not when policy is removed.

### Upgrade & Downgrade considerations

The feature supports both Upgrade and Downgrade as we are introducing a new field in the API.
Downgrade will cause the operator to treat an externally managed PF as non externally managed and actually configure PF,
this may cause conflicts in the system.

### Test Plan

* Should not allow to create a policy with externallyManaged true if there are no vfs configured
* Should create a policy if the number of requested vfs is equal
* Should create a policy if the number of requested vfs is equal and not delete them when the policy is removed
* should reset the virtual functions if externallyCreated is false
* should to configure a policy with externallyManaged true if there are no vfs configured with disabled webhook