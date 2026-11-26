/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	sriovdrav1alpha1 "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/api/sriovdra/v1alpha1"
	configapi "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/api/virtualfunction/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type draPolicyResource struct {
	resourceName    string
	deviceClassName string
	driver          string
}

// validatedDRAPolicyResources returns a deterministic resource list only when
// the entire policy set is compatible. All DRA sync paths use this validation.
func validatedDRAPolicyResources(pl *sriovnetworkv1.SriovNetworkNodePolicyList) ([]draPolicyResource, error) {
	resourceDrivers, err := sriovnetworkv1.PolicyResourceDrivers(pl.Items)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resourceDrivers))
	for name := range resourceDrivers {
		names = append(names, name)
	}
	sort.Strings(names)
	resources := make([]draPolicyResource, 0, len(names))
	for _, name := range names {
		resources = append(resources, draPolicyResource{resourceName: name, deviceClassName: sriovnetworkv1.ResourceNameToDeviceClassName(name), driver: resourceDrivers[name]})
	}
	return resources, nil
}

// draResourceNameSet returns a set of resource names from a list of draPolicyResource.
func draResourceNameSet(resources []draPolicyResource) map[string]struct{} {
	set := make(map[string]struct{}, len(resources))
	for _, res := range resources {
		set[res.resourceName] = struct{}{}
	}
	return set
}

// draDeviceClassPoolSet returns a set of device class names from a list of draPolicyResource.
func draDeviceClassPoolSet(resources []draPolicyResource) map[string]struct{} {
	set := make(map[string]struct{}, len(resources))
	for _, res := range resources {
		set[res.deviceClassName] = struct{}{}
	}
	return set
}

// draRetainedPools returns the resource-pool names selected by operator-managed
// SriovResourcePolicies. Reads live (apiReader) because these objects may have
// been written earlier in the same reconcile.
func (r *SriovNetworkNodePolicyReconciler) draRetainedPools(ctx context.Context,
	dc *sriovnetworkv1.SriovOperatorConfig) (map[string]struct{}, error) {
	logger := log.FromContext(ctx)
	pools := make(map[string]struct{})
	policyList := &sriovdrav1alpha1.SriovResourcePolicyList{}
	if err := r.apiReader().List(ctx, policyList, client.InNamespace(vars.Namespace)); err != nil {
		if apimeta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			logger.V(1).Info("SriovResourcePolicy CRD not available, no retained pools")
			return pools, nil
		}
		return nil, err
	}
	for i := range policyList.Items {
		policy := &policyList.Items[i]
		if !draObjectHasOwnershipMarker(dc, policy) {
			continue
		}
		for _, config := range policy.Spec.Configs {
			if config.DeviceAttributesSelector == nil {
				continue
			}
			if pool, found := config.DeviceAttributesSelector.MatchLabels[constants.DRAResourcePoolLabel]; found {
				pools[pool] = struct{}{}
			}
		}
	}
	return pools, nil
}

// requireDRAOwnership permits recovery of either a missing label or a missing
// controller reference, but never adopts an unmarked, user-created object.
func requireDRAOwnership(owner, existing metav1.Object, resource string) error {
	if draObjectHasOwnershipMarker(owner, existing) {
		return nil
	}
	return apierrors.NewConflict(sriovdrav1alpha1.Resource(resource),
		existing.GetName(), fmt.Errorf("object exists but is not operator-managed"))
}

// draObjectHasOwnershipMarker checks whether an existing DRA object can be treated as
// managed by this operator (SR-IOV Network Operator).
func draObjectHasOwnershipMarker(owner, existing metav1.Object) bool {
	// Preserve objects controlled by another owner, even if they carry
	// this operator's generated-by label.
	if metav1.GetControllerOf(existing) != nil && !metav1.IsControlledBy(existing, owner) {
		return false
	}
	return objectHasOperatorManagedLabel(existing) || metav1.IsControlledBy(existing, owner)
}

// reconcileDeviceAttributes brings an existing DeviceAttributes CR in line with desired state:
// owner reference, operator labels (including after managed-label drift), and spec.
func (r *SriovNetworkNodePolicyReconciler) reconcileDeviceAttributes(ctx context.Context,
	dc *sriovnetworkv1.SriovOperatorConfig, desired, existing *sriovdrav1alpha1.DeviceAttributes) error {
	logger := log.FromContext(ctx).WithName("reconcileDeviceAttributes")
	if err := requireDRAOwnership(dc, existing, "deviceattributes"); err != nil {
		return err
	}
	changed, err := utils.EnsureControllerOwner(dc, existing, r.Scheme)
	if err != nil {
		return err
	}
	if utils.ApplyDesiredLabels(existing, desired.Labels) {
		changed = true
	}
	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		existing.Spec = desired.Spec
		changed = true
	}
	if !changed {
		return nil
	}
	logger.V(1).Info("Updating DeviceAttributes", "name", existing.Name)
	return r.Update(ctx, existing)
}

// reconcileSriovResourcePolicy brings an existing SriovResourcePolicy CR in line with desired state:
// owner reference, operator labels (including after managed-label drift), and spec.
func (r *SriovNetworkNodePolicyReconciler) reconcileSriovResourcePolicy(ctx context.Context,
	dc *sriovnetworkv1.SriovOperatorConfig, desired, existing *sriovdrav1alpha1.SriovResourcePolicy) error {
	logger := log.FromContext(ctx).WithName("reconcileSriovResourcePolicy")
	if err := requireDRAOwnership(dc, existing, "sriovresourcepolicies"); err != nil {
		return err
	}
	changed, err := utils.EnsureControllerOwner(dc, existing, r.Scheme)
	if err != nil {
		return err
	}
	if utils.ApplyDesiredLabels(existing, desired.Labels) {
		changed = true
	}
	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		existing.Spec = desired.Spec
		changed = true
	}
	if !changed {
		return nil
	}
	logger.V(1).Info("Updating SriovResourcePolicy", "name", existing.Name)
	return r.Update(ctx, existing)
}

// deviceClassAPIUnavailable reports whether the cluster client cannot use DeviceClass (API absent or type not in scheme).
func deviceClassAPIUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return apimeta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err)
}

// extendedDeviceClassHasOwnershipMarker reports whether dc was previously managed by this operator.
// The resource-name label survives managed-label drift when generated-by is removed.
func extendedDeviceClassHasOwnershipMarker(dc *resourceapi.DeviceClass, resourceName string) bool {
	if dc == nil || dc.Labels == nil {
		return false
	}
	return dc.Labels[constants.DRADeviceClassResourceNameLabel] == resourceName
}

// Identify generated DeviceClasses by the operator label or by a resource-name
// label that matches the generated class name. Exclude the base DeviceClass,
// which is managed by the driver deployment code.
func managedExtendedDeviceClass(dc *resourceapi.DeviceClass) bool {
	if dc.Name == constants.DRADriverBaseDeviceClassName {
		return false
	}
	resourceName := dc.Labels[constants.DRADeviceClassResourceNameLabel]
	return objectHasOperatorManagedLabel(dc) ||
		(resourceName != "" && dc.Name == sriovnetworkv1.ResourceNameToDeviceClassName(resourceName))
}

// reconcileExtendedDeviceClass brings an existing extended-resource DeviceClass in line with desired
// operator labels (including after managed-label drift) and spec.
func (r *SriovNetworkNodePolicyReconciler) reconcileExtendedDeviceClass(ctx context.Context,
	desired, existing *resourceapi.DeviceClass) error {
	desiredSpec := *desired.Spec.DeepCopy()
	if desiredSpec.ExtendedResourceName != nil && existing.Spec.ExtendedResourceName == nil {
		// DRAExtendedResource is a cluster feature gate. When it is disabled, the
		// API server drops this field from the stored object. Probe with a dry-run
		// so the comparison reflects the API server's current capability and can
		// notice when the feature is enabled later.
		probe := existing.DeepCopy()
		probe.Spec = desiredSpec
		if err := r.Update(ctx, probe, client.DryRunAll); err != nil {
			return err
		}
		if probe.Spec.ExtendedResourceName == nil {
			desiredSpec.ExtendedResourceName = nil
		}
	}

	changed := utils.ApplyDesiredLabels(existing, desired.GetLabels())
	if !equality.Semantic.DeepEqual(desiredSpec, existing.Spec) {
		existing.Spec = desiredSpec
		changed = true
	}
	if !changed {
		return nil
	}
	return r.Update(ctx, existing)
}

// syncDeviceAttributes creates/updates/deletes DeviceAttributes CRs for each unique resourceName from policies (DRA mode).
// Policies reference these via DeviceAttributesSelector; the driver merges attributes onto selected devices.
func (r *SriovNetworkNodePolicyReconciler) syncDeviceAttributes(ctx context.Context,
	dc *sriovnetworkv1.SriovOperatorConfig,
	pl *sriovnetworkv1.SriovNetworkNodePolicyList) error {
	logger := log.FromContext(ctx).WithName("syncDeviceAttributes")
	logger.V(1).Info("Start to sync DeviceAttributes CRs")

	retainedPolicyResources, err := validatedDRAPolicyResources(pl)
	if err != nil {
		return err
	}

	attrList := &sriovdrav1alpha1.DeviceAttributesList{}
	if err := r.List(ctx, attrList, client.InNamespace(vars.Namespace)); err != nil {
		if apimeta.IsNoMatchError(err) {
			return fmt.Errorf("DeviceAttributes REST mapping is unavailable: %w", err)
		}
		logger.Error(err, "Failed to list DeviceAttributes CRs")
		return err
	}
	// This sync runs before syncSriovResourcePolicies. A pending node retains its
	// prior SriovResourcePolicy there, so preserve attributes selected by that
	// policy even when its pool is no longer in the current source policies.
	retainedPolicyPools, err := r.draRetainedPools(ctx, dc)
	if err != nil {
		logger.Error(err, "Failed to list SriovResourcePolicies")
		return err
	}

	existingAttrsByName := make(map[string]*sriovdrav1alpha1.DeviceAttributes, len(attrList.Items))
	for i := range attrList.Items {
		existingAttrsByName[attrList.Items[i].Name] = &attrList.Items[i]
	}

	desiredNames := make(map[string]struct{}, len(retainedPolicyResources))
	for _, res := range retainedPolicyResources {
		name := res.deviceClassName + "-attrs"
		desiredNames[name] = struct{}{}
		desired := buildDeviceAttributesCR(name, res.resourceName)
		if err := controllerutil.SetControllerReference(dc, desired, r.Scheme); err != nil {
			logger.Error(err, "Failed to set controller reference on DeviceAttributes", "name", name)
			return err
		}
		if existing, ok := existingAttrsByName[name]; ok {
			if err := r.reconcileDeviceAttributes(ctx, dc, desired, existing); err != nil {
				logger.Error(err, "Failed to reconcile DeviceAttributes", "name", name)
				return err
			}
			continue
		}
		logger.V(1).Info("Creating DeviceAttributes", "name", name)
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				logger.V(1).Info("Adopting existing DeviceAttributes after managed-label drift", "name", name)
				existing := &sriovdrav1alpha1.DeviceAttributes{}
				if err := r.Get(ctx, types.NamespacedName{Namespace: desired.Namespace, Name: name}, existing); err != nil {
					logger.Error(err, "Failed to get existing DeviceAttributes for adoption", "name", name)
					return err
				}
				if err := r.reconcileDeviceAttributes(ctx, dc, desired, existing); err != nil {
					logger.Error(err, "Failed to adopt DeviceAttributes", "name", name)
					return err
				}
				continue
			}
			logger.Error(err, "Failed to create DeviceAttributes", "name", name)
			return err
		}
	}

	for i := range attrList.Items {
		item := &attrList.Items[i]
		if !draObjectHasOwnershipMarker(dc, item) {
			continue
		}
		if _, found := desiredNames[item.Name]; !found {
			if pool := item.Labels[constants.DRAResourcePoolLabel]; pool != "" {
				if _, retained := retainedPolicyPools[pool]; retained {
					logger.V(1).Info("Retaining DeviceAttributes selected by existing SriovResourcePolicy", "name", item.Name, "pool", pool)
					continue
				}
			}
			logger.V(1).Info("Deleting obsolete DeviceAttributes", "name", item.Name)
			if err := deleteObservedObject(ctx, r.Client, item); err != nil {
				logger.Error(err, "Failed to delete DeviceAttributes", "name", item.Name)
				return err
			}
		}
	}
	return nil
}

func buildDeviceAttributesCR(name, resourceName string) *sriovdrav1alpha1.DeviceAttributes {
	deviceClassName := sriovnetworkv1.ResourceNameToDeviceClassName(resourceName)
	// Use same extended resource name (prefix/resourceName) as device plugin for consistency
	extendedName := buildExtendedResourceName(resourceName)
	labels := maps.Clone(constants.DRAOperatorLabels)
	labels[constants.DRAResourcePoolLabel] = deviceClassName
	return &sriovdrav1alpha1.DeviceAttributes{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: vars.Namespace,
			Labels:    labels,
		},
		Spec: sriovdrav1alpha1.DeviceAttributesSpec{
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				resourceapi.QualifiedName(constants.DRAResourceNameAttributeKey): {StringValue: &extendedName},
			},
		},
	}
}

// syncSriovResourcePolicies syncs SriovResourcePolicy CRs for DRA
// mode (one per node). Each node waits for its rendered policy revisions and
// daemon-confirmed NodeState generation before publication, retaining its existing
// policy while pending. Obsolete operator-managed policies are always pruned.
//
// publish controls whether desired policies are created or updated. When false
// (for example DeviceAttributes sync failed), obsolete CRs are still deleted so
// stale advertisements go away, but new or updated SriovResourcePolicy objects
// are not written until attributes are healthy again.
func (r *SriovNetworkNodePolicyReconciler) syncSriovResourcePolicies(ctx context.Context,
	dc *sriovnetworkv1.SriovOperatorConfig,
	pl *sriovnetworkv1.SriovNetworkNodePolicyList,
	nl *corev1.NodeList,
	publish bool) error {
	logger := log.FromContext(ctx).WithName("syncSriovResourcePolicies")
	logger.V(1).Info("Start to sync SriovResourcePolicy CRs", "publish", publish)

	retainedPolicyResources, validationErr := validatedDRAPolicyResources(pl)
	retainedResourceNames := draResourceNameSet(retainedPolicyResources)
	// Keyed by CR metadata.name (same as node.Name today)
	desiredPolicies := make(map[string]*sriovdrav1alpha1.SriovResourcePolicy)
	pendingNodes := make(map[string]bool)
	failedNodes := make(map[string]bool)
	renderErr := validationErr
	for _, node := range nl.Items {
		if validationErr != nil {
			// Without valid resources, retain every managed node's existing policy.
			// Policies for nodes outside this list can still be safely pruned.
			failedNodes[node.Name] = true
			continue
		}
		policy, ready, err := r.renderSriovResourcePolicyForNode(ctx, pl, &node, retainedResourceNames)
		if err != nil {
			logger.Error(err, "Failed to render SriovResourcePolicy for node", "node", node.Name)
			failedNodes[node.Name] = true
			renderErr = stderrors.Join(renderErr, err)
			continue
		}
		if !ready {
			pendingNodes[node.Name] = true
			logger.V(1).Info("Waiting for applied NodeState before publishing SriovResourcePolicy", "node", node.Name)
			continue
		}
		if policy != nil {
			desiredPolicies[policy.Name] = policy
		}
	}

	policyList := &sriovdrav1alpha1.SriovResourcePolicyList{}
	if err := r.List(ctx, policyList, client.InNamespace(vars.Namespace)); err != nil {
		if apimeta.IsNoMatchError(err) {
			logger.V(1).Info("SriovResourcePolicy CRD not available, skipping sync")
			return renderErr
		}
		logger.Error(err, "Failed to list SriovResourcePolicy CRs")
		return stderrors.Join(renderErr, err)
	}

	existingPoliciesByName := make(map[string]*sriovdrav1alpha1.SriovResourcePolicy, len(policyList.Items))
	for i := range policyList.Items {
		existingPoliciesByName[policyList.Items[i].Name] = &policyList.Items[i]
	}

	// Delete obsolete managed policies before creating or updating desired ones,
	// so an ownership conflict or render error on one node does not block cleanup
	// on another. A failed render retains its existing policy just like a pending
	// node, because this pass cannot know whether it is obsolete.
	for i := range policyList.Items {
		existing := &policyList.Items[i]
		if !draObjectHasOwnershipMarker(dc, existing) {
			continue
		}
		if _, exists := desiredPolicies[existing.Name]; !exists && !pendingNodes[existing.Name] && !failedNodes[existing.Name] {
			logger.V(1).Info("Deleting obsolete SriovResourcePolicy", "name", existing.Name)
			if err := deleteObservedObject(ctx, r.Client, existing); err != nil {
				logger.Error(err, "Failed to delete SriovResourcePolicy", "name", existing.Name)
				return stderrors.Join(renderErr, err)
			}
		}
	}

	if renderErr != nil {
		return renderErr
	}

	// Pruning above always runs. Skip create/update when publish is false so we
	// do not advertise resources while DeviceAttributes (or another prerequisite)
	// are unhealthy.
	if !publish {
		logger.V(1).Info("Skipping SriovResourcePolicy create/update (publish=false)")
		return nil
	}

	for name, desired := range desiredPolicies {
		if existing, ok := existingPoliciesByName[name]; ok {
			if err := r.reconcileSriovResourcePolicy(ctx, dc, desired, existing); err != nil {
				logger.Error(err, "Failed to reconcile SriovResourcePolicy", "name", name)
				return err
			}
			continue
		}
		logger.V(1).Info("Creating SriovResourcePolicy", "name", name)
		if err := controllerutil.SetControllerReference(dc, desired, r.Scheme); err != nil {
			logger.Error(err, "Failed to set controller reference", "name", name)
			return err
		}
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				logger.V(1).Info("Adopting existing SriovResourcePolicy after managed-label drift", "name", name)
				existing := &sriovdrav1alpha1.SriovResourcePolicy{}
				if err := r.Get(ctx, types.NamespacedName{Namespace: desired.Namespace, Name: name}, existing); err != nil {
					logger.Error(err, "Failed to get existing SriovResourcePolicy for adoption", "name", name)
					return err
				}
				if err := r.reconcileSriovResourcePolicy(ctx, dc, desired, existing); err != nil {
					logger.Error(err, "Failed to adopt SriovResourcePolicy", "name", name)
					return err
				}
				continue
			}
			logger.Error(err, "Failed to create SriovResourcePolicy", "name", name)
			return err
		}
	}

	return nil
}

// nodeHostname returns the kubernetes.io/hostname label value for node selectors.
// node.Name is not guaranteed to match that label (e.g. FQDN vs short name).
func nodeHostname(node *corev1.Node) (string, error) {
	hostname := node.Labels[corev1.LabelHostname]
	if hostname == "" {
		return "", fmt.Errorf("node %q is missing required label %q", node.Name, corev1.LabelHostname)
	}
	return hostname, nil
}

// sriovResourcePolicyNodeSelectorForHostname returns a NodeSelector that matches exactly
// one node by kubernetes.io/hostname (same intent as the former map nodeSelector).
func sriovResourcePolicyNodeSelectorForHostname(hostname string) *corev1.NodeSelector {
	return &corev1.NodeSelector{
		NodeSelectorTerms: []corev1.NodeSelectorTerm{
			{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      corev1.LabelHostname,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{hostname},
					},
				},
			},
		},
	}
}

// renderSriovResourcePolicyForNode generates a SriovResourcePolicy CR for a specific node.
// ready is false while the node's configuration is pending; callers must retain
// its existing policy in that case. A nil policy with ready=true is obsolete.
// Rendering and readiness use the same NodeState snapshot.
func (r *SriovNetworkNodePolicyReconciler) renderSriovResourcePolicyForNode(ctx context.Context,
	pl *sriovnetworkv1.SriovNetworkNodePolicyList,
	node *corev1.Node,
	retainedResourceNames map[string]struct{}) (policy *sriovdrav1alpha1.SriovResourcePolicy, ready bool, err error) {
	logger := log.FromContext(ctx).WithName("renderSriovResourcePolicyForNode")
	logger.V(1).Info("Start to render SriovResourcePolicy for node", "node", node.Name)

	var applicablePolicies []*sriovnetworkv1.SriovNetworkNodePolicy
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Name == constants.DefaultPolicyName {
			continue
		}
		if p.Selected(node) {
			applicablePolicies = append(applicablePolicies, p)
		}
	}
	if len(applicablePolicies) == 0 {
		logger.V(1).Info("No policies apply to node, skipping policy creation", "node", node.Name)
		return nil, true, nil
	}

	hostname, err := nodeHostname(node)
	if err != nil {
		logger.V(1).Info("Skipping node without kubernetes.io/hostname for SriovResourcePolicy", "node", node.Name)
		return nil, true, nil
	}

	policyLabels := maps.Clone(constants.DRAOperatorLabels)
	// node.Name can exceed label-value limits (63 chars); omit the convenience label if invalid.
	if len(validation.IsValidLabelValue(node.Name)) == 0 {
		policyLabels[constants.DRASriovResourcePolicyNodeLabel] = node.Name
	}
	policy = &sriovdrav1alpha1.SriovResourcePolicy{
		ObjectMeta: metav1.ObjectMeta{
			// Same metadata.name as SriovNetworkNodeState (syncAllSriovNetworkNodeStates: ns.Name = node.Name).
			Name:      node.Name,
			Namespace: vars.Namespace,
			Labels:    policyLabels,
		},
		Spec: sriovdrav1alpha1.SriovResourcePolicySpec{
			NodeSelector: sriovResourcePolicyNodeSelectorForHostname(hostname),
			Configs:      []sriovdrav1alpha1.Config{},
		},
	}

	// Read directly from the API server: syncAllSriovNetworkNodeStates may have
	// just written this object, while the controller cache can still hold its
	// previous generation (stale object version). Use this one snapshot for both
	// readiness and config rendering below.
	reader := r.apiReader()
	nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
	if err := reader.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: node.Name}, nodeState); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("SriovNetworkNodeState not yet created, skipping node", "node", node.Name)
			return nil, false, nil
		}
		logger.Error(err, "Failed to get SriovNetworkNodeState", "node", node.Name)
		return nil, false, err
	}
	ready = nodeState.IsReady()
	for _, p := range applicablePolicies {
		// Only policies matching a discovered NIC receive revision stamps from
		// renderNodePolicies. A nonmatching policy must not block publication.
		if p.MatchesInterfaceInNodeState(nodeState) && !p.RevisionMatchesInNodeState(nodeState) {
			ready = false
		}
	}
	// NodeState applies policies last-wins; the DRA driver matches first-wins.
	// Reverse application order to preserve priority and name tie-breaking.
	for i := len(applicablePolicies) - 1; i >= 0; i-- {
		p := applicablePolicies[i]
		if p.Spec.ResourceName != "" {
			if _, ok := retainedResourceNames[p.Spec.ResourceName]; !ok {
				logger.V(1).Info("Skipping policy config for filtered DRA resource name",
					"policy", p.Name, "resourceName", p.Spec.ResourceName)
				continue
			}
		}
		config, skip, err := buildPolicyConfig(p, nodeState)
		if skip {
			logger.V(1).Info("Skipping policy config for DRA", "policy", p.Name, "reason", err)
			continue
		}
		if err != nil {
			logger.Error(err, "Failed to build policy config", "policy", p.Name)
			return nil, false, err
		}
		policy.Spec.Configs = append(policy.Spec.Configs, *config)
	}

	if len(policy.Spec.Configs) == 0 {
		logger.V(1).Info("No policy configs for node, skipping policy creation", "node", node.Name)
		return nil, true, nil
	}
	return policy, ready, nil
}

// buildPolicyConfig converts a SriovNetworkNodePolicy to a SriovResourcePolicy Config
// (DeviceAttributesSelector + ResourceFilters; resource name is in DeviceAttributes).
// When skip is true, err describes why the policy was omitted and the caller should not treat it as a failure.
func buildPolicyConfig(p *sriovnetworkv1.SriovNetworkNodePolicy,
	nodeState *sriovnetworkv1.SriovNetworkNodeState) (*sriovdrav1alpha1.Config, bool, error) {
	if p == nil {
		return nil, false, fmt.Errorf("policy is required")
	}

	// TODO: Add NetFilter support.
	// We skip the entire config because we cannot express the NetFilter selector for DRA Driver SR-IOV.
	if p.Spec.NicSelector.NetFilter != "" {
		// SriovResourcePolicy ResourceFilter has no netFilter field yet; omitting ResourceFilters
		// would match all devices on the node (driver wildcard semantics).
		return nil, true, fmt.Errorf("netFilter nic selector is not supported for DRA")
	}

	pool := sriovnetworkv1.ResourceNameToDeviceClassName(p.Spec.ResourceName)
	config := &sriovdrav1alpha1.Config{
		DeviceAttributesSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{constants.DRAResourcePoolLabel: pool},
		},
		ResourceFilters: []sriovdrav1alpha1.ResourceFilter{},
	}
	resourceFilter := sriovdrav1alpha1.ResourceFilter{}

	// Map vendor
	if p.Spec.NicSelector.Vendor != "" {
		resourceFilter.Vendors = []string{p.Spec.NicSelector.Vendor}
	}

	// Map device ID (convert to VF device ID)
	if p.Spec.NicSelector.DeviceID != "" {
		var deviceID string
		if p.Spec.NumVfs == 0 {
			deviceID = p.Spec.NicSelector.DeviceID
		} else {
			deviceID = sriovnetworkv1.GetVfDeviceID(p.Spec.NicSelector.DeviceID)
			if deviceID == "" {
				return nil, false, fmt.Errorf("policy %q: no VF device ID mapping for PF device ID %q", p.Name, p.Spec.NicSelector.DeviceID)
			}
		}
		resourceFilter.Devices = []string{deviceID}
	}

	// Map PF names (resolve alternative interface names via node state)
	if len(p.Spec.NicSelector.PfNames) > 0 {
		resourceFilter.PfNames = resolvePfNames(p.Spec.NicSelector.PfNames, nodeState)
	}

	// Map root devices (PF PCI addresses) to DRA filter PfPciAddresses
	if len(p.Spec.NicSelector.RootDevices) > 0 {
		resourceFilter.PfPciAddresses = p.Spec.NicSelector.RootDevices
	}

	// Map driver if VFIO
	if p.Spec.DeviceType == constants.DeviceTypeVfioPci {
		resourceFilter.Drivers = []string{"vfio-pci"}
	}

	// Map link type. vfio-pci device link type is not detectable.
	// Emit DRA ResourceFilter aliases ("eth"/"ib"); the driver normalizes to
	// "ethernet"/"infiniband" when matching device attributes.
	if p.Spec.DeviceType != constants.DeviceTypeVfioPci && p.Spec.LinkType != "" {
		resourceFilter.LinkType = strings.ToLower(constants.LinkTypeETH)
		if strings.EqualFold(p.Spec.LinkType, constants.LinkTypeIB) {
			resourceFilter.LinkType = strings.ToLower(constants.LinkTypeIB)
		}
	}

	// Do not append a zero-valued ResourceFilter: in dra-driver-sriov, deviceMatchesFilter
	// treats every unset field as a wildcard, so {} matches all devices on the node.
	// NetFilter-only policies are skipped earlier; when nothing else mapped (e.g. GetVfDeviceID
	// returned empty), omit ResourceFilters rather than emitting a match-all filter entry.
	if !equality.Semantic.DeepEqual(resourceFilter, sriovdrav1alpha1.ResourceFilter{}) {
		config.ResourceFilters = append(config.ResourceFilters, resourceFilter)
	}

	return config, false, nil
}

// buildExtendedResourceName returns the extended resource name: ResourcePrefix/resourceName.
func buildExtendedResourceName(resourceName string) string {
	prefix := vars.ResourcePrefix
	if prefix == "" {
		return resourceName
	}
	return prefix + "/" + resourceName
}

// buildDeviceClassCEL returns a CEL expression matching devices with the given resourceName.
// Uses the extended resource name (ResourcePrefix/resourceName) so it matches the attribute set by DeviceAttributes.
func buildDeviceClassCEL(resourceName string) string {
	extendedName := buildExtendedResourceName(resourceName)
	escaped := strings.ReplaceAll(extendedName, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	attrNS, attrName, _ := strings.Cut(constants.DRAResourceNameAttributeKey, "/")
	return fmt.Sprintf(`device.driver == "%s" && has(device.attributes["%s"].%s) && device.attributes["%s"].%s == "%s"`,
		constants.DRADriverBaseDeviceClassName, attrNS, attrName, attrNS, attrName, escaped)
}

// syncExtendedResourceDeviceClasses creates/updates/deletes DeviceClass resources with extendedResourceName.
// DeviceClass is cluster-scoped so no SriovOperatorConfig owner reference is set; operator-created
// DeviceClasses are identified by label and cleaned up in cleanupExtendedResourceDeviceClasses when DRA is disabled.
func (r *SriovNetworkNodePolicyReconciler) syncExtendedResourceDeviceClasses(ctx context.Context,
	dc *sriovnetworkv1.SriovOperatorConfig,
	pl *sriovnetworkv1.SriovNetworkNodePolicyList) error {
	logger := log.FromContext(ctx).WithName("syncExtendedResourceDeviceClasses")
	logger.V(1).Info("Start to sync extended resource DeviceClasses")
	retainedPolicyResources, err := validatedDRAPolicyResources(pl)
	if err != nil {
		return err
	}
	retainedDeviceClassNames := draDeviceClassPoolSet(retainedPolicyResources)
	// This sync runs after syncSriovResourcePolicies. A pending (VF/config for the latest
	// policies is not applied and confirmed) node retains its prior SriovResourcePolicy
	// there, so preserve classes selected by that policy even when their pools are no
	// longer in the current source policies.
	retainedPools, err := r.draRetainedPools(ctx, dc)
	if err != nil {
		logger.Error(err, "Failed to list SriovResourcePolicies")
		return err
	}
	maps.Copy(retainedDeviceClassNames, retainedPools)
	list := &resourceapi.DeviceClassList{}
	if err := r.List(ctx, list); err != nil {
		if deviceClassAPIUnavailable(err) {
			logger.V(1).Info("DeviceClass API not available, skipping extended resource DeviceClass sync")
			return nil
		}
		return err
	}

	existingDCByName := make(map[string]*resourceapi.DeviceClass, len(list.Items))
	for i := range list.Items {
		if objectHasOperatorManagedLabel(&list.Items[i]) {
			existingDCByName[list.Items[i].Name] = &list.Items[i]
		}
	}

	for _, res := range retainedPolicyResources {
		desired, err := buildExtendedResourceDeviceClass(res.deviceClassName, res.resourceName, buildExtendedResourceName(res.resourceName), buildDeviceClassCEL(res.resourceName), res.driver)
		if err != nil {
			return err
		}
		if existing, ok := existingDCByName[res.deviceClassName]; ok {
			if err := r.reconcileExtendedDeviceClass(ctx, desired, existing); err != nil {
				logger.Error(err, "Failed to reconcile DeviceClass", "name", res.deviceClassName)
				return err
			}
			continue
		}
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				logger.V(1).Info("Adopting existing DeviceClass after managed-label drift", "name", res.deviceClassName)
				existing := &resourceapi.DeviceClass{}
				if err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing); err != nil {
					logger.Error(err, "Failed to get existing DeviceClass for adoption", "name", res.deviceClassName)
					return err
				}
				if !extendedDeviceClassHasOwnershipMarker(existing, res.resourceName) {
					gr := schema.GroupResource{Group: resourceapi.SchemeGroupVersion.Group, Resource: "deviceclasses"}
					return apierrors.NewConflict(gr, existing.Name,
						fmt.Errorf("DeviceClass %q exists but is not operator-managed", existing.Name))
				}
				if err := r.reconcileExtendedDeviceClass(ctx, desired, existing); err != nil {
					logger.Error(err, "Failed to adopt DeviceClass", "name", res.deviceClassName)
					return err
				}
				continue
			}
			return err
		}
	}
	for i := range list.Items {
		item := &list.Items[i]
		if !managedExtendedDeviceClass(item) {
			continue
		}
		if _, desired := retainedDeviceClassNames[item.Name]; !desired {
			if err := deleteObservedObject(ctx, r.Client, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildExtendedResourceDeviceClass(deviceClassName, resourceName, extendedResourceName, celExpression, driver string) (*resourceapi.DeviceClass, error) {
	var config []resourceapi.DeviceClassConfiguration
	if driver != "" {
		vfConfig := configapi.DefaultVfConfig()
		vfConfig.Driver = driver
		parameters, err := json.Marshal(vfConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to encode VF configuration for DeviceClass %q: %w", deviceClassName, err)
		}
		config = []resourceapi.DeviceClassConfiguration{{DeviceConfiguration: resourceapi.DeviceConfiguration{
			Opaque: &resourceapi.OpaqueDeviceConfiguration{
				Driver:     constants.DRADriverBaseDeviceClassName,
				Parameters: runtime.RawExtension{Raw: parameters},
			},
		}}}
	}
	labels := maps.Clone(constants.DRAOperatorLabels)
	labels[constants.DRADeviceClassResourceNameLabel] = resourceName
	return &resourceapi.DeviceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:   deviceClassName,
			Labels: labels,
		},
		Spec: resourceapi.DeviceClassSpec{
			ExtendedResourceName: &extendedResourceName,
			Config:               config,
			Selectors: []resourceapi.DeviceSelector{
				{CEL: &resourceapi.CELDeviceSelector{Expression: celExpression}},
			},
		},
	}, nil
}

// cleanupExtendedResourceDeviceClasses removes generated classes when DRA is disabled
// or the operator config is being deleted. These cluster-scoped objects have no owner reference.
func cleanupExtendedResourceDeviceClasses(ctx context.Context, c client.Client) error {
	logger := log.FromContext(ctx).WithName("cleanupExtendedResourceDeviceClasses")
	logger.V(1).Info("Cleaning up extended resource DeviceClasses")
	list := &resourceapi.DeviceClassList{}
	if err := c.List(ctx, list); err != nil {
		if deviceClassAPIUnavailable(err) {
			logger.V(1).Info("DeviceClass API not available, nothing to clean up", "error", err)
			return nil
		}
		logger.Error(err, "Failed to list extended resource DeviceClasses for cleanup")
		return err
	}
	for i := range list.Items {
		item := &list.Items[i]
		if !managedExtendedDeviceClass(item) {
			continue
		}
		if err := deleteObservedObject(ctx, c, item); err != nil {
			return err
		}
	}
	logger.V(1).Info("Finished cleaning up extended resource DeviceClasses")
	return nil
}

// cleanupSriovResourcePoliciesAndDeviceAttributes deletes all operator-generated SriovResourcePolicy and DeviceAttributes CRs when DRA is disabled.
func (r *SriovNetworkNodePolicyReconciler) cleanupSriovResourcePoliciesAndDeviceAttributes(ctx context.Context, dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.FromContext(ctx).WithName("cleanupSriovResourcePoliciesAndDeviceAttributes")
	logger.V(1).Info("Cleaning up SriovResourcePolicy and DeviceAttributes CRs")
	listOpts := []client.ListOption{
		client.InNamespace(vars.Namespace),
	}
	policyList := &sriovdrav1alpha1.SriovResourcePolicyList{}
	if err := r.List(ctx, policyList, listOpts...); err != nil {
		if apimeta.IsNoMatchError(err) {
			logger.V(1).Info("SriovResourcePolicy CRD not available, nothing to clean up")
		} else {
			return err
		}
	} else {
		for i := range policyList.Items {
			if !draObjectHasOwnershipMarker(dc, &policyList.Items[i]) {
				continue
			}
			if err := deleteObservedObject(ctx, r.Client, &policyList.Items[i]); err != nil {
				return err
			}
		}
	}
	attrList := &sriovdrav1alpha1.DeviceAttributesList{}
	if err := r.List(ctx, attrList, listOpts...); err != nil {
		if apimeta.IsNoMatchError(err) {
			logger.V(1).Info("DeviceAttributes CRD not available, nothing to clean up")
		} else {
			return err
		}
	} else {
		for i := range attrList.Items {
			if !draObjectHasOwnershipMarker(dc, &attrList.Items[i]) {
				continue
			}
			if err := deleteObservedObject(ctx, r.Client, &attrList.Items[i]); err != nil {
				return err
			}
		}
	}
	return nil
}
