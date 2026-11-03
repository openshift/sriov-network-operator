---
title: Kubernetes Conditions Integration for SR-IOV Network Operator CRDs
authors:
  - SR-IOV Network Operator Team
reviewers:
  - TBD
creation-date: 21-07-2025
last-updated: 28-09-2026
status: implemented
---

# Kubernetes Conditions Integration for SR-IOV Network Operator CRDs

## Summary

This proposal enhances the observability and operational transparency of the SR-IOV Network Operator by integrating standard Kubernetes conditions into the status of its key Custom Resource Definitions (CRDs). This enables users and automated systems to easily understand the current state, progress, and health of SR-IOV network configurations and components directly through Kubernetes API objects.

## Motivation

Adding Kubernetes conditions to the SR-IOV Network Operator's CRDs is crucial for several reasons:

* **Improved Observability:** Conditions provide a standardized, machine-readable way to convey the state of a resource, including its readiness, progress, and any encountered issues. This allows for better monitoring and debugging.

* **Enhanced User Experience:** Users can quickly ascertain the health and status of their `SriovNetwork`, `SriovIBNetwork`, `OVSNetwork`, `SriovNetworkNodeState`, `SriovOperatorConfig`, `SriovNetworkNodePolicy`, and `SriovNetworkPoolConfig` resources without needing to delve into logs or complex operator-specific status fields.

* **Standardized API Interaction:** Aligning with Kubernetes' best practices for API object status makes the SR-IOV operator more consistent with other Kubernetes operators and native resources, simplifying integration with existing tooling (e.g., `kubectl wait`, Prometheus alerts).

* **Automated Remediation and Orchestration:** External controllers or automation tools can reliably react to changes in resource conditions, enabling more robust and intelligent orchestration workflows and automated problem resolution.

* **Clearer Error Reporting:** Condition status, reason, and message identify the current outcome and explain failures or ongoing work.

* **Simplified Troubleshooting:** When a resource is not in the desired state, conditions can point directly to the reason, accelerating troubleshooting.

### Use Cases

1. **Network Resource Provisioning Status:**
   - A user creates a `SriovNetwork`, `SriovIBNetwork`, or `OVSNetwork` custom resource
   - Condition `Ready` is set to `True` once the network is successfully provisioned and ready for use by pods
   - Condition `Ready` is set to `False` with a reason if the network provisioning fails

2. **Node Configuration Health:**
   - The operator updates the `SriovNetworkNodeState` for a node
   - Condition `Progressing` when the operator is applying changes to the node's SR-IOV configuration
   - Condition `Ready` indicating the overall readiness of the SR-IOV components on that specific node
   - Condition `Draining` for tracking whether the node is currently undergoing a drain operation

3. **Operator Configuration Status:**
   - An administrator modifies the `SriovOperatorConfig`
   - Condition `Ready=True` indicates that the operator configuration reconciled successfully
   - Condition `Ready=False` carries the failure reason when reconciliation fails or the requested configuration is unsupported

4. **Policy Configuration Management:**
   - An administrator creates or updates a `SriovNetworkNodePolicy`
   - The only policy condition, `Ready`, is `True` when the current policy has been applied to every effective target node
   - `Ready=False` uses a specific reason and message to explain ongoing work, failure, or lack of matching nodes
   - Status includes `matchedNodeCount` and `readyNodeCount` for aggregated visibility

5. **Pool Configuration Management:**
   - An administrator creates or updates a `SriovNetworkPoolConfig`
   - The only pool condition, `Ready`, is `True` when the current pool configuration is effective for every target node
   - `Ready=False` uses a specific reason and message to explain rollout progress, pool conflicts, or failures
   - Regular-pool status includes `matchedNodeCount` and `readyNodeCount`; OpenShift hardware offload pools report unsupported readiness without counts

### Goals

* Add standard Kubernetes conditions to all major SR-IOV CRDs (`SriovNetwork`, `SriovIBNetwork`, `OVSNetwork`, `SriovNetworkNodeState`, `SriovOperatorConfig`, `SriovNetworkNodePolicy`, `SriovNetworkPoolConfig`)
* Implement consistent condition types across all CRDs where applicable
* Update conditions promptly as relevant resource states change
* Maintain backward compatibility with existing status fields
* Provide comprehensive documentation and examples for condition usage
* Enable `kubectl wait` functionality for all resources
* Add aggregated status for policy and pool resources showing matched/ready node counts

### Non-Goals

* Removing or renaming existing status fields (maintaining backward compatibility)
* Adding conditions to deprecated or legacy CRDs
* Implementing custom condition types beyond standard Kubernetes patterns
* Changing host configuration or drain behavior as part of status reporting

## Implementation

### Condition Types

The following condition types are used across SR-IOV CRDs. Policy and pool status use only `Ready`; NodeState also uses `Progressing` and `Draining` for separate node operations.

```go
const (
    // ConditionProgressing indicates that the resource is being actively reconciled
    ConditionProgressing = "Progressing"

    // ConditionReady indicates that the resource has reached its desired state and is fully functional
    ConditionReady = "Ready"

    // ConditionDraining indicates that the node is currently undergoing a drain operation
    ConditionDraining = "Draining"
)
```

### Condition Reasons

The following reasons are used across conditions:

```go
const (
    // Reasons for Ready condition
    ReasonNetworkReady         = "NetworkReady"
    ReasonNodeReady            = "NodeConfigurationReady"
    ReasonOperatorConfigReady  = "OperatorConfigReady"
    ReasonNotReady             = "NotReady"

    // Reasons for Network NotReady conditions
    ReasonNetworkAttachmentDefNotFound = "NetworkAttachmentDefinitionNotFound"
    ReasonNetworkAttachmentDefInvalid  = "NetworkAttachmentDefinitionInvalid"
    ReasonNamespaceNotFound            = "NamespaceNotFound"

    // Reasons for Operator Config NotReady conditions
    ReasonOperatorConfigSyncFailed     = "OperatorConfigSyncFailed"
    ReasonUnsupportedConfiguration     = "UnsupportedConfiguration"

    // Reasons shared by NodeState Progressing and aggregate Ready=False
    ReasonApplyingConfiguration = "ApplyingConfiguration"
    ReasonWaitingForDrain       = "WaitingForDrain"

    // Reason for NodeState Progressing=False
    ReasonNotProgressing        = "NotProgressing"

    // Reasons for Draining condition
    ReasonDrainingNode   = "DrainingNode"
    ReasonDrainCompleted = "DrainCompleted"
    ReasonDrainNotNeeded = "DrainNotNeeded"
    ReasonDrainPending   = "DrainPending"
    ReasonDrainFailed    = "DrainFailed"

    // Reasons for policy and pool Ready conditions
    ReasonPolicyReady                  = "PolicyReady"
    ReasonPoolConfigReady              = "PoolConfigReady"
    ReasonPolicyIgnored                = "PolicyIgnored"
    ReasonNoMatchingNodes             = "NoMatchingNodes"
    ReasonNoMatchingInterfaces        = "NoMatchingInterfaces"
    ReasonNodeSyncFailed              = "NodeSyncFailed"
    ReasonPoolConfigSyncFailed        = "PoolConfigSyncFailed"
    ReasonPoolConflict                = "PoolConflict"
    ReasonMachineConfigPoolNotFound   = "MachineConfigPoolNotFound"
    ReasonMachineConfigPoolUpdating   = "MachineConfigPoolUpdating"
    ReasonMachineConfigPoolDegraded   = "MachineConfigPoolDegraded"
    ReasonStatusUnknown               = "StatusUnknown"
)
```

Use stable, specific reasons across all condition types. `Ready=Unknown` uses `StatusUnknown` with a message explaining what cannot be observed. Do not use a generic `NotReady` reason when the cause is known.

### API Extensions

#### ConditionStatus (Shared by CRDs)

```go
// ConditionStatus defines the common observed state for SR-IOV operator CRDs
type ConditionStatus struct {
    // Conditions represent the latest available observations of the resource's state
    // +patchMergeKey=type
    // +patchStrategy=merge
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
```

#### SriovNetwork, SriovIBNetwork, OVSNetwork

The network CRDs embed `ConditionStatus`:

```go
type SriovNetworkStatus struct {
    ConditionStatus `json:",inline"`
}
```

`SriovOperatorConfigStatus` also embeds `ConditionStatus` to reuse the shared
conditions holder without network-specific naming.

**Conditions:**
- `Ready`: `True` when the NetworkAttachmentDefinition is created and the network is ready for use. `False` with the failure reason when provisioning fails, the target namespace does not exist, or the configuration is invalid.

Status updates use Server-Side Apply (SSA) to apply only the specific `Ready` condition entry. Because the conditions list uses `+listType=map` with `+listMapKey=type`, SSA merges each condition independently by its type key. This means different field managers can own different condition types on the same object without conflicting, and no retry logic is needed since SSA does not rely on `resourceVersion`.

**kubectl output columns:** Ready, Age

#### SriovNetworkNodeState

```go
type SriovNetworkNodeStateStatus struct {
    Interfaces    InterfaceExts `json:"interfaces,omitempty"`
    Bridges       Bridges       `json:"bridges,omitempty"`
    System        System        `json:"system,omitempty"`
    SyncStatus    string        `json:"syncStatus,omitempty"`
    LastSyncError string        `json:"lastSyncError,omitempty"`
    
    // Conditions represent the latest available observations of the SriovNetworkNodeState's state
    // +patchMergeKey=type
    // +patchStrategy=merge
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
```

**Conditions:**
- `Ready`: Node's SR-IOV configuration is complete. Device plugin pod readiness is independent of NodeState readiness.
- `Progressing`: Node is being configured (VF creation, driver loading, etc.)
- `Draining`: Node drain operation is in progress (`True`) or not (`False`). Reasons distinguish between `DrainingNode`, `DrainPending`, `DrainFailed`, `DrainCompleted`, and `DrainNotNeeded`.

**kubectl output columns:** Sync Status, Ready, Progressing, Draining, Desired Sync State, Current Sync State, Age

#### SriovOperatorConfig

```go
type SriovOperatorConfigStatus struct {
    Injector        string `json:"injector,omitempty"`
    OperatorWebhook string `json:"operatorWebhook,omitempty"`
    
    // Conditions represent the latest available observations of the SriovOperatorConfig's state
    // +patchMergeKey=type
    // +patchStrategy=merge
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
```

**Conditions:**
- `Ready`: `True` when `SriovOperatorConfig` reconciles successfully. `False` when reconciliation fails, with reason `OperatorConfigSyncFailed`, or when the requested configuration is unsupported, with reason `UnsupportedConfiguration`.

**kubectl output columns:** Ready, Age

#### SriovNetworkNodePolicy

```go
type SriovNetworkNodePolicyStatus struct {
    // MatchedNodeCount is the number of managed nodes selected by this policy.
    // Nil means the count has not yet been determined.
    MatchedNodeCount *int32 `json:"matchedNodeCount,omitempty"`

    // ReadyNodeCount is the number of matched nodes where this policy is applied.
    // Nil means the count has not yet been determined.
    ReadyNodeCount *int32 `json:"readyNodeCount,omitempty"`

    ConditionStatus `json:",inline"`
}
```

**Conditions:**
- `Ready=True` only when `matchedNodeCount > 0`, `readyNodeCount == matchedNodeCount`, and every matched node has a current NodeState `Ready=True` condition for the NodeState spec generation associated with this policy revision.
- `Ready=False` while configuration is being applied or a target node has failed, with the most useful current reason and a concise message. A policy with no managed nodes has reason `NoMatchingNodes`; a selected node with no NIC matched by `nicSelector` is not counted as ready and uses `NoMatchingInterfaces` when that is the blocker.
- `Ready=Unknown` when the controller lacks enough information to decide. `Progressing` and `Degraded` are not policy conditions.

`matchedNodeCount` counts nodes selected by `nodeSelector` within the operator's config-daemon node selection. It does not count unmanaged cluster nodes. The deprecated default policy is ignored by policy reconciliation and reports `Ready=False` with reason `PolicyIgnored`. When the policy controller renders a NodeState, it records the UID and generation of selected policies with matching NICs in a NodeState annotation, together with the rendered spec update. The status controller counts a target node only when this stamp matches the current policy and the NodeState `Ready=True` condition observes its current generation. Policy status does not inspect device plugin ConfigMap contents.

**kubectl output columns:** Matched (`matchedNodeCount`), Ready Nodes (`readyNodeCount`), Ready, Age. Separate count fields are exposed through [CRD printer-column JSONPaths](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#additional-printer-columns).

#### SriovNetworkPoolConfig

```go
type SriovNetworkPoolConfigStatus struct {
    // MatchedNodeCount counts managed nodes targeted by a regular pool.
    // Nil means unknown or not applicable to hardware offload MachineConfig pools.
    MatchedNodeCount *int32 `json:"matchedNodeCount,omitempty"`

    // ReadyNodeCount counts matched nodes where a regular pool is effective.
    // Nil means unknown or not applicable to hardware offload MachineConfig pools.
    ReadyNodeCount *int32 `json:"readyNodeCount,omitempty"`

    ConditionStatus `json:",inline"`
}
```

**Conditions:**
- For regular pools, `Ready=True` only when `matchedNodeCount > 0`, `readyNodeCount == matchedNodeCount`, and the current pool configuration is effective for every target node. Vanilla Kubernetes OVS configuration follows this NodeState-based flow, including named pools. OpenShift hardware-offload readiness is currently unsupported and has no node counts.
- `Ready=False` for incomplete rollout, overlapping pools, missing targets, unsupported configuration, or application failure. Reason and message identify the blocker. `Ready=Unknown` means the controller cannot determine the outcome. `Progressing` and `Degraded` are not pool conditions.

Pool readiness depends on its configuration path:

- For regular pools, target nodes come from `nodeSelector` within the managed node set. Reject overlapping pool membership with `Ready=False`, reason `PoolConflict`; do not count conflicting nodes as ready. When the pool sets RDMA or OVS system configuration, count a node only when its NodeState records the current pool UID and generation and reports `Ready=True` for its current spec generation.
- A pool that only sets `maxUnavailable` changes drain coordination, not host configuration. Count its selected nodes as ready once membership is unambiguous and the drain setting is accepted. Unrelated NodeState failures must not make this pool unready.
  Removing the last RDMA or OVS host setting switches to this drain-only readiness contract. `Ready=True` does not confirm that the daemon has finished processing removal of earlier host configuration.
- OpenShift hardware offload pools report `Ready=False` with `UnsupportedConfiguration`; `matchedNodeCount` and `readyNodeCount` are absent. Previously reported counts are cleared when a regular pool switches to this path. Vanilla Kubernetes pools, including named hardware-offload pools, use the regular NodeState readiness and node counts. Aggregate status does not read or watch MachineConfigs or MachineConfigPools.

TODO: Support OpenShift hardware-offload readiness after removing MachineConfig-related configuration and using the same flow as vanilla Kubernetes.

Regular-pool target selection and ready-node calculation must use the same managed-node scope.

**kubectl output columns:** Matched (`matchedNodeCount`), Ready Nodes (`readyNodeCount`), Ready, Age.

#### Aggregation and freshness

- Calculate counts and the `Ready` condition from the same observation, then write them together. Set `Ready.observedGeneration` to the policy or pool generation evaluated for those counts. A new spec must not inherit `Ready=True` from an older generation. [`kubectl wait --for=condition=Ready`](https://github.com/kubernetes/kubectl/blob/master/pkg/cmd/wait/condition.go) uses the condition's `observedGeneration` to reject stale conditions. No separate status-level `observedGeneration` field is needed.
- Keep counts absent until known; zero means a complete observation found zero nodes. If dependencies cannot be read, report `Ready=Unknown` and clear counts rather than presenting stale data as current.
- Count a NodeState only when its `Ready=True` condition reflects its applied spec generation and its source revision stamp matches the current policy or pool. A ready NodeState can represent an earlier source revision. Source stamps live in NodeState metadata, so source edits that render the same spec do not force daemon reconfiguration. NodeState status writing must preserve the generation of the configuration actually applied, including when its spec changes during reconciliation.
- Recalculate after policy, pool, Node, or NodeState changes. A single aggregate status reconciler lists nodes and NodeStates once per event. NodeState watches ignore host-status churn that cannot change policy or pool Ready. Regular-pool selectors and memberships are calculated once per observation. Periodic resync covers missed events. Skip unchanged status writes.
- Keep status size independent of target-node count: store counts, not node-name lists. [Kubernetes custom resource guidance](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/) cautions against large objects. For policies and regular pools, use `Ready.message` for a short count summary and at most three affected node names for its reason, sorted for stable output. Hardware offload messages explain unsupported readiness without counts or node names. Detailed per-node failures remain on NodeStates. If users need a complete policy-specific node list, provide queryable per-node attribution rather than an unbounded aggregate status field.

### Implementation Details

#### Status Patcher Package (pkg/status)

The `pkg/status` package provides utilities for applying conditions to CRD status subresources using Server-Side Apply:

```go
// Interface provides methods for applying resource status using Server-Side Apply
// and emitting events for condition transitions.
type Interface interface {
    // ApplyCondition applies one or more conditions to the status subresource
    // using Server-Side Apply. Only the specified conditions are included in the
    // apply configuration, so the field manager only claims ownership of those
    // specific condition entries (keyed by type).
    ApplyCondition(ctx context.Context, obj client.Object, conditions ...metav1.Condition) error

    // ApplyStatus writes aggregate status fields and owned conditions together.
    ApplyStatus(ctx context.Context, obj client.Object, statusFields map[string]interface{}, ownedConditions []metav1.Condition) error
}

// NewCondition creates a new metav1.Condition with the given parameters.
func NewCondition(conditionType string, s metav1.ConditionStatus, reason, message string, generation int64) metav1.Condition
```

Internally, `ApplyCondition` builds a minimal unstructured apply configuration containing only the object identity (GVK, name, namespace) and the specific conditions being applied. `ApplyStatus` includes the supplied status fields and owned conditions in one status write. Both use `client.Status().Apply()`, `client.FieldOwner`, and `client.ForceOwnership`. Since SSA does not use `resourceVersion`, no retry-on-conflict logic is needed.

#### Transition Detection

The package includes transition detection for automatic event emission:

```go
// TransitionType represents the type of condition transition
type TransitionType string

const (
    TransitionAdded     TransitionType = "Added"
    TransitionChanged   TransitionType = "Changed"
    TransitionRemoved   TransitionType = "Removed"
    TransitionUnchanged TransitionType = "Unchanged"
)

// DetectTransitions compares old and new conditions and returns a list of transitions
func DetectTransitions(oldConditions, newConditions []metav1.Condition) []Transition
```

#### Controller Integration

Each controller uses the status patcher to apply conditions:

**Network Controllers (SriovNetwork, SriovIBNetwork, OVSNetwork):**
- Apply `Ready=True` with reason `NetworkReady` on successful NAD provisioning
- Apply `Ready=False` with reason `NetworkAttachmentDefinitionInvalid` when provisioning fails or the target namespace does not exist
- No re-fetch of the object is needed before applying; SSA only requires the object identity

**SriovOperatorConfig Controller:**
- Set `Ready=True` with reason `OperatorConfigReady` on successful reconciliation
- Set `Ready=False` with reason `OperatorConfigSyncFailed` on reconciliation or apply failures
- Set `Ready=False` with reason `UnsupportedConfiguration` for unsupported settings such as HyperShift with `systemd` configuration mode

**Config Daemon:**
- Updates `SriovNetworkNodeState` conditions during sync operations
- Sets configuration conditions based on sync status

**Drain Controller:**
- Sets the `Draining` condition during drain operations
- Updates `Draining` with appropriate reasons based on drain state (`DrainingNode`, `DrainPending`, `DrainFailed`, `DrainCompleted`, `DrainNotNeeded`)

**Status Controllers (Policy and PoolConfig):**
- Reconcile one `Ready` condition and node counts per resource using `ApplyStatus`
- Check policy revision stamps and current NodeState `Ready` conditions; do not copy a NodeState condition from an earlier policy revision
- Use pool-specific NodeState and drain-coordination evidence for supported configuration paths
- Watch policy, pool, Node, and NodeState changes

### Usage Examples

```bash
# Check network status
kubectl get sriovnetwork -o wide
NAME          READY   AGE
my-network    True    5m

# Wait for network to be ready
kubectl wait --for=condition=Ready sriovnetwork/my-network --timeout=60s

# Check policy status with node counts
kubectl get sriovnetworknodepolicy -o wide
NAME        MATCHED   READY NODES   READY   AGE
policy1     3         2             False   10m

# Check pool config status
kubectl get sriovnetworkpoolconfig -o wide
NAME          MATCHED   READY NODES   READY   AGE
worker-pool   5         5             True    15m

# Check node state with all conditions
kubectl get sriovnetworknodestate -o wide
NAME      SYNC STATUS   READY   PROGRESSING   DRAINING   DESIRED SYNC STATE   CURRENT SYNC STATE   AGE
worker1   Succeeded     True    False         False      Idle                 Idle                 1h
```

### Backward Compatibility

* Existing status fields are preserved
* Conditions are added as optional fields
* Controllers continue to update legacy status fields alongside conditions
* Client code relying on existing status fields is not affected
* New policy and pool status fields do not change spec or host configuration behavior

### Error Handling

* Network condition updates use Server-Side Apply, which eliminates `resourceVersion` conflicts and requires no retry logic
* Failed condition-only updates are logged but don't cause reconciliation failure
* Each field manager only claims ownership of the specific condition types it applies, avoiding conflicts between controllers
* Policy and pool status updates retry transient failures so counts and `Ready` do not remain stale indefinitely

### Upgrade & Downgrade Considerations

#### Upgrade
* New CRD versions with condition fields are backward compatible
* Existing CR instances continue to function without conditions
* Controllers populate conditions after upgrade
* No manual intervention required from users

#### Downgrade
* Conditions are ignored by older controller versions
* Existing status fields continue to be populated
* No data loss or functionality degradation during downgrade
* CRD structure remains compatible with older API versions

## Testing

Testing covers condition handling, the status patcher, controller condition updates, and policy and pool aggregation. Required coverage includes:

* Unit tests for condition setting, status patching, and transition detection across all supported CRDs
* Integration tests for controller condition updates and aggregation
* E2E tests for `Ready` conditions and `kubectl wait` on all supported CRDs
* `Ready` reason and count transitions for pending, successful, failed, and zero-target resources
* Absent versus zero counts, count consistency, and bounded condition messages
* Policy changes, policy overlap, NIC selector misses, and stale NodeState generations
* Regular and drain-only pool counts, including overlapping pools and invalid selectors
* Unsupported OpenShift hardware-offload conditions without counters, including clearing counters after switching from a regular pool
* Aggregate controller startup on Kubernetes without MachineConfig CRDs
* `kubectl wait` behavior after a new policy or pool generation
