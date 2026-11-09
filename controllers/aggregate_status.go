package controllers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/status"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const aggregateStatusSyncEventName = "aggregate-status-sync-event"

// AggregateStatusReconciler publishes Ready counts for all policies and pools.
type AggregateStatusReconciler struct {
	client.Client
	StatusPatcher status.Interface
}

type aggregateResult struct {
	matched   *int32
	ready     *int32
	condition metav1.Condition
}

type aggregateIssue struct {
	reason string
	node   string
}

type poolMemberships struct {
	nodes  map[string][]corev1.Node
	counts map[string]int
	errors map[string]error
}

// Reconcile recalculates policy and pool Ready status from one cluster snapshot.
func (r *AggregateStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Name != aggregateStatusSyncEventName || req.Namespace != "" {
		return ctrl.Result{}, nil
	}

	// get all policies
	policies := &sriovnetworkv1.SriovNetworkNodePolicyList{}
	if err := r.List(ctx, policies); err != nil {
		return ctrl.Result{}, err
	}

	// get all pools
	pools := &sriovnetworkv1.SriovNetworkPoolConfigList{}
	if err := r.List(ctx, pools); err != nil {
		return ctrl.Result{}, err
	}
	nodes, nodesErr := managedNodes(ctx, r.Client)
	states, statesErr := nodeStatesByName(ctx, r.Client)

	for i := range policies.Items {
		policy := &policies.Items[i]
		result := policyStatusFrom(policy, nodes, nodesErr, states, statesErr)
		if err := applyAggregateStatus(ctx, r.StatusPatcher, policy, policy.Status.MatchedNodeCount, policy.Status.ReadyNodeCount, policy.Status.Conditions, result); err != nil {
			return ctrl.Result{}, err
		}
	}

	memberships := selectPoolNodes(pools.Items, nodes)

	for i := range pools.Items {
		pool := &pools.Items[i]
		var result aggregateResult
		// TODO: remove this after we migrate the hw offload config for openshift to not use machineconfigs
		if vars.ClusterType == constants.ClusterTypeOpenshift && pool.Spec.OvsHardwareOffloadConfig != nil && pool.Spec.OvsHardwareOffloadConfig.Name != "" {
			result = offloadPoolStatus(pool)
		} else {
			result = regularPoolStatusFrom(pool, nodesErr, states, statesErr, memberships)
		}
		if err := applyAggregateStatus(ctx, r.StatusPatcher, pool, pool.Status.MatchedNodeCount, pool.Status.ReadyNodeCount, pool.Status.Conditions, result); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: constants.ResyncPeriod}, nil
}

// SetupWithManager registers a single coalesced watch for policy and pool status.
func (r *AggregateStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	syncRequests := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []ctrl.Request {
		return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: aggregateStatusSyncEventName}}}
	})
	generationChanged := builder.WithPredicates(predicate.GenerationChangedPredicate{})
	controller := ctrl.NewControllerManagedBy(mgr).
		Named("SriovAggregateStatus").
		Watches(&sriovnetworkv1.SriovNetworkNodePolicy{}, syncRequests, generationChanged).
		Watches(&sriovnetworkv1.SriovNetworkPoolConfig{}, syncRequests, generationChanged).
		Watches(&corev1.Node{}, syncRequests, builder.WithPredicates(predicate.Funcs{UpdateFunc: aggregateNodeChanged})).
		Watches(&sriovnetworkv1.SriovNetworkNodeState{}, syncRequests, builder.WithPredicates(predicate.Funcs{UpdateFunc: aggregateNodeStateChanged})).
		Watches(&sriovnetworkv1.SriovOperatorConfig{}, syncRequests, generationChanged)

	return controller.Complete(r)
}

// aggregateKnown builds a Ready result with known node counts.
func aggregateKnown(generation int64, matched, ready int32, reason, message string, isReady bool) aggregateResult {
	conditionStatus := metav1.ConditionFalse
	if isReady {
		conditionStatus = metav1.ConditionTrue
	}
	return aggregateResult{matched: &matched, ready: &ready,
		condition: status.NewCondition(sriovnetworkv1.ConditionReady, conditionStatus, reason, message, generation)}
}

// aggregateUnknown reports an observation failure without node counts.
func aggregateUnknown(generation int64, message string) aggregateResult {
	return aggregateResult{condition: status.NewCondition(sriovnetworkv1.ConditionReady,
		metav1.ConditionUnknown, sriovnetworkv1.ReasonStatusUnknown, message, generation)}
}

// aggregateBlocked reports a configuration blocker without node counts.
func aggregateBlocked(generation int64, reason, message string) aggregateResult {
	return aggregateResult{condition: status.NewCondition(sriovnetworkv1.ConditionReady,
		metav1.ConditionFalse, reason, message, generation)}
}

// aggregateUnchanged reports whether counts and the Ready condition already match.
func aggregateUnchanged(matched, ready *int32, conditions []metav1.Condition, result aggregateResult) bool {
	current := meta.FindStatusCondition(conditions, sriovnetworkv1.ConditionReady)
	return ptr.Equal(matched, result.matched) && ptr.Equal(ready, result.ready) &&
		current != nil && sriovnetworkv1.ConditionsEqual([]metav1.Condition{*current}, []metav1.Condition{result.condition})
}

// aggregateFields builds the count fields owned by aggregate status.
func aggregateFields(result aggregateResult) map[string]interface{} {
	// SSA drops previously owned fields when they are omitted from the next apply.
	fields := map[string]interface{}{}
	if result.matched != nil {
		fields["matchedNodeCount"] = *result.matched
	}
	if result.ready != nil {
		fields["readyNodeCount"] = *result.ready
	}
	return fields
}

// applyAggregateStatus writes counts and Ready when they differ from the live object.
func applyAggregateStatus(ctx context.Context, patcher status.Interface, obj client.Object, matched, ready *int32, conditions []metav1.Condition, result aggregateResult) error {
	if aggregateUnchanged(matched, ready, conditions, result) {
		return nil
	}
	return patcher.ApplyStatus(ctx, obj, aggregateFields(result), []metav1.Condition{result.condition})
}

// issueReason selects the highest-priority reason among node issues.
func issueReason(issues []aggregateIssue) string {
	for _, reason := range sriovnetworkv1.AggregateReasonPriority {
		for _, issue := range issues {
			if issue.reason == reason {
				return reason
			}
		}
	}
	return sriovnetworkv1.ReasonApplyingConfiguration
}

// aggregateMessage summarizes readiness and names nodes with the selected issue.
func aggregateMessage(ready, matched int32, issues []aggregateIssue, reason string) string {
	message := fmt.Sprintf("%d/%d target nodes ready", ready, matched)
	if len(issues) == 0 {
		return message
	}
	names := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.reason != reason {
			continue
		}
		names = append(names, issue.node)
	}
	if len(names) == 0 {
		return message
	}
	sort.Strings(names)
	if len(names) > 3 {
		names = names[:3]
	}
	if reason == sriovnetworkv1.ReasonNoMatchingInterfaces {
		return message + "; no interfaces match the policy NIC selector on: " + strings.Join(names, ", ")
	}
	return message + "; affected: " + strings.Join(names, ", ")
}

// aggregateNodeChanged detects node label changes that can affect membership.
func aggregateNodeChanged(e event.UpdateEvent) bool {
	oldNode, okOld := e.ObjectOld.(*corev1.Node)
	newNode, okNew := e.ObjectNew.(*corev1.Node)
	if !okOld || !okNew {
		return true
	}
	return !equality.Semantic.DeepEqual(oldNode.Labels, newNode.Labels)
}

// aggregateNodeStateChanged detects NodeState changes that can affect readiness.
func aggregateNodeStateChanged(e event.UpdateEvent) bool {
	oldState, okOld := e.ObjectOld.(*sriovnetworkv1.SriovNetworkNodeState)
	newState, okNew := e.ObjectNew.(*sriovnetworkv1.SriovNetworkNodeState)
	if !okOld || !okNew {
		return true
	}
	return oldState.AffectsAggregate(newState)
}

// policyStatusFrom computes policy Ready from a preloaded node and NodeState snapshot.
func policyStatusFrom(policy *sriovnetworkv1.SriovNetworkNodePolicy, nodes []corev1.Node, nodesErr error, states map[string]*sriovnetworkv1.SriovNetworkNodeState, statesErr error) aggregateResult {
	if policy.Name == constants.DefaultPolicyName {
		return aggregateKnown(policy.Generation, 0, 0, sriovnetworkv1.ReasonPolicyIgnored,
			"Deprecated default policy is ignored", false)
	}
	if nodesErr != nil {
		return aggregateUnknown(policy.Generation, "Cannot list managed nodes or operator configuration: "+nodesErr.Error())
	}
	if statesErr != nil {
		return aggregateUnknown(policy.Generation, "Cannot list node states: "+statesErr.Error())
	}
	var matchedNodeCount, readyNodeCount int32
	var issues []aggregateIssue
	for i := range nodes {
		node := &nodes[i]
		if !policy.Selected(node) {
			continue
		}
		matchedNodeCount++

		// this is for the case where the nodeState is not yet created
		state, found := states[node.Name]
		if !found {
			issues = append(issues, aggregateIssue{sriovnetworkv1.ReasonApplyingConfiguration, node.Name})
			continue
		}

		// this is for the case where the nodeState is not yet populated
		if len(state.Status.Interfaces) == 0 {
			reason := sriovnetworkv1.ReasonApplyingConfiguration
			if state.IsReady() {
				reason = sriovnetworkv1.ReasonNoMatchingInterfaces
			}
			issues = append(issues, aggregateIssue{reason, node.Name})
			continue
		}

		if !policy.MatchesInterfaceInNodeState(state) {
			issues = append(issues, aggregateIssue{sriovnetworkv1.ReasonNoMatchingInterfaces, node.Name})
			continue
		}

		if !policy.RevisionMatchesInNodeState(state) {
			issues = append(issues, aggregateIssue{sriovnetworkv1.ReasonApplyingConfiguration, node.Name})
			continue
		}

		if !state.IsReady() {
			issues = append(issues, aggregateIssue{state.FailureReason(), node.Name})
			continue
		}

		readyNodeCount++
	}
	return aggregateNodes(policy.Generation, matchedNodeCount, readyNodeCount, issues, sriovnetworkv1.ReasonPolicyReady)
}

// selectPoolNodes evaluates regular-pool membership once for the whole snapshot.
func selectPoolNodes(pools []sriovnetworkv1.SriovNetworkPoolConfig, nodes []corev1.Node) poolMemberships {
	result := poolMemberships{
		nodes: map[string][]corev1.Node{}, counts: map[string]int{}, errors: map[string]error{},
	}
	for i := range pools {
		pool := &pools[i]

		// TODO: remove this after we migrate the hw offload config for openshift to not use machineconfigs
		if vars.ClusterType == constants.ClusterTypeOpenshift && pool.Spec.OvsHardwareOffloadConfig != nil && pool.Spec.OvsHardwareOffloadConfig.Name != "" {
			continue
		}

		// get the nodes that match the pool selector
		selector, err := pool.GetNodeSelector()
		if err != nil {
			result.errors[pool.Name] = err
			continue
		}
		for _, node := range nodes {
			if selector.Matches(labels.Set(node.Labels)) {
				result.nodes[pool.Name] = append(result.nodes[pool.Name], node)
				result.counts[node.Name]++
			}
		}
	}
	return result
}

// regularPoolStatusFrom computes regular-pool Ready from a preloaded snapshot.
func regularPoolStatusFrom(pool *sriovnetworkv1.SriovNetworkPoolConfig, nodesErr error, states map[string]*sriovnetworkv1.SriovNetworkNodeState, statesErr error, memberships poolMemberships) aggregateResult {
	if nodesErr != nil {
		return aggregateUnknown(pool.Generation, "Cannot list managed nodes or operator configuration: "+nodesErr.Error())
	}
	if err := memberships.errors[pool.Name]; err != nil {
		return aggregateBlocked(pool.Generation, sriovnetworkv1.ReasonPoolConfigSyncFailed,
			"Invalid pool selector: "+err.Error())
	}
	if len(memberships.errors) > 0 {
		return aggregateUnknown(pool.Generation, "Cannot determine pool conflicts: another pool has an invalid selector")
	}
	var matched, ready int32
	var issues []aggregateIssue
	needsHostConfig := pool.HasHostConfigurations()
	for _, node := range memberships.nodes[pool.Name] {
		matched++
		if memberships.counts[node.Name] > 1 {
			issues = append(issues, aggregateIssue{sriovnetworkv1.ReasonPoolConflict, node.Name})
			continue
		}
		if !needsHostConfig {
			// shortcut: removal is not acknowledged
			// TODO: track completion if cleanup readiness is required.
			ready++
			continue
		}
		if statesErr != nil {
			return aggregateUnknown(pool.Generation, "Cannot list node states: "+statesErr.Error())
		}
		state, found := states[node.Name]
		if !found {
			issues = append(issues, aggregateIssue{sriovnetworkv1.ReasonApplyingConfiguration, node.Name})
			continue
		}
		if !pool.RevisionMatchesInObject(state) {
			issues = append(issues, aggregateIssue{sriovnetworkv1.ReasonApplyingConfiguration, node.Name})
			continue
		}
		if !state.IsReady() {
			issues = append(issues, aggregateIssue{state.FailureReason(), node.Name})
			continue
		}
		ready++
	}
	return aggregateNodes(pool.Generation, matched, ready, issues, sriovnetworkv1.ReasonPoolConfigReady)
}

// aggregateNodes derives Ready from node counts and outstanding issues.
func aggregateNodes(generation int64, matched, ready int32, issues []aggregateIssue, readyReason string) aggregateResult {
	reason := readyReason
	isReady := matched > 0 && ready == matched
	if matched == 0 {
		reason = sriovnetworkv1.ReasonNoMatchingNodes
	} else if !isReady {
		reason = issueReason(issues)
	}
	return aggregateKnown(generation, matched, ready, reason, aggregateMessage(ready, matched, issues, reason), isReady)
}

// TODO: Support OpenShift hardware-offload readiness after MachineConfig is removed and it uses the vanilla Kubernetes flow.
func offloadPoolStatus(pool *sriovnetworkv1.SriovNetworkPoolConfig) aggregateResult {
	return aggregateBlocked(pool.Generation, sriovnetworkv1.ReasonUnsupportedConfiguration,
		"Hardware offload readiness is not supported yet")
}
