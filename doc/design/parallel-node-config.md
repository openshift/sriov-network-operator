---
title: Parallel SR-IOV configuration
authors:
  - SchSeba
reviewers:
  - adrianchiris
  - e0ne
creation-date: 18-07-2023
last-updated: 18-07-2023
---

# Parallel SR-IOV configuration

## Summary
Allow SR-IOV Network Operator to configure more than one node at the same time.

## Motivation
SR-IOV Network Operator configures SR-IOV one node at a time and one nic at a same time. That means we’ll need to wait
hours or even days to configure all NICs  on large cluster deployments. Also moving all draining logic to a centralized
place which will reduce chances of race conditions and bugs that were encountered before in sriov-network-config-daemon
with draining.

### Use Cases

### Goals
* Number of drainable nodes should be 1 by default
* Number of drainable nodes should be configured by pool
* Nodes pool should be defined by node selector
* Move all drain-related logic into the centralized place


### Non-Goals
Parallel NICs configuration on the same node is out of scope of this proposal

## Proposal
Introduce nodes pool drain configuration and controller to meet goals targets.


### Workflow Description
A new Drain controller will be introduced to manage node drain and cordon procedures. That means we don't need to do
drain and use `drain lock` in config daemon anymore. The overall drain process will be covered by the following states:

```golang
NodeDrainAnnotation             = "sriovnetwork.openshift.io/state"
NodeStateDrainAnnotation        = "sriovnetwork.openshift.io/desired-state"
NodeStateDrainAnnotationCurrent = "sriovnetwork.openshift.io/current-state"
DrainIdle                       = "Idle"
DrainRequired                   = "Drain_Required"
RebootRequired                  = "Reboot_Required"
Draining                        = "Draining"
DrainComplete                   = "DrainComplete"
```

Drain controller will watch for Node annotation, `sriovnetwork.openshift.io/state` 
and SriovNetworkNodeState annotation `sriovnetwork.openshift.io/desired-state`
and write the `sriovnetwork.openshift.io/current-state` annotation in the SriovNetworkNodeState.

Config Daemon will read SriovNetworkNodeState annotation `sriovnetwork.openshift.io/current-state` and write both
Node annotation, `sriovnetwork.openshift.io/state` and SriovNetworkNodeState annotation `sriovnetwork.openshift.io/desired-state`.

*NOTE:* In the future we are going to drop the node annotation and only use the SriovNetworkNodeState

Draining procedure:

1. config daemon mark the node as `Drain_Required` or `Reboot_Required` by adding that to both the Node annotation, `sriovnetwork.openshift.io/state`
   and SriovNetworkNodeState annotation `sriovnetwork.openshift.io/desired-state`
2. operator drain controller reconcile loop find the node SriovNetworkPoolConfig
   1. if number of `Draining` nodes is great or equal to the `MaxUnavailable` the operator will re-queue the request
   2. if number of `Draining` nodes is lower than the `MaxUnavailable` the operator will start the draining process
   and annotate the SriovNetworkNodeState annotation `sriovnetwork.openshift.io/current-state` with `Draining`
5. on Openshift platform we will pause the machine config pool related to the node
6. the operator will start the drain process
   1. if `Drain_Required` the operator will remove ONLY pods used sriov devices
   2. if `Reboot_Required` the operator will remove ALL the pods on the system
9. operator moves the `sriovnetwork.openshift.io/current-state` annotation to `DrainComplete`
10. daemon will continue to the configuration when it's done it will move back both `sriovnetwork.openshift.io/state` 
annotation on Node and `sriovnetwork.openshift.io/desired-state` on SriovNetworkNodeState to `Idle`
11. operator runs the complete drain to remove the cordon and mark the `sriovnetwork.openshift.io/current-state` annotation to `Idle`

### API Extensions

#### Extend existing CR SriovNetworkPoolConfig
SriovNetworkPoolConfig is used only for OpenShift to provide configuration for
OVS Hardware Offloading. We can extend it to add configuration for the drain
pool. E.g.:

```golang
// SriovNetworkPoolConfigSpec defines the desired state of SriovNetworkPoolConfig
type SriovNetworkPoolConfigSpec struct {
    ...
	
    // nodeSelector specifies a label selector for Nodes
    NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`

    // maxUnavailable defines either an integer number or percentage
    // of nodes in the pool that can go Unavailable during an update.
    //
    // A value larger than 1 will mean multiple nodes going unavailable during
    // the update, which may affect your workload stress on the remaining nodes.
    // Drain will respect Pod Disruption Budgets (PDBs) such as etcd quorum guards,
    // even if maxUnavailable is greater than one.
    MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}
```

```yaml
apiVersion: v1
kind: SriovNetworkPoolConfig
metadata:
  name: pool-1
  namespace: network-operator
spec:
  maxUnavailable: "20%"
  nodeSelector:
    - matchExpressions:
      - key: some-label
        operator: In
        values:
          - val-2
    - matchExpressions:
      - key: other-label
        operator: "Exists"
```

Once this change will be implemented `SriovNetworkPoolConfig` configuration will be applied both to vanilla Kubernetes
and OpenShift clusters.

### Implementation Constraints

Node can only be part of one pool. if the node is not part of any node it will be allocated
to a virtual default pool with `maxUnavailable` of 1.

_*Note:*_ if you create a pool with empty selector it will match all the nodes, and you can not have another pool.

### Upgrade & Downgrade considerations
After operator upgrade we have to support `sriovnetwork.openshift.io/state` node annotation and `sriovnetwork.openshift.io/desired-state`
annotation in the `sriovNetworkNodeState`. in the future we are going to migrate to only using the annotation in the `sriovNetworkNodeState`

There is no change in upgrade from the user point of view.
If there is no pools or the node doesn't belong to any pool the `maxUnavailable` will be 1 to preserve the same functionality after upgrade.

_*Note:*_ no node should be in `Draining` or `MCP_Paused` state in the node annotation before the upgrade

### Alternative APIs
#### Option 1: extend SriovOperatorConfig CRD
We can extend SriovOperatorConfig CRD to include drain pools configuration. E.g.:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
name: default
namespace: network-operator
spec:
# Add fields here
enableInjector: false
enableOperatorWebhook: false
configDaemonNodeSelector: {}
disableDrain: false
drainConfig:
- name: default
  maxParallelNodeConfiguration: 1
  priority: 0 # the lowest priority
- name: pool-1
  maxParallelNodeConfiguration: 5
  priority: 44
  # empty nodeSelectorTerms means 'all nodes'
  nodeSelectorTerms:
  - matchExpressions:
    - key: some-label
      operator: In
      values:
      - val-1
      - val-2
  - matchExpressions:
    - key: other-label
      operator: "Exists"
```

We didn't choose this option because SriovOperatorConfig contains Config Daemon-specific options only while draing
configuration is node-specific.

#### Option 2:  New CRD
Add new `DrainConfiguration`CRD with fields mentioned in previous options.
We can extend SriovOperatorConfig CRD to include drain pools configuration. E.g.:
```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovDrainConfig
metadata:
  name: default
  namespace: network-operator
spec:
  maxParallelNodeConfiguration: 1
  priority: 0 # the lowest priority
  # empty nodeSelectorTerms means 'all nodes'
  nodeSelectorTerms:
  - matchExpressions:
  - key: some-label
  operator: In
```

We didn't choose this option because there is already defined `SriovNetworkPoolConfig` CRD wich could be uses for needed
configuration.

### Test Plan
* Unit tests will be implemented for new Drain Controller.
** E2E, manual or automation functional testing should have such test cases:
** to verify that we actually configure SR-IOV on `MaxParallelNodeConfiguration` nodes at the same time
** to check that we don't configure more than `MaxParallelNodeConfiguration` nodes at the same time
