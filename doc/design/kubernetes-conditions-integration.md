---
title: Kubernetes Conditions Integration for SR-IOV Network Operator CRDs
authors:
  - SR-IOV Network Operator Team
reviewers:
  - TBD
creation-date: 21-07-2025
last-updated: 08-01-2026
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

* **Clearer Error Reporting:** Specific conditions can indicate different types of errors (e.g., `Degraded`, `Ready`, `Progressing`), providing more granular insight into failures.

* **Simplified Troubleshooting:** When a resource is not in the desired state, conditions can point directly to the reason, accelerating troubleshooting.

### Use Cases

1. **Network Resource Provisioning Status:**
   - A user creates a `SriovNetwork`, `SriovIBNetwork`, or `OVSNetwork` custom resource
   - Condition `Ready` is set to `True` once the network is successfully provisioned and ready for use by pods
   - Condition `Ready` is set to `False` with a reason if the network provisioning fails

2. **Node Configuration Health:**
   - The operator updates the `SriovNetworkNodeState` for a node
   - Condition `Progressing` when the operator is applying changes to the node's SR-IOV configuration
   - Condition `Degraded` if a node's SR-IOV configuration is incorrect
   - Condition `Ready` indicating the overall readiness of the SR-IOV components on that specific node
   - Drain-specific conditions (`DrainProgressing`, `DrainDegraded`, `DrainComplete`) for tracking drain operations

3. **Operator Configuration Status:**
   - An administrator modifies the `SriovOperatorConfig`
   - Condition `Ready` indicates that the operator's components are running and healthy
   - Condition `Degraded` if the operator itself encounters issues

4. **Policy Configuration Management:**
   - An administrator creates or updates a `SriovNetworkNodePolicy`
   - Condition `Ready` indicates that the policy configuration has been successfully applied to all target nodes
   - Condition `Progressing` when the policy configuration is being applied to the selected nodes
   - Condition `Degraded` if any matched nodes are in a degraded state
   - Status includes `matchedNodeCount` and `readyNodeCount` for aggregated visibility

5. **Pool Configuration Management:**
   - An administrator creates or updates a `SriovNetworkPoolConfig`
   - Condition `Ready` indicates that the pool configuration has been successfully applied to all target nodes
   - Condition `Progressing` when the pool configuration is being applied to the selected nodes
   - Condition `Degraded` if the pool configuration fails to apply or conflicts with existing configurations
   - Status includes `matchedNodeCount` and `readyNodeCount` for aggregated visibility

### Goals

* Add standard Kubernetes conditions to all major SR-IOV CRDs (`SriovNetwork`, `SriovIBNetwork`, `OVSNetwork`, `SriovNetworkNodeState`, `SriovOperatorConfig`, `SriovNetworkNodePolicy`, `SriovNetworkPoolConfig`)
* Implement consistent condition types across all CRDs where applicable
* Ensure conditions are updated in real-time as resource states change
* Maintain backward compatibility with existing status fields
* Provide comprehensive documentation and examples for condition usage
* Enable `kubectl wait` functionality for all resources
* Add aggregated status for policy and pool resources showing matched/ready node counts

### Non-Goals

* Modifying existing status field structures (maintaining backward compatibility)
* Adding conditions to deprecated or legacy CRDs
* Implementing custom condition types beyond standard Kubernetes patterns
* Changing existing controller reconciliation logic beyond condition updates

## Implementation

### Condition Types

The following condition types are implemented across SR-IOV CRDs:

```go
const (
    // ConditionProgressing indicates that the resource is being actively reconciled
    ConditionProgressing = "Progressing"
    
    // ConditionDegraded indicates that the resource is not functioning as expected
    ConditionDegraded = "Degraded"
    
    // ConditionReady indicates that the resource has reached its desired state and is fully functional
    ConditionReady = "Ready"

    // Drain-specific conditions for SriovNetworkNodeState
    
    // ConditionDrainProgressing indicates that the node is being actively drained
    ConditionDrainProgressing = "DrainProgressing"

    // ConditionDrainDegraded indicates that the drain process is not functioning as expected
    ConditionDrainDegraded = "DrainDegraded"

    // ConditionDrainComplete indicates that the drain operation completed successfully
    ConditionDrainComplete = "DrainComplete"
)
```

### Condition Reasons

The following reasons are used across conditions:

```go
const (
    // Reasons for Ready condition
    ReasonNetworkReady   = "NetworkReady"
    ReasonNodeReady      = "NodeConfigurationReady"
    ReasonNodeDrainReady = "NodeDrainReady"
    ReasonOperatorReady  = "OperatorReady"
    ReasonNotReady       = "NotReady"

    // Reasons for Degraded condition
    ReasonProvisioningFailed           = "ProvisioningFailed"
    ReasonConfigurationFailed          = "ConfigurationFailed"
    ReasonNetworkAttachmentDefNotFound = "NetworkAttachmentDefinitionNotFound"
    ReasonNetworkAttachmentDefInvalid  = "NetworkAttachmentDefinitionInvalid"
    ReasonNamespaceNotFound            = "NamespaceNotFound"
    ReasonHardwareError                = "HardwareError"
    ReasonDriverError                  = "DriverError"
    ReasonOperatorComponentsNotHealthy = "OperatorComponentsNotHealthy"
    ReasonNotDegraded                  = "NotDegraded"

    // Reasons for Progressing condition
    ReasonConfiguringNode       = "ConfiguringNode"
    ReasonApplyingConfiguration = "ApplyingConfiguration"
    ReasonCreatingVFs           = "CreatingVFs"
    ReasonLoadingDriver         = "LoadingDriver"
    ReasonDrainingNode          = "DrainingNode"
    ReasonNotProgressing        = "NotProgressing"

    // Reasons for DrainComplete condition
    ReasonDrainCompleted = "DrainCompleted"
    ReasonDrainNotNeeded = "DrainNotNeeded"
    ReasonDrainPending   = "DrainPending"

    // Reasons for DrainDegraded condition
    ReasonDrainFailed = "DrainFailed"

    // Reasons for Policy/PoolConfig conditions
    ReasonPolicyReady                = "PolicyReady"
    ReasonPolicyNotReady             = "PolicyNotReady"
    ReasonNoMatchingNodes            = "NoMatchingNodes"
    ReasonPartiallyApplied           = "PartiallyApplied"
    ReasonAllNodesConfigured         = "AllNodesConfigured"
    ReasonSomeNodesFailed            = "SomeNodesFailed"
    ReasonSomeNodesProgressing       = "SomeNodesProgressing"
)
```

### API Extensions

#### NetworkStatus (Shared by Network CRDs)

```go
// NetworkStatus defines the common observed state for network-type CRDs
type NetworkStatus struct {
    // Conditions represent the latest available observations of the network's state
    // +patchMergeKey=type
    // +patchStrategy=merge
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
```

#### SriovNetwork, SriovIBNetwork, OVSNetwork

All network CRDs embed `NetworkStatus`:

```go
type SriovNetworkStatus struct {
    NetworkStatus `json:",inline"`
}
```

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
- `Ready`: Node's SR-IOV configuration is complete and functional
- `Progressing`: Node is being configured (VF creation, driver loading, node draining, etc.)
- `Degraded`: Node configuration failed or hardware issues detected
- `DrainProgressing`: Node drain operation is in progress
- `DrainDegraded`: Node drain operation encountered errors (e.g., PDB violations)
- `DrainComplete`: Node drain operation completed successfully

**kubectl output columns:** Sync Status, Ready, Progressing, Degraded, DrainProgress, DrainDegraded, DrainComplete, Age

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
- `Ready`: Operator components are running and healthy
- `Degraded`: Operator components are failing or misconfigured

**kubectl output columns:** Ready, Progressing, Degraded, Age

#### SriovNetworkNodePolicy

```go
type SriovNetworkNodePolicyStatus struct {
    // MatchedNodeCount is the number of nodes that match the nodeSelector for this policy
    MatchedNodeCount int `json:"matchedNodeCount"`

    // ReadyNodeCount is the number of matched nodes that have successfully applied the configuration
    ReadyNodeCount int `json:"readyNodeCount"`

    // Conditions represent the latest available observations of the SriovNetworkNodePolicy's state
    // +patchMergeKey=type
    // +patchStrategy=merge
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
```

**Conditions:**
- `Ready`: True only when ALL matched nodes are ready
- `Progressing`: True when ANY matched node is progressing
- `Degraded`: True when ANY matched node is degraded

**kubectl output columns:** Matched, Ready Nodes, Ready, Progressing, Degraded, Age

#### SriovNetworkPoolConfig

```go
type SriovNetworkPoolConfigStatus struct {
    // MatchedNodeCount is the number of nodes that match the nodeSelector for this pool
    MatchedNodeCount int `json:"matchedNodeCount"`

    // ReadyNodeCount is the number of matched nodes that have successfully applied the configuration
    ReadyNodeCount int `json:"readyNodeCount"`

    // Conditions represent the latest available observations of the SriovNetworkPoolConfig's state
    // +patchMergeKey=type
    // +patchStrategy=merge
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
```

**Conditions:**
- `Ready`: Pool configuration has been successfully applied to all target nodes
- `Progressing`: Pool configuration is being applied to selected nodes
- `Degraded`: Pool configuration failed to apply or conflicts with existing configurations

**kubectl output columns:** Matched, Ready Nodes, Ready, Progressing, Degraded, Age

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
}

// NewCondition creates a new metav1.Condition with the given parameters.
func NewCondition(conditionType string, s metav1.ConditionStatus, reason, message string, generation int64) metav1.Condition
```

Internally, `ApplyCondition` builds a minimal unstructured apply configuration containing only the object identity (GVK, name, namespace) and the specific conditions being applied. This is wrapped via `client.ApplyConfigurationFromUnstructured` and applied with `client.Status().Apply()`, using `client.FieldOwner` and `client.ForceOwnership`. Since SSA does not use `resourceVersion`, no retry-on-conflict logic is needed.

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
- Set `Ready=True, Degraded=False` on successful reconciliation
- Set `Ready=False, Degraded=True` on component failures

**Config Daemon:**
- Updates `SriovNetworkNodeState` conditions during sync operations
- Sets configuration conditions based on sync status

**Drain Controller:**
- Sets drain-specific conditions during drain operations
- Updates `DrainProgressing`, `DrainDegraded`, `DrainComplete` based on drain state

**Status Controllers (Policy and PoolConfig):**
- Dedicated controllers aggregate conditions from matching `SriovNetworkNodeState` objects
- Watches changes to NodeStates and Nodes to update aggregated status

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
NAME        MATCHED   READY NODES   READY   PROGRESSING   DEGRADED   AGE
policy1     3         3             True    False         False      10m

# Check pool config status
kubectl get sriovnetworkpoolconfig -o wide
NAME          MATCHED   READY NODES   READY   PROGRESSING   DEGRADED   AGE
worker-pool   5         5             True    False         False      15m

# Check node state with all conditions
kubectl get sriovnetworknodestate -o wide
NAME      SYNC STATUS   READY   PROGRESSING   DEGRADED   DRAINPROGRESS   DRAINDEGRADED   DRAINCOMPLETE   AGE
worker1   Succeeded     True    False         False      False           False           True            1h
```

### Backward Compatibility

* Existing status fields are preserved
* Conditions are added as optional fields
* Controllers continue to update legacy status fields alongside conditions
* Client code relying on existing status fields is not affected

### Error Handling

* Network condition updates use Server-Side Apply, which eliminates `resourceVersion` conflicts and requires no retry logic
* Failed condition updates are logged but don't cause reconciliation failure
* Each field manager only claims ownership of the specific condition types it applies, avoiding conflicts between controllers

### Upgrade & Downgrade Considerations

#### Upgrade
* New CRD versions with condition fields are backward compatible
* Existing CR instances continue to function without conditions
* Controllers start populating conditions immediately after upgrade
* No manual intervention required from users

#### Downgrade
* Conditions are ignored by older controller versions
* Existing status fields continue to be populated
* No data loss or functionality degradation during downgrade
* CRD structure remains compatible with older API versions

## Testing

The implementation includes:

* **Unit tests** for all condition handling logic
* **Unit tests** for status patcher and transition detection
* **Integration tests** for controller condition updates
* **E2E conformance tests** validating conditions in real cluster scenarios
