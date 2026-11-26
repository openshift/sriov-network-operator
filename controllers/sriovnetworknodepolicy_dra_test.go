package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-cmp/cmp"
	sriovdrav1alpha1 "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/api/sriovdra/v1alpha1"
	configapi "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/api/virtualfunction/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	dracel "k8s.io/dynamic-resource-allocation/cel"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// --- Phase 1: DRA pure helper unit tests ---

var _ = DescribeTable("prunes unmanaged nodes when DRA policies are invalid", func(cleanupFails bool) {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })

	ctx := context.Background()
	scheme := runtime.NewScheme()
	Expect(sriovnetworkv1.AddToScheme(scheme)).To(Succeed())
	Expect(sriovdrav1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	owner := &sriovnetworkv1.SriovOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner"},
		Spec: sriovnetworkv1.SriovOperatorConfigSpec{
			FeatureGates:             map[string]bool{consts.DynamicResourceAllocationFeatureGate: true},
			ConfigDaemonNodeSelector: map[string]string{"managed": "true"},
		},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "managed-node", Labels: map[string]string{"managed": "true"}}}
	outside := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "outside-node"}}
	state := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace}}
	policy := &sriovnetworkv1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "netdevice", Namespace: vars.Namespace},
		Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
	}
	conflict := policy.DeepCopy()
	conflict.Name = "vfio"
	conflict.Spec.DeviceType = consts.DeviceTypeVfioPci
	// The valid policy has a pending update: generation 2, last rendered as generation 1.
	independent := &sriovnetworkv1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "independent", Namespace: vars.Namespace, UID: "independent-uid", Generation: 2},
		Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "mellanox_nic"},
	}
	Expect(state.SetPolicyRevisions(map[string]sriovnetworkv1.SourceRevision{
		"independent": {Name: "independent", UID: "independent-uid", Generation: 1},
	})).To(Succeed())
	retained := &sriovdrav1alpha1.SriovResourcePolicy{ObjectMeta: metav1.ObjectMeta{
		Name: node.Name, Namespace: vars.Namespace,
	}}
	Expect(controllerutil.SetControllerReference(owner, retained, scheme)).To(Succeed())
	obsolete := retained.DeepCopy()
	obsolete.Name = outside.Name
	foreign := obsolete.DeepCopy()
	foreign.Name = "foreign-policy"
	foreign.Labels = maps.Clone(consts.DRAOperatorLabels)
	foreign.OwnerReferences[0].UID = "another-owner"
	cleanupErr := fmt.Errorf("cleanup failed")
	writes := 0
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(owner, node, outside, state, policy, conflict, independent, retained, obsolete, foreign).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.CreateOption) error {
				writes++
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.UpdateOption) error {
				writes++
				return c.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, patch k8sclient.Patch, opts ...k8sclient.PatchOption) error {
				writes++
				return c.Patch(ctx, obj, patch, opts...)
			},
			Delete: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.DeleteOption) error {
				Expect(obj).To(BeAssignableToTypeOf(obsolete))
				Expect(obj.GetName()).To(Equal(obsolete.Name))
				if cleanupFails {
					return cleanupErr
				}
				return c.Delete(ctx, obj, opts...)
			},
		}).Build()
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodePolicySyncEventName}})
	Expect(err).To(MatchError(ContainSubstring("incompatible deviceType settings")))
	if cleanupFails {
		Expect(err).To(MatchError(ContainSubstring(cleanupErr.Error())))
	} else {
		Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(obsolete), &sriovdrav1alpha1.SriovResourcePolicy{})).To(MatchError(errors.IsNotFound, "obsolete policy should be pruned"))
	}
	Expect(writes).To(BeZero(), "invalid policies must not change NodeStates or publish advertisements")
	// Conflicting policies prevent even an unrelated valid policy's revision from advancing.
	gotState := &sriovnetworkv1.SriovNetworkNodeState{}
	Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(state), gotState)).To(Succeed())
	revisions := map[string]sriovnetworkv1.SourceRevision{}
	Expect(json.Unmarshal([]byte(gotState.GetAnnotations()[consts.NodeStatePolicyRevisionsAnnotation]), &revisions)).To(Succeed())
	Expect(revisions["independent"].Generation).To(Equal(int64(1)), "valid policy update must stay pending while other policies conflict")
	for _, expected := range []*sriovdrav1alpha1.SriovResourcePolicy{retained, foreign} {
		got := &sriovdrav1alpha1.SriovResourcePolicy{}
		Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(expected), got)).To(Succeed())
		Expect(got.Spec).To(Equal(expected.Spec))
	}
},
	Entry("removes obsolete policies when cleanup succeeds", false),
	Entry("reports both validation and cleanup failures", true),
)

var _ = It("requires the node hostname label", func() {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker-0.example.com",
			Labels: map[string]string{
				corev1.LabelHostname: "worker-0",
			},
		},
	}
	got, err := nodeHostname(node)
	Expect(err).NotTo(HaveOccurred(), "nodeHostname() unexpected error: %v", err)
	Expect(got).To(Equal("worker-0"), "nodeHostname() = %q, want %q", got, "worker-0")

	node.Labels = nil
	_, err = nodeHostname(node)
	Expect(err).To(HaveOccurred(), "nodeHostname() without label should return an error")
})

var _ = It("uses the uncached NodeState snapshot for DRA policy readiness and rendering", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })

	scheme := runtime.NewScheme()
	Expect(sriovnetworkv1.AddToScheme(scheme)).To(Succeed())
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-0",
		Labels: map[string]string{
			"node-role.kubernetes.io/worker": "",
			corev1.LabelHostname:             "worker-0",
		},
	}}
	policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{{
		ObjectMeta: metav1.ObjectMeta{Name: "intel", Generation: 1},
		Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			ResourceName: "intel_nic",
			NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
			NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
				Vendor:  "8086",
				PfNames: []string{"alternate-pf"},
			},
		},
	}}}
	fresh := &sriovnetworkv1.SriovNetworkNodeState{
		ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace, Generation: 2},
		Status: sriovnetworkv1.SriovNetworkNodeStateStatus{Interfaces: sriovnetworkv1.InterfaceExts{{
			Name: "fresh-pf", Vendor: "8086", AltNames: []string{"alternate-pf"},
		}}},
	}
	Expect(renderNodePolicies(fresh, policies, node, false)).To(Succeed())
	fresh.Status.Conditions = []metav1.Condition{{
		Type: sriovnetworkv1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: fresh.Generation,
	}}
	stale := fresh.DeepCopy()
	stale.Status.Interfaces[0].Name = "stale-pf"
	stale.Status.Conditions[0].ObservedGeneration--

	cachedClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()
	apiReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fresh).Build()
	r := &SriovNetworkNodePolicyReconciler{
		Client:            cachedClient,
		Scheme:            scheme,
		UncachedAPIReader: apiReader,
	}
	policy, ready, err := r.renderSriovResourcePolicyForNode(context.Background(), policies, node,
		map[string]struct{}{"intel_nic": {}})
	Expect(err).NotTo(HaveOccurred())
	Expect(ready).To(BeTrue(), "the API snapshot includes the current NodeState generation")
	Expect(policy).NotTo(BeNil())
	Expect(policy.Spec.Configs).To(HaveLen(1))
	Expect(policy.Spec.Configs[0].ResourceFilters).To(HaveLen(1))
	Expect(policy.Spec.Configs[0].ResourceFilters[0].PfNames).To(Equal([]string{"fresh-pf"}))
})

var _ = It("collects the resource names from DRA policies", func() {
	pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
		Items: []sriovnetworkv1.SriovNetworkNodePolicy{
			{ObjectMeta: metav1.ObjectMeta{Name: "p1"}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "p3"}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "mlx5"}},
		},
	}
	got, err := sriovnetworkv1.PolicyResourceDrivers(pl.Items)
	Expect(err).NotTo(HaveOccurred())
	Expect(got).To(HaveLen(2))
	Expect(got).To(HaveKey("intel_nic"))
	Expect(got).To(HaveKey("mlx5"))
})

var _ = It("rejects resource names that normalize to the same device class", func() {
	pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
		{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "INTEL_NIC"}},
	}}
	resources, err := validatedDRAPolicyResources(pl)
	Expect(err).To(MatchError(ContainSubstring("normalizes to the same DRA device class name")))
	Expect(resources).To(BeNil())
})

var _ = It("publishes each node only after its policy revision is applied", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })

	scheme := runtime.NewScheme()
	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	utilruntime.Must(sriovdrav1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	ctx := context.Background()
	owner := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
		Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner",
	}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-0",
		Labels: map[string]string{
			"node-role.kubernetes.io/worker": "",
			corev1.LabelHostname:             "worker-0",
		},
	}}
	policy := &sriovnetworkv1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: vars.Namespace, UID: "policy-uid", Generation: 3},
		Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			ResourceName: "intel_nic",
			NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
			NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
		},
	}
	// This policy selects both nodes but matches no NIC. renderNodePolicies
	// deliberately does not stamp it; it must not prevent either node publishing.
	unmatchedPolicy := policy.DeepCopy()
	unmatchedPolicy.Name, unmatchedPolicy.UID = "unmatched", "unmatched-uid"
	unmatchedPolicy.Spec.ResourceName = "unmatched_nic"
	unmatchedPolicy.Spec.NicSelector.Vendor = "15b3"
	policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{*policy, *unmatchedPolicy}}
	state := &sriovnetworkv1.SriovNetworkNodeState{
		ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace, Generation: 2},
	}
	state.Status.Interfaces = sriovnetworkv1.InterfaceExts{{Vendor: "8086"}}
	Expect(renderNodePolicies(state, policies, node, false)).To(Succeed())
	state.Status.Conditions = []metav1.Condition{{
		Type:               sriovnetworkv1.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: state.Generation,
	}}
	for _, tc := range []struct {
		name         string
		mutate       func(*sriovnetworkv1.SriovNetworkNodeState)
		missingState bool
		wantReady    bool
	}{
		{name: "applied including unmatched NIC policy", wantReady: true},
		{name: "stale applied generation", mutate: func(s *sriovnetworkv1.SriovNetworkNodeState) {
			s.Status.Conditions[0].ObservedGeneration--
		}},
		{name: "not ready", mutate: func(s *sriovnetworkv1.SriovNetworkNodeState) {
			s.Status.Conditions[0].Status = metav1.ConditionFalse
		}},
		{name: "missing readiness", mutate: func(s *sriovnetworkv1.SriovNetworkNodeState) {
			s.Status.Conditions = nil
		}},
		{name: "missing revision", mutate: func(s *sriovnetworkv1.SriovNetworkNodeState) {
			s.Annotations = nil
		}},
		{name: "stale policy generation", mutate: func(s *sriovnetworkv1.SriovNetworkNodeState) {
			Expect(s.SetPolicyRevisions(map[string]sriovnetworkv1.SourceRevision{
				policy.Name: {UID: policy.UID, Generation: policy.Generation - 1},
			})).To(Succeed())
		}},
		{name: "recreated policy", mutate: func(s *sriovnetworkv1.SriovNetworkNodeState) {
			Expect(s.SetPolicyRevisions(map[string]sriovnetworkv1.SourceRevision{
				policy.Name: {UID: "old-policy-uid", Generation: policy.Generation},
			})).To(Succeed())
		}},
		{name: "missing NodeState", missingState: true},
	} {
		for _, existing := range []bool{false, true} {
			By(fmt.Sprintf("%s/existing=%t", tc.name, existing), func() {
				healthyNode := node.DeepCopy()
				healthyNode.Name = "worker-1"
				healthyNode.Labels[corev1.LabelHostname] = healthyNode.Name
				healthyState := state.DeepCopy()
				healthyState.Name = healthyNode.Name
				obsolete := &sriovdrav1alpha1.SriovResourcePolicy{ObjectMeta: metav1.ObjectMeta{
					Name: "obsolete", Namespace: vars.Namespace, Labels: maps.Clone(consts.DRAOperatorLabels),
				}}
				objects := []k8sclient.Object{owner, node, healthyNode, healthyState, obsolete}
				if !tc.missingState {
					pendingState := state.DeepCopy()
					if tc.mutate != nil {
						tc.mutate(pendingState)
					}
					objects = append(objects, pendingState)
				}
				oldPolicy := &sriovdrav1alpha1.SriovResourcePolicy{
					ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace, Labels: maps.Clone(consts.DRAOperatorLabels)},
					Spec: sriovdrav1alpha1.SriovResourcePolicySpec{Configs: []sriovdrav1alpha1.Config{{
						ResourceFilters: []sriovdrav1alpha1.ResourceFilter{{Vendors: []string{"old-vendor"}}},
					}}},
				}
				if existing {
					objects = append(objects, oldPolicy)
				}
				stateReads := map[string]int{}
				cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c k8sclient.WithWatch, key k8sclient.ObjectKey, obj k8sclient.Object, opts ...k8sclient.GetOption) error {
						if _, ok := obj.(*sriovnetworkv1.SriovNetworkNodeState); ok {
							stateReads[key.Name]++
						}
						return c.Get(ctx, key, obj, opts...)
					},
				}).Build()
				r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
				if existing {
					Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(oldPolicy), oldPolicy)).To(Succeed())
				}
				nodes := &corev1.NodeList{Items: []corev1.Node{*node, *healthyNode}}
				Expect(r.syncSriovResourcePolicies(ctx, owner, policies, nodes, true)).To(Succeed())
				Expect(stateReads).To(Equal(map[string]int{node.Name: 1, healthyNode.Name: 1}), "readiness and rendering must share one NodeState snapshot")
				Expect(errors.IsNotFound(cl.Get(ctx, k8sclient.ObjectKeyFromObject(obsolete), &sriovdrav1alpha1.SriovResourcePolicy{}))).To(BeTrue())

				checkPublished := func(name string) *sriovdrav1alpha1.SriovResourcePolicy {
					got := &sriovdrav1alpha1.SriovResourcePolicy{}
					Expect(cl.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: name}, got)).To(Succeed())
					Expect(got.Spec.Configs).To(HaveLen(2))
					Expect(got.Spec.Configs[1].DeviceAttributesSelector.MatchLabels).To(HaveKeyWithValue(consts.DRAResourcePoolLabel, "intel-nic"))
					Expect(got.Spec.Configs[1].ResourceFilters[0].Vendors).To(Equal([]string{"8086"}))
					return got
				}
				// An unrelated ready node always progresses, even while worker-0 waits.
				healthyPolicy := checkPublished(healthyNode.Name)
				if tc.wantReady {
					checkPublished(node.Name)
				} else {
					got := &sriovdrav1alpha1.SriovResourcePolicy{}
					err := cl.Get(ctx, k8sclient.ObjectKeyFromObject(oldPolicy), got)
					if existing {
						Expect(err).NotTo(HaveOccurred())
						Expect(got).To(Equal(oldPolicy), "pending policy must remain untouched")
					} else {
						Expect(errors.IsNotFound(err)).To(BeTrue(), "pending policy must not be created")
					}
					// Simulate the controller rendering the revision and the daemon
					// acknowledging it; the next sync must publish this node too.
					applied := state.DeepCopy()
					if tc.missingState {
						Expect(cl.Create(ctx, applied)).To(Succeed())
					} else {
						current := &sriovnetworkv1.SriovNetworkNodeState{}
						Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(state), current)).To(Succeed())
						applied.ResourceVersion = current.ResourceVersion
						Expect(cl.Update(ctx, applied)).To(Succeed())
					}
				}
				Expect(r.syncSriovResourcePolicies(ctx, owner, policies, nodes, true)).To(Succeed())
				checkPublished(node.Name)
				Expect(checkPublished(healthyNode.Name)).To(Equal(healthyPolicy), "already published revision must not be rewritten")
			})
		}
	}
})

var _ = It("prunes independently obsolete policies after a node render failure", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })

	scheme := runtime.NewScheme()
	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	utilruntime.Must(sriovdrav1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	ctx := context.Background()
	owner := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
		Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner",
	}}
	failedNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-failed", Labels: map[string]string{"role": "failed", corev1.LabelHostname: "worker-failed"},
	}}
	healthyNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-healthy", Labels: map[string]string{"role": "healthy", corev1.LabelHostname: "worker-healthy"},
	}}
	failedState := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: failedNode.Name, Namespace: vars.Namespace}}
	failedState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)
	healthyState := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: healthyNode.Name, Namespace: vars.Namespace}}
	healthyState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)
	policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "invalid-for-failed-node"},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "failed_nic", NumVfs: 1,
				NodeSelector: map[string]string{"role": "failed"},
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{DeviceID: "ffff"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "healthy-policy"},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "healthy_nic", NodeSelector: map[string]string{"role": "healthy"},
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
			},
		},
	}}
	failedExisting := &sriovdrav1alpha1.SriovResourcePolicy{ObjectMeta: metav1.ObjectMeta{
		Name: failedNode.Name, Namespace: vars.Namespace, Labels: maps.Clone(consts.DRAOperatorLabels),
	}, Spec: sriovdrav1alpha1.SriovResourcePolicySpec{Configs: []sriovdrav1alpha1.Config{{
		ResourceFilters: []sriovdrav1alpha1.ResourceFilter{{Vendors: []string{"old-failed"}}},
	}}}}
	healthyExisting := &sriovdrav1alpha1.SriovResourcePolicy{ObjectMeta: metav1.ObjectMeta{
		Name: healthyNode.Name, Namespace: vars.Namespace, Labels: maps.Clone(consts.DRAOperatorLabels),
	}, Spec: sriovdrav1alpha1.SriovResourcePolicySpec{Configs: []sriovdrav1alpha1.Config{{
		ResourceFilters: []sriovdrav1alpha1.ResourceFilter{{Vendors: []string{"old-healthy"}}},
	}}}}
	obsolete := &sriovdrav1alpha1.SriovResourcePolicy{ObjectMeta: metav1.ObjectMeta{
		Name: "obsolete", Namespace: vars.Namespace, Labels: maps.Clone(consts.DRAOperatorLabels),
	}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		owner, failedNode, healthyNode, failedState, healthyState, failedExisting, healthyExisting, obsolete,
	).Build()
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}

	err := r.syncSriovResourcePolicies(ctx, owner, policies,
		&corev1.NodeList{Items: []corev1.Node{*failedNode, *healthyNode}}, true)
	Expect(err).To(MatchError(ContainSubstring("no VF device ID mapping")))
	Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(obsolete), &sriovdrav1alpha1.SriovResourcePolicy{})).To(MatchError(errors.IsNotFound, "obsolete policy should be pruned"))

	gotFailed := &sriovdrav1alpha1.SriovResourcePolicy{}
	Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(failedExisting), gotFailed)).To(Succeed())
	Expect(gotFailed.Spec.Configs[0].ResourceFilters[0].Vendors).To(Equal([]string{"old-failed"}))
	gotHealthy := &sriovdrav1alpha1.SriovResourcePolicy{}
	Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(healthyExisting), gotHealthy)).To(Succeed())
	Expect(gotHealthy.Spec.Configs[0].ResourceFilters[0].Vendors).To(Equal([]string{"old-healthy"}), "upserts must wait for the render error to be resolved")
})

var _ = It("builds extended resource names with the configured prefix", func() {
	defer func(prev string) { vars.ResourcePrefix = prev }(vars.ResourcePrefix)

	testCases := []struct {
		name         string
		prefix       string
		resourceName string
		expected     string
	}{
		{"empty prefix returns resourceName as-is", "", "intel_nic", "intel_nic"},
		{"with prefix", "openshift.io", "intel_nic", "openshift.io/intel_nic"},
		{"empty prefix empty name", "", "", ""},
		{"prefix with empty name", "p", "", "p/"},
	}
	for _, tc := range testCases {
		By(tc.name, func() {
			vars.ResourcePrefix = tc.prefix
			got := buildExtendedResourceName(tc.resourceName)
			Expect(got).To(Equal(tc.expected), "buildExtendedResourceName(%q) with prefix %q = %q, want %q", tc.resourceName, tc.prefix, got, tc.expected)
		})
	}
})

var _ = It("builds device selectors with escaped resource names", func() {
	defer func(prev string) { vars.ResourcePrefix = prev }(vars.ResourcePrefix)

	vars.ResourcePrefix = ""
	got := buildDeviceClassCEL("intel_nic")
	expectContain := fmt.Sprintf(`device.driver == "%s"`, consts.DRADriverBaseDeviceClassName)
	Expect(got).To(ContainSubstring(expectContain), "buildDeviceClassCEL() must contain driver check: got %q", got)
	attrNS, attrName, _ := strings.Cut(consts.DRAResourceNameAttributeKey, "/")
	expectAttr := fmt.Sprintf(`device.attributes["%s"].%s == "intel_nic"`, attrNS, attrName)
	Expect(got).To(ContainSubstring(expectAttr), "buildDeviceClassCEL() must contain resourceName check: got %q", got)

	// Escaping: quote and backslash in resourceName
	vars.ResourcePrefix = "p"
	got2 := buildDeviceClassCEL(`a"b\c`)
	Expect(got2).To(ContainSubstring(`\"`), "buildDeviceClassCEL() should escape quotes in extended name")
	Expect(got2).To(ContainSubstring(`\\`), "buildDeviceClassCEL() should escape backslash in extended name")
})

var _ = It("matches only devices with the expected driver and resource name", func() {
	defer func(prev string) { vars.ResourcePrefix = prev }(vars.ResourcePrefix)
	vars.ResourcePrefix = "openshift.io"
	compiled := dracel.GetCompiler(dracel.Features{}).CompileCELExpression(buildDeviceClassCEL("intel_nic"), dracel.Options{})
	Expect(compiled.Error).NotTo(HaveOccurred())
	for _, tc := range []struct {
		name, driver, attribute, value string
		want                           bool
	}{
		{"matching resource", consts.DRADriverBaseDeviceClassName, consts.DRAResourceNameAttributeKey, "openshift.io/intel_nic", true},
		{"different resource", consts.DRADriverBaseDeviceClassName, consts.DRAResourceNameAttributeKey, "openshift.io/other", false},
		{"different driver", "other.example.com", consts.DRAResourceNameAttributeKey, "openshift.io/intel_nic", false},
		{"missing attribute namespace", consts.DRADriverBaseDeviceClassName, "", "", false},
		{"missing resourceName", consts.DRADriverBaseDeviceClassName, "k8s.cni.cncf.io/other", "value", false},
	} {
		By(tc.name, func() {
			device := dracel.Device{Driver: tc.driver}
			if tc.attribute != "" {
				device.Attributes = map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					resourceapi.QualifiedName(tc.attribute): {StringValue: &tc.value},
				}
			}
			got, _, err := compiled.DeviceMatches(context.Background(), device)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(tc.want), "device matches = %t, want %t", got, tc.want)
		})
	}
})

var _ = It("builds labeled device attributes for the extended resource", func() {
	defer func(prev string) { vars.Namespace = prev }(vars.Namespace)
	defer func(prev string) { vars.ResourcePrefix = prev }(vars.ResourcePrefix)
	vars.Namespace = "sriov-network-operator"
	vars.ResourcePrefix = "openshift.io"

	cr := buildDeviceAttributesCR("intel-nic-attrs", "intel_nic")

	Expect(cr.Name).To(Equal("intel-nic-attrs"), "Name=%q Namespace=%q, want intel-nic-attrs / %s", cr.Name, cr.Namespace, vars.Namespace)
	Expect(cr.Namespace).To(Equal(vars.Namespace), "Name=%q Namespace=%q, want intel-nic-attrs / %s", cr.Name, cr.Namespace, vars.Namespace)
	Expect(cr.Labels[consts.DRAGeneratedByLabel]).To(Equal(consts.SriovNetworkOperatorIdentifier), "unexpected labels: %v", cr.Labels)
	Expect(cr.Labels[consts.DRAResourcePoolLabel]).To(Equal("intel-nic"), "unexpected labels: %v", cr.Labels)
	key := resourceapi.QualifiedName(consts.DRAResourceNameAttributeKey)
	attr, ok := cr.Spec.Attributes[key]
	Expect(ok).To(BeTrue(), "expected attribute with extended resource name openshift.io/intel_nic, got %v", cr.Spec.Attributes)
	Expect(attr.StringValue).NotTo(BeNil(), "expected attribute with extended resource name openshift.io/intel_nic, got %v", cr.Spec.Attributes)
	Expect(*attr.StringValue).To(Equal("openshift.io/intel_nic"), "expected attribute with extended resource name openshift.io/intel_nic, got %v", cr.Spec.Attributes)
})

var _ = It("renders policy filters from NIC selectors", func() {
	oldNicIDMap := sriovnetworkv1.NicIDMap
	sriovnetworkv1.NicIDMap = []string{"8086 1572 154c"}
	DeferCleanup(func() { sriovnetworkv1.NicIDMap = oldNicIDMap })
	poolLabel := "intel-nic"
	deviceIDPassthrough := "154c"
	pfDeviceID := "1572"
	pfNames := []string{"ens1f0"}
	rootDevices := []string{"0000:08:00.0"}

	testCases := []struct {
		name      string
		policy    *sriovnetworkv1.SriovNetworkNodePolicy
		nodeState *sriovnetworkv1.SriovNetworkNodeState
		wantErr   bool
		wantSkip  bool
		check     func(c *sriovdrav1alpha1.Config)
	}{
		{
			name:    "nil policy",
			policy:  nil,
			wantErr: true,
		},
		{
			name: "vendor and deviceID and pfNames and rootDevices",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "intel_nic",
					NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
						Vendor:      "8086",
						DeviceID:    deviceIDPassthrough,
						PfNames:     pfNames,
						RootDevices: rootDevices,
					},
					NumVfs: 0,
				},
			},
			nodeState: nil,
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.DeviceAttributesSelector).NotTo(BeNil(), "DeviceAttributesSelector should match pool %q", poolLabel)
				Expect(c.DeviceAttributesSelector.MatchLabels[consts.DRAResourcePoolLabel]).To(Equal(poolLabel), "DeviceAttributesSelector should match pool %q", poolLabel)
				Expect(c.ResourceFilters).To(HaveLen(1), "expected one ResourceFilter, got %d", len(c.ResourceFilters))
				f := c.ResourceFilters[0]
				Expect(f.Vendors).To(HaveLen(1), "Vendors want [8086], got %v", f.Vendors)
				Expect(f.Vendors[0]).To(Equal("8086"), "Vendors want [8086], got %v", f.Vendors)
				Expect(f.Devices).To(HaveLen(1), "Devices want [%s], got %v", deviceIDPassthrough, f.Devices)
				Expect(f.Devices[0]).To(Equal(deviceIDPassthrough), "Devices want [%s], got %v", deviceIDPassthrough, f.Devices)
				Expect(f.PfNames).To(Equal(pfNames), "PfNames: %v", cmp.Diff(f.PfNames, pfNames))
				Expect(f.PfPciAddresses).To(Equal(rootDevices), "PfPciAddresses: %v", cmp.Diff(f.PfPciAddresses, rootDevices))
			},
		},
		{
			name: "vfio driver",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "vfio_nic",
					DeviceType:   consts.DeviceTypeVfioPci,
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
				},
			},
			nodeState: nil,
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.ResourceFilters).To(HaveLen(1), "expected one ResourceFilter, got %d", len(c.ResourceFilters))
				Expect(c.ResourceFilters[0].Drivers).To(HaveLen(1), "Drivers want [vfio-pci], got %v", c.ResourceFilters[0].Drivers)
				Expect(c.ResourceFilters[0].Drivers[0]).To(Equal("vfio-pci"), "Drivers want [vfio-pci], got %v", c.ResourceFilters[0].Drivers)
			},
		},
		{
			name: "netFilter nic selector is skipped",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "openstack_nic",
					NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
						NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
					},
				},
			},
			nodeState: nil,
			wantSkip:  true,
		},
		{
			name: "empty nic selector omits ResourceFilter",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "intel_nic",
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
				},
			},
			nodeState: nil,
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.DeviceAttributesSelector).NotTo(BeNil(), "DeviceAttributesSelector should match pool %q", poolLabel)
				Expect(c.DeviceAttributesSelector.MatchLabels[consts.DRAResourcePoolLabel]).To(Equal(poolLabel), "DeviceAttributesSelector should match pool %q", poolLabel)
				Expect(c.ResourceFilters).To(HaveLen(0), "empty nic selector should omit ResourceFilter, got %+v", c.ResourceFilters)
			},
		},
		{
			name: "deviceID with NumVfs uses GetVfDeviceID",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "nic",
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{DeviceID: pfDeviceID},
					NumVfs:       4,
				},
			},
			nodeState: nil,
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.ResourceFilters).To(HaveLen(1), "expected one ResourceFilter, got %d", len(c.ResourceFilters))
				f := c.ResourceFilters[0]
				Expect(f.Devices).To(Equal([]string{"154c"}), "Devices want [154c], got %v", f.Devices)
			},
		},
		{
			name: "PfNames resolved via nodeState altNames",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "nic",
					NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
						PfNames: []string{"enp3s0np0"},
					},
					NumVfs: 4,
				},
			},
			nodeState: &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{
							Name:     "ens1f0",
							AltNames: []string{"enp3s0np0"},
						},
					},
				},
			},
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.ResourceFilters).To(HaveLen(1), "expected one ResourceFilter, got %d", len(c.ResourceFilters))
				f := c.ResourceFilters[0]
				Expect(f.PfNames).To(HaveLen(1), "PfNames want [ens1f0], got %v", f.PfNames)
				Expect(f.PfNames[0]).To(Equal("ens1f0"), "PfNames want [ens1f0], got %v", f.PfNames)
			},
		},
		{
			name: "linkType eth",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "nic",
					LinkType:     consts.LinkTypeETH,
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
				},
			},
			nodeState: nil,
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.ResourceFilters).To(HaveLen(1), "expected one ResourceFilter, got %d", len(c.ResourceFilters))
				want := strings.ToLower(consts.LinkTypeETH)
				Expect(c.ResourceFilters[0].LinkType).To(Equal(want), "LinkType want %q, got %q", want, c.ResourceFilters[0].LinkType)
			},
		},
		{
			name: "linkType ib",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "nic",
					LinkType:     consts.LinkTypeIB,
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
				},
			},
			nodeState: nil,
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.ResourceFilters).To(HaveLen(1), "expected one ResourceFilter, got %d", len(c.ResourceFilters))
				want := strings.ToLower(consts.LinkTypeIB)
				Expect(c.ResourceFilters[0].LinkType).To(Equal(want), "LinkType want %q, got %q", want, c.ResourceFilters[0].LinkType)
			},
		},
		{
			name: "linkType omitted for vfio-pci",
			policy: &sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "vfio_nic",
					DeviceType:   consts.DeviceTypeVfioPci,
					LinkType:     consts.LinkTypeETH,
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
				},
			},
			nodeState: nil,
			check: func(c *sriovdrav1alpha1.Config) {
				Expect(c.ResourceFilters).To(HaveLen(1), "expected one ResourceFilter, got %d", len(c.ResourceFilters))
				f := c.ResourceFilters[0]
				Expect(f.LinkType).To(Equal(""), "LinkType want empty for vfio-pci, got %q", f.LinkType)
				Expect(f.Drivers).To(HaveLen(1), "Drivers want [vfio-pci], got %v", f.Drivers)
				Expect(f.Drivers[0]).To(Equal("vfio-pci"), "Drivers want [vfio-pci], got %v", f.Drivers)
			},
		},
	}
	for _, tc := range testCases {
		By(tc.name, func() {
			cfg, skip, err := buildPolicyConfig(tc.policy, tc.nodeState)
			if tc.wantSkip {
				Expect(skip).To(BeTrue(), "buildPolicyConfig: expected skip with reason error, got skip=%v cfg=%v err=%v", skip, cfg, err)
				Expect(cfg).To(BeNil(), "buildPolicyConfig: expected skip with reason error, got skip=%v cfg=%v err=%v", skip, cfg, err)
				Expect(err).To(HaveOccurred(), "buildPolicyConfig: expected skip with reason error, got skip=%v cfg=%v err=%v", skip, cfg, err)
				return
			}
			if tc.wantErr {
				Expect(err).To(HaveOccurred(), "buildPolicyConfig: expected error, got skip=%v err=%v", skip, err)
				Expect(skip).To(BeFalse(), "buildPolicyConfig: expected error, got skip=%v err=%v", skip, err)
				Expect(cfg).To(BeNil(), "buildPolicyConfig: expected error, got skip=%v err=%v", skip, err)
				return
			}
			Expect(err).NotTo(HaveOccurred(), "buildPolicyConfig: skip=%v err=%v", skip, err)
			Expect(skip).To(BeFalse(), "buildPolicyConfig: skip=%v err=%v", skip, err)
			tc.check(cfg)
		})
	}
})

var _ = It("rejects unmapped device IDs without broadening filters", func() {
	oldNicIDMap := sriovnetworkv1.NicIDMap
	sriovnetworkv1.NicIDMap = []string{"8086 1572 154c"}
	DeferCleanup(func() { sriovnetworkv1.NicIDMap = oldNicIDMap })
	for _, vendor := range []string{"", "8086"} {
		By("vendor="+vendor, func() {
			policy := &sriovnetworkv1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "unknown-model"},
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "nic", NumVfs: 4,
					NicSelector: sriovnetworkv1.SriovNetworkNicSelector{Vendor: vendor, DeviceID: "ffff"},
				},
			}
			cfg, skip, err := buildPolicyConfig(policy, nil)
			Expect(err).To(HaveOccurred(), "expected configuration error without a broadened filter, got config=%+v skip=%t err=%v", cfg, skip, err)
			Expect(skip).To(BeFalse(), "expected configuration error without a broadened filter, got config=%+v skip=%t err=%v", cfg, skip, err)
			Expect(cfg).To(BeNil(), "expected configuration error without a broadened filter, got config=%+v skip=%t err=%v", cfg, skip, err)
			Expect(err.Error()).To(ContainSubstring(policy.Name), "error should identify the policy and unmapped device ID: %v", err)
			Expect(err.Error()).To(ContainSubstring("ffff"), "error should identify the policy and unmapped device ID: %v", err)
		})
	}
})

var _ = It("retries policy publication after an attribute write failure", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		sriovnetworkv1.AddToScheme, sriovdrav1alpha1.AddToScheme, corev1.AddToScheme,
	} {
		utilruntime.Must(add(scheme))
	}
	owner := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
		Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner",
	}, Spec: sriovnetworkv1.SriovOperatorConfigSpec{FeatureGates: map[string]bool{
		consts.DynamicResourceAllocationFeatureGate: true,
	}}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-0", Labels: map[string]string{
			corev1.LabelHostname: "worker-0", "node-role.kubernetes.io/worker": "", "kubernetes.io/os": "linux",
		},
	}}
	policy := sriovnetworkv1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "new-policy", Namespace: vars.Namespace, UID: "new-policy-uid", Generation: 1},
		Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			ResourceName: "new_nic",
			NodeSelector: map[string]string{"kubernetes.io/hostname": "worker-0"},
			NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
		},
	}
	state := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{
		Name: node.Name, Namespace: vars.Namespace, Generation: 1,
	}, Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
		Interfaces: sriovnetworkv1.InterfaceExts{{Vendor: "8086"}},
		Conditions: []metav1.Condition{{
			Type:               sriovnetworkv1.ConditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
		}},
	}}
	Expect(state.SetPolicyRevisions(map[string]sriovnetworkv1.SourceRevision{
		policy.Name: {UID: policy.UID, Generation: policy.Generation},
	})).To(Succeed())
	obsolete := &sriovdrav1alpha1.SriovResourcePolicy{ObjectMeta: metav1.ObjectMeta{
		Name: "obsolete", Namespace: vars.Namespace, UID: "obsolete-policy",
		Labels: maps.Clone(consts.DRAOperatorLabels),
	}}
	writeErr := fmt.Errorf("injected DeviceAttributes write failure")
	failAttributeWrite := true
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(owner, node, state, obsolete, &policy).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.CreateOption) error {
			if failAttributeWrite {
				if _, ok := obj.(*sriovdrav1alpha1.DeviceAttributes); ok {
					return writeErr
				}
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: nodePolicySyncEventName}}
	{
		_, err := r.Reconcile(ctx, req)
		Expect(err).To(MatchError(writeErr), "Reconcile error = %v, want %v", err, writeErr)
	}
	{
		err := cl.Get(ctx, k8sclient.ObjectKeyFromObject(obsolete), &sriovdrav1alpha1.SriovResourcePolicy{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "obsolete policy should be deleted, got %v", err)
	}
	{
		err := cl.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: node.Name}, &sriovdrav1alpha1.SriovResourcePolicy{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "new policy should not be published, got %v", err)
	}

	failAttributeWrite = false
	{
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred(), "retry Reconcile: %v", err)
	}
	{
		err := cl.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: node.Name}, &sriovdrav1alpha1.SriovResourcePolicy{})
		Expect(err).NotTo(HaveOccurred(), "new policy should be published after retry: %v", err)
	}
})

var _ = It("fails DeviceAttributes sync when its REST mapping is unavailable", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })

	scheme := runtime.NewScheme()
	utilruntime.Must(sriovdrav1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c k8sclient.WithWatch, list k8sclient.ObjectList, opts ...k8sclient.ListOption) error {
			if _, ok := list.(*sriovdrav1alpha1.DeviceAttributesList); ok {
				return &apimeta.NoKindMatchError{GroupKind: schema.GroupKind{Kind: "DeviceAttributes"}}
			}
			return c.List(ctx, list, opts...)
		},
	}).Build()
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}

	err := r.syncDeviceAttributes(context.Background(), nil, &sriovnetworkv1.SriovNetworkNodePolicyList{})
	Expect(err).To(MatchError(ContainSubstring("DeviceAttributes REST mapping is unavailable")))
})

var _ = It("preserves node state policy precedence in DRA filters", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })

	for _, tc := range []struct {
		name              string
		genericName       string
		dedicatedName     string
		genericPriority   int
		dedicatedPriority int
		wantResource      string
	}{
		{
			name:        "numeric priority overrides name order",
			genericName: "z-generic", dedicatedName: "a-dedicated",
			genericPriority: 99, dedicatedPriority: 10, wantResource: "dedicated",
		},
		{
			name:        "equal priority with dedicated policy last",
			genericName: "a-generic", dedicatedName: "z-dedicated",
			genericPriority: 10, dedicatedPriority: 10, wantResource: "dedicated",
		},
		{
			name:        "equal priority with generic policy last",
			genericName: "z-generic", dedicatedName: "a-dedicated",
			genericPriority: 10, dedicatedPriority: 10, wantResource: "generic",
		},
	} {
		By(tc.name, func() {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "worker-0", Labels: map[string]string{corev1.LabelHostname: "worker-0"},
			}}
			state := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Name: "ens1f0", PciAddress: "0000:86:00.0", Vendor: "8086", TotalVfs: 8},
						{Name: "ens1f1", PciAddress: "0000:86:00.1", Vendor: "8086", TotalVfs: 8},
					},
				},
			}
			policies := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: tc.genericName},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							Priority: tc.genericPriority, ResourceName: "generic", NumVfs: 4,
							DeviceType:   consts.DeviceTypeNetDevice,
							NodeSelector: node.Labels,
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: tc.dedicatedName},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							Priority: tc.dedicatedPriority, ResourceName: "dedicated", NumVfs: 4,
							DeviceType:   consts.DeviceTypeNetDevice,
							NodeSelector: node.Labels,
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1f0"}},
						},
					},
				},
			}
			// Reconcile sorts once and passes the same order to both renderers.
			sort.Sort(sriovnetworkv1.ByPriority(policies.Items))
			Expect(renderNodePolicies(state, policies, node, false)).To(Succeed())
			Expect(state.Spec.Interfaces).To(HaveLen(2))
			for _, iface := range state.Spec.Interfaces {
				wantResource := "generic"
				if iface.PciAddress == "0000:86:00.0" {
					wantResource = tc.wantResource
				}
				Expect(iface.VfGroups).To(HaveLen(1))
				Expect(iface.VfGroups[0].ResourceName).To(Equal(wantResource))
			}

			scheme := runtime.NewScheme()
			Expect(sriovnetworkv1.AddToScheme(scheme)).To(Succeed())
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(state).Build()
			r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
			policy, _, err := r.renderSriovResourcePolicyForNode(context.Background(), policies, node,
				map[string]struct{}{"generic": {}, "dedicated": {}})
			Expect(err).NotTo(HaveOccurred())
			Expect(policy).NotTo(BeNil())

			// Both filters match ens1f0's VFs: the driver's first match must agree
			// with NodeState. Only the vendor filter matches ens1f1's VFs.
			wantConfigs := []sriovdrav1alpha1.Config{
				{
					DeviceAttributesSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{consts.DRAResourcePoolLabel: "dedicated"},
					},
					ResourceFilters: []sriovdrav1alpha1.ResourceFilter{{PfNames: []string{"ens1f0"}}},
				},
				{
					DeviceAttributesSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{consts.DRAResourcePoolLabel: "generic"},
					},
					ResourceFilters: []sriovdrav1alpha1.ResourceFilter{{Vendors: []string{"8086"}}},
				},
			}
			if tc.wantResource == "generic" {
				wantConfigs[0], wantConfigs[1] = wantConfigs[1], wantConfigs[0]
			}
			Expect(policy.Spec.Configs).To(Equal(wantConfigs))
		})
	}
})

var _ = It("recognizes an unavailable DeviceClass API", func() {
	list := &resourceapi.DeviceClassList{}
	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	err := cl.List(context.Background(), list)
	Expect(err).To(HaveOccurred(), "expected list error without DeviceClass in scheme")
	Expect(deviceClassAPIUnavailable(err)).To(BeTrue(), "deviceClassAPIUnavailable() = false, want true for %v", err)
})

var _ = It("builds an owned device class with an extended resource name", func() {
	deviceClassName := "intel-nic"
	resourceName := "intel_nic"
	extendedResourceName := "openshift.io/intel_nic"
	celExpr := `device.driver == "sriovnetwork.k8snetworkplumbingwg.io" && device.attributes["k8s.cni.cncf.io"].resourceName == "openshift.io/intel_nic"`

	obj, err := buildExtendedResourceDeviceClass(deviceClassName, resourceName, extendedResourceName, celExpr, "")
	Expect(err).NotTo(HaveOccurred())

	Expect(obj.Name).To(Equal(deviceClassName), "Name = %q, want %q", obj.Name, deviceClassName)
	Expect(obj.Labels[consts.DRAGeneratedByLabel]).To(Equal(consts.SriovNetworkOperatorIdentifier), "generated-by label: %v", obj.Labels)
	Expect(obj.Labels[consts.DRADeviceClassResourceNameLabel]).To(Equal(resourceName), "resource-name label: %v", obj.Labels)
	Expect(obj.Spec.ExtendedResourceName).NotTo(BeNil(), "spec.extendedResourceName = %v", obj.Spec.ExtendedResourceName)
	Expect(*obj.Spec.ExtendedResourceName).To(Equal(extendedResourceName), "spec.extendedResourceName = %v", obj.Spec.ExtendedResourceName)
	Expect(obj.Spec.Selectors).To(HaveLen(1), "spec.selectors = %v", obj.Spec.Selectors)
	Expect(obj.Spec.Selectors[0].CEL).NotTo(BeNil(), "spec.selectors = %v", obj.Spec.Selectors)
	Expect(obj.Spec.Selectors[0].CEL.Expression).To(Equal(celExpr), "spec.selectors[0].cel.expression = %q, want %q", obj.Spec.Selectors[0].CEL.Expression, celExpr)
})

var _ = It("updates device classes when extended resource support changes", func() {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	Expect(resourceapi.AddToScheme(scheme)).To(Succeed())

	desired, err := buildExtendedResourceDeviceClass(
		"intel-nic", "intel_nic", "openshift.io/intel_nic", buildDeviceClassCEL("intel_nic"), "")
	Expect(err).NotTo(HaveOccurred())
	existing := desired.DeepCopy()
	existing.Spec.ExtendedResourceName = nil

	featureEnabled := false
	actualUpdates := 0
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.UpdateOption) error {
			updateOpts := k8sclient.UpdateOptions{}
			for _, opt := range opts {
				opt.ApplyToUpdate(&updateOpts)
			}
			if len(updateOpts.DryRun) != 0 {
				if !featureEnabled {
					obj.(*resourceapi.DeviceClass).Spec.ExtendedResourceName = nil
				}
				return nil
			}
			actualUpdates++
			return c.Update(ctx, obj, opts...)
		},
	}).Build()
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}

	Expect(r.reconcileExtendedDeviceClass(ctx, desired, existing)).To(Succeed())
	Expect(actualUpdates).To(BeZero(), "unsupported extendedResourceName caused %d persisted updates", actualUpdates)

	featureEnabled = true
	Expect(r.reconcileExtendedDeviceClass(ctx, desired, existing)).To(Succeed())
	Expect(actualUpdates).To(BeNumerically("==", 1), "supported extendedResourceName caused %d persisted updates, want 1", actualUpdates)
	got := &resourceapi.DeviceClass{}
	Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(desired), got)).To(Succeed())
	Expect(got.Spec.ExtendedResourceName).NotTo(BeNil(), "extendedResourceName = %v, want supported value", got.Spec.ExtendedResourceName)
	Expect(*got.Spec.ExtendedResourceName).To(Equal("openshift.io/intel_nic"), "extendedResourceName = %v, want supported value", got.Spec.ExtendedResourceName)
})

var _ = It("rejects conflicting device types before DRA publication", func() {
	scheme := runtime.NewScheme()
	Expect(sriovdrav1alpha1.AddToScheme(scheme)).To(Succeed())
	r := &SriovNetworkNodePolicyReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
		{ObjectMeta: metav1.ObjectMeta{Name: "vfio"}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "shared", DeviceType: consts.DeviceTypeVfioPci}},
		{ObjectMeta: metav1.ObjectMeta{Name: "netdevice"}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "shared", DeviceType: consts.DeviceTypeNetDevice}},
	}}
	ctx := context.Background()
	// All three sync paths must reject conflicting policies before publication,
	// including when admission was bypassed or policies already existed.
	for name, sync := range map[string]func() error{
		"classes":    func() error { return r.syncExtendedResourceDeviceClasses(ctx, nil, pl) },
		"attributes": func() error { return r.syncDeviceAttributes(ctx, nil, pl) },
		"resource policies": func() error {
			return r.syncSriovResourcePolicies(ctx, nil, pl, &corev1.NodeList{}, true)
		},
	} {
		By(name, func() {
			{
				err := sync()
				Expect(err).To(HaveOccurred(), "expected conflicting device types, got %v", err)
				Expect(err.Error()).To(ContainSubstring("incompatible deviceType"), "expected conflicting device types, got %v", err)
			}
		})
	}
})

var _ = It("synchronizes VFIO configuration and repairs drift", func() {
	ctx := context.Background()
	previousPrefix := vars.ResourcePrefix
	vars.ResourcePrefix = "openshift.io"
	DeferCleanup(func() { vars.ResourcePrefix = previousPrefix })
	scheme := runtime.NewScheme()
	Expect(resourceapi.AddToScheme(scheme)).To(Succeed())
	r := &SriovNetworkNodePolicyReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
		{ObjectMeta: metav1.ObjectMeta{Name: "vfio"}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			ResourceName: "dpdk_vfio", DeviceType: consts.DeviceTypeVfioPci,
		}},
	}}
	for step, deviceType := range []string{consts.DeviceTypeVfioPci, consts.DeviceTypeVfioPci, consts.DeviceTypeNetDevice, consts.DeviceTypeVfioPci} {
		pl.Items[0].Spec.DeviceType = deviceType
		Expect(r.syncExtendedResourceDeviceClasses(ctx, nil, pl)).To(Succeed())
		got := &resourceapi.DeviceClass{}
		Expect(r.Get(ctx, k8sclient.ObjectKey{Name: "dpdk-vfio"}, got)).To(Succeed())
		Expect(got.Spec.ExtendedResourceName).NotTo(BeNil(), "unexpected extended resource name: %v", got.Spec.ExtendedResourceName)
		Expect(*got.Spec.ExtendedResourceName).To(Equal("openshift.io/dpdk_vfio"), "unexpected extended resource name: %v", got.Spec.ExtendedResourceName)
		version := got.ResourceVersion
		Expect(r.syncExtendedResourceDeviceClasses(ctx, nil, pl)).To(Succeed())
		Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(got), got)).To(Succeed())
		Expect(got.ResourceVersion).To(Equal(version), "unchanged configuration caused a redundant DeviceClass update")
		if deviceType == consts.DeviceTypeNetDevice {
			Expect(got.Spec.Config).To(HaveLen(0), "netdevice class retained VFIO configuration: %+v", got.Spec.Config)
			continue
		}
		Expect(got.Spec.Config).To(HaveLen(1), "VFIO class must supply one opaque configuration, got %+v", got.Spec.Config)
		Expect(got.Spec.Config[0].Opaque).NotTo(BeNil(), "VFIO class must supply one opaque configuration, got %+v", got.Spec.Config)
		opaque := got.Spec.Config[0].Opaque
		Expect(opaque.Driver).To(Equal(consts.DRADriverBaseDeviceClassName), "unexpected DRA driver: %s", opaque.Driver)
		// Decode the serialized parameters with the pinned driver's real decoder.
		raw, err := json.Marshal(opaque.Parameters)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := runtime.Decode(configapi.Decoder, raw)
		Expect(err).NotTo(HaveOccurred())
		config, ok := decoded.(*configapi.VfConfig)
		Expect(ok).To(BeTrue(), "unexpected VFIO configuration: %+v", decoded)
		Expect(config.Driver).To(Equal(consts.DeviceTypeVfioPci), "unexpected VFIO configuration: %+v", decoded)
		// Simulate configuration drift; the next sync must restore it.
		if step == 0 {
			got.Spec.Config = nil
			Expect(r.Update(ctx, got)).To(Succeed())
		}
	}
})

var _ = It("recognizes only matching device class ownership markers", func() {
	dc := &resourceapi.DeviceClass{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{consts.DRADeviceClassResourceNameLabel: "intel_nic"},
	}}
	Expect(extendedDeviceClassHasOwnershipMarker(dc, "intel_nic")).To(BeTrue(), "expected ownership marker for matching resource name")
	Expect(extendedDeviceClassHasOwnershipMarker(dc, "other")).To(BeFalse(), "expected no ownership marker for mismatched resource name")
	Expect(extendedDeviceClassHasOwnershipMarker(&resourceapi.DeviceClass{}, "intel_nic")).To(BeFalse(), "expected no ownership marker without labels")
})

var _ = It("preserves a foreign device class on a name conflict", func() {
	ctx := context.Background()
	nsSaved := vars.Namespace
	prefixSaved := vars.ResourcePrefix
	vars.Namespace = testNamespace
	vars.ResourcePrefix = "openshift.io"
	defer func() {
		vars.Namespace = nsSaved
		vars.ResourcePrefix = prefixSaved
	}()

	scheme := runtime.NewScheme()
	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	utilruntime.Must(sriovdrav1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(resourceapi.AddToScheme(scheme))
	dc := &sriovnetworkv1.SriovOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: testNamespace},
	}
	foreign := &resourceapi.DeviceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "intel-nic"},
		Spec: resourceapi.DeviceClassSpec{
			Selectors: []resourceapi.DeviceSelector{
				{CEL: &resourceapi.CELDeviceSelector{Expression: `device.driver == "other"`}},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dc, foreign).Build()
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
	pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
		Items: []sriovnetworkv1.SriovNetworkNodePolicy{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
				Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
			},
		},
	}
	err := r.syncExtendedResourceDeviceClasses(ctx, dc, pl)
	Expect(err).To(HaveOccurred(), "expected conflict when DeviceClass name collides without ownership marker")
	Expect(errors.IsConflict(err)).To(BeTrue(), "expected conflict error, got %v", err)
	got := &resourceapi.DeviceClass{}
	{
		err := cl.Get(ctx, k8sclient.ObjectKey{Name: "intel-nic"}, got)
		Expect(err).NotTo(HaveOccurred(), "Get foreign DeviceClass: %v", err)
	}
	Expect(got.Spec.Selectors[0].CEL.Expression).To(Equal(`device.driver == "other"`), "foreign DeviceClass spec was modified: %q", got.Spec.Selectors[0].CEL.Expression)
})

var _ = Describe("SriovNetworkNodePolicyReconciler DRA", Ordered, func() {
	Context("DRA sync and cleanup", func() {
		var (
			ctx         context.Context
			scheme      *runtime.Scheme
			r           *SriovNetworkNodePolicyReconciler
			dc          *sriovnetworkv1.SriovOperatorConfig
			nsSaved     string
			prefixSaved string
		)

		beforeEachDRA := func(objs ...k8sclient.Object) {
			ctx = context.Background()
			nsSaved = vars.Namespace
			prefixSaved = vars.ResourcePrefix
			vars.Namespace = testNamespace
			vars.ResourcePrefix = "openshift.io"
			DeferCleanup(func() {
				vars.Namespace = nsSaved
				vars.ResourcePrefix = prefixSaved
			})
			scheme = runtime.NewScheme()
			utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
			utilruntime.Must(sriovdrav1alpha1.AddToScheme(scheme))
			utilruntime.Must(corev1.AddToScheme(scheme))
			utilruntime.Must(resourceapi.AddToScheme(scheme))
			dc = &sriovnetworkv1.SriovOperatorConfig{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: testNamespace, UID: "operator-owner"},
			}
			allObjs := append([]k8sclient.Object{dc}, objs...)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(allObjs...).Build()
			r = &SriovNetworkNodePolicyReconciler{Client: client, Scheme: scheme}
		}

		It("DRA sync and cleanup are idempotent across repeated reconcile", func() {
			nodeName := "worker-0"
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"node-role.kubernetes.io/worker": "",
						"kubernetes.io/hostname":         nodeName,
					},
				},
			}
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Vendor: "8086", Driver: "i40e", PciAddress: "0000:86:00.0"},
					},
				},
			}
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "intel_nic",
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
						},
					},
				},
			}
			Expect(renderNodePolicies(nodeState, pl, node, false)).To(Succeed())
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)
			beforeEachDRA(node, nodeState)
			nl := &corev1.NodeList{Items: []corev1.Node{*node}}

			attrList := &sriovdrav1alpha1.DeviceAttributesList{}
			var attrVersion string
			for i := range 3 {
				Expect(r.syncDeviceAttributes(ctx, dc, pl)).To(Succeed())
				Expect(r.List(ctx, attrList, k8sclient.InNamespace(testNamespace),
					k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
				Expect(attrList.Items).To(HaveLen(1))
				if i == 0 {
					attrVersion = attrList.Items[0].ResourceVersion
					Expect(attrVersion).NotTo(BeEmpty())
				} else {
					Expect(attrList.Items[0].ResourceVersion).To(Equal(attrVersion), "repeated sync must not update DeviceAttributes")
				}
			}

			policyList := &sriovdrav1alpha1.SriovResourcePolicyList{}
			var policyVersion string
			for i := range 3 {
				Expect(r.syncSriovResourcePolicies(ctx, dc, pl, nl, true)).To(Succeed())
				Expect(r.List(ctx, policyList, k8sclient.InNamespace(testNamespace),
					k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
				Expect(policyList.Items).To(HaveLen(1))
				if i == 0 {
					policyVersion = policyList.Items[0].ResourceVersion
					Expect(policyVersion).NotTo(BeEmpty())
				} else {
					Expect(policyList.Items[0].ResourceVersion).To(Equal(policyVersion), "repeated sync must not update SriovResourcePolicy")
				}
			}

			dcList := &resourceapi.DeviceClassList{}
			var classVersion string
			for i := range 3 {
				Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, pl)).To(Succeed())
				Expect(r.List(ctx, dcList, k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
				Expect(dcList.Items).To(HaveLen(1))
				if i == 0 {
					classVersion = dcList.Items[0].ResourceVersion
					Expect(classVersion).NotTo(BeEmpty())
				} else {
					Expect(dcList.Items[0].ResourceVersion).To(Equal(classVersion), "repeated sync must not update DeviceClass")
				}
			}

			for range 3 {
				Expect(r.cleanupSriovResourcePoliciesAndDeviceAttributes(ctx, dc)).To(Succeed())
				Expect(cleanupExtendedResourceDeviceClasses(ctx, r.Client)).To(Succeed())
			}
			Expect(r.List(ctx, attrList, k8sclient.InNamespace(testNamespace),
				k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(attrList.Items).To(BeEmpty())
			Expect(r.List(ctx, policyList, k8sclient.InNamespace(testNamespace),
				k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(policyList.Items).To(BeEmpty())
			Expect(r.List(ctx, dcList, k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(dcList.Items).To(BeEmpty())
		})

		It("syncDeviceAttributes adopts CR when operator managed label was removed", func() {
			attr := buildDeviceAttributesCR("intel-nic-attrs", "intel_nic")
			attr.Labels = map[string]string{consts.DRAResourcePoolLabel: "intel-nic"}
			attr.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(
				&sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, UID: "operator-owner"}},
				sriovnetworkv1.SchemeGroupVersion.WithKind("SriovOperatorConfig"))}
			beforeEachDRA(attr)
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			Expect(r.syncDeviceAttributes(ctx, dc, pl)).To(Succeed())
			got := &sriovdrav1alpha1.DeviceAttributes{}
			Expect(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "intel-nic-attrs"}, got)).To(Succeed())
			Expect(got.Labels[consts.DRAGeneratedByLabel]).To(Equal(consts.SriovNetworkOperatorIdentifier))
			Expect(got.Labels[consts.DRAResourcePoolLabel]).To(Equal("intel-nic"))
			Expect(metav1.IsControlledBy(got, dc)).To(BeTrue(), "adopted DeviceAttributes should reference SriovOperatorConfig")
		})

		It("syncDeviceAttributes restores controller owner reference when labels and spec already match", func() {
			beforeEachDRA()
			attr := buildDeviceAttributesCR("intel-nic-attrs", "intel_nic")
			attr.OwnerReferences = nil
			Expect(r.Create(ctx, attr)).To(Succeed())
			desired := buildDeviceAttributesCR("intel-nic-attrs", "intel_nic")
			Expect(controllerutil.SetControllerReference(dc, desired, scheme)).To(Succeed())
			Expect(attr.Namespace).To(Equal(desired.Namespace))
			Expect(attr.Spec).To(Equal(desired.Spec))
			Expect(attr.Labels).To(Equal(desired.Labels))
			Expect(attr.OwnerReferences).To(BeEmpty())
			Expect(desired.OwnerReferences).NotTo(BeEmpty())
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			Expect(r.syncDeviceAttributes(ctx, dc, pl)).To(Succeed())
			got := &sriovdrav1alpha1.DeviceAttributes{}
			Expect(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "intel-nic-attrs"}, got)).To(Succeed())
			Expect(metav1.IsControlledBy(got, dc)).To(BeTrue())
			Expect(got.Spec).To(Equal(attr.Spec))
			Expect(got.Labels).To(Equal(attr.Labels))
		})

		It("syncSriovResourcePolicies adopts CR when operator managed label was removed", func() {
			nodeName := "worker-0"
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"node-role.kubernetes.io/worker": "",
						"kubernetes.io/hostname":         nodeName,
					},
				},
			}
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Vendor: "8086", Driver: "i40e", PciAddress: "0000:86:00.0"},
					},
				},
			}
			existingPolicy := &sriovdrav1alpha1.SriovResourcePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodeName,
					Namespace: testNamespace,
					Labels:    map[string]string{consts.DRASriovResourcePolicyNodeLabel: nodeName},
				},
				Spec: sriovdrav1alpha1.SriovResourcePolicySpec{},
			}
			existingPolicy.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(
				&sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, UID: "operator-owner"}},
				sriovnetworkv1.SchemeGroupVersion.WithKind("SriovOperatorConfig"))}
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "intel_nic",
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
						},
					},
				},
			}
			Expect(renderNodePolicies(nodeState, pl, node, false)).To(Succeed())
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)
			beforeEachDRA(node, nodeState, existingPolicy)
			nl := &corev1.NodeList{Items: []corev1.Node{*node}}
			Expect(r.syncSriovResourcePolicies(ctx, dc, pl, nl, true)).To(Succeed())
			got := &sriovdrav1alpha1.SriovResourcePolicy{}
			Expect(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: nodeName}, got)).To(Succeed())
			Expect(got.Labels[consts.DRAGeneratedByLabel]).To(Equal(consts.SriovNetworkOperatorIdentifier))
			Expect(got.Spec.Configs).To(HaveLen(1))
			Expect(metav1.IsControlledBy(got, dc)).To(BeTrue(), "adopted SriovResourcePolicy should reference SriovOperatorConfig")
		})

		It("syncExtendedResourceDeviceClasses returns conflict when DeviceClass name collides without ownership marker", func() {
			foreign := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{Name: "intel-nic"},
				Spec: resourceapi.DeviceClassSpec{
					Selectors: []resourceapi.DeviceSelector{
						{CEL: &resourceapi.CELDeviceSelector{Expression: `device.driver == "other"`}},
					},
				},
			}
			beforeEachDRA(foreign)
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			err := r.syncExtendedResourceDeviceClasses(ctx, dc, pl)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsConflict(err)).To(BeTrue())
			got := &resourceapi.DeviceClass{}
			Expect(r.Get(ctx, k8sclient.ObjectKey{Name: "intel-nic"}, got)).To(Succeed())
			Expect(got.Spec.Selectors[0].CEL.Expression).To(Equal(`device.driver == "other"`))
		})

		It("syncExtendedResourceDeviceClasses adopts CR when operator managed label was removed", func() {
			obj, err := buildExtendedResourceDeviceClass("intel-nic", "intel_nic", "openshift.io/intel_nic", buildDeviceClassCEL("intel_nic"), "")
			Expect(err).NotTo(HaveOccurred())
			obj.Labels = map[string]string{consts.DRADeviceClassResourceNameLabel: "intel_nic"}
			beforeEachDRA(obj)
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, pl)).To(Succeed())
			got := &resourceapi.DeviceClass{}
			Expect(r.Get(ctx, k8sclient.ObjectKey{Name: "intel-nic"}, got)).To(Succeed())
			Expect(got.Labels[consts.DRAGeneratedByLabel]).To(Equal(consts.SriovNetworkOperatorIdentifier))
		})

		It("syncDeviceAttributes creates DeviceAttributes for each policy resource name", func() {
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			beforeEachDRA()
			Expect(r.syncDeviceAttributes(ctx, dc, pl)).To(Succeed())
			attr := &sriovdrav1alpha1.DeviceAttributes{}
			Expect(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "intel-nic-attrs"}, attr)).To(Succeed())
			Expect(attr.Labels[consts.DRAResourcePoolLabel]).To(Equal("intel-nic"))
			key := resourceapi.QualifiedName(consts.DRAResourceNameAttributeKey)
			Expect(attr.Spec.Attributes).To(HaveKey(key))
			Expect(attr.Spec.Attributes[key].StringValue).NotTo(BeNil())
			Expect(*attr.Spec.Attributes[key].StringValue).To(Equal("openshift.io/intel_nic"))
		})

		It("syncDeviceAttributes rejects colliding normalized names before writing", func() {
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy2", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "INTEL_NIC"},
					},
				},
			}
			beforeEachDRA()
			Expect(r.syncDeviceAttributes(ctx, dc, pl)).To(MatchError(ContainSubstring("normalizes to the same DRA device class name")))
			attrList := &sriovdrav1alpha1.DeviceAttributesList{}
			Expect(r.List(ctx, attrList, k8sclient.InNamespace(testNamespace),
				k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(attrList.Items).To(BeEmpty())
		})

		It("syncSriovResourcePolicies rejects colliding normalized names before writing", func() {
			nodeName := "worker-0"
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"node-role.kubernetes.io/worker": "",
						"kubernetes.io/hostname":         nodeName,
					},
				},
			}
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Vendor: "8086", Driver: "i40e", PciAddress: "0000:86:00.0"},
					},
				},
			}
			beforeEachDRA(node, nodeState)
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "intel_nic",
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy2", Namespace: testNamespace},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "INTEL_NIC",
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
						},
					},
				},
			}
			nl := &corev1.NodeList{Items: []corev1.Node{*node}}
			Expect(r.syncSriovResourcePolicies(ctx, dc, pl, nl, true)).To(MatchError(ContainSubstring("normalizes to the same DRA device class name")))
			policy := &sriovdrav1alpha1.SriovResourcePolicy{}
			Expect(errors.IsNotFound(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: nodeName}, policy))).To(BeTrue())
		})

		It("syncDeviceAttributes removes DeviceAttributes when resource name no longer in policies", func() {
			attrLabels := maps.Clone(consts.DRAOperatorLabels)
			attrLabels[consts.DRAResourcePoolLabel] = "old"
			attr := &sriovdrav1alpha1.DeviceAttributes{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "old-attrs",
					Namespace: testNamespace,
					Labels:    attrLabels,
				},
				Spec: sriovdrav1alpha1.DeviceAttributesSpec{Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{}},
			}
			beforeEachDRA(attr)
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{}}
			Expect(r.syncDeviceAttributes(ctx, dc, pl)).To(Succeed())
			err := r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "old-attrs"}, attr)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("retains DRA objects while a renamed resource is pending, then converges once applied", func() {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "worker-0",
				Labels: map[string]string{
					corev1.LabelHostname:             "worker-0",
					"node-role.kubernetes.io/worker": "",
				},
			}}
			oldAttrs := buildDeviceAttributesCR("old-nic-attrs", "old_nic")
			oldPolicy := &sriovdrav1alpha1.SriovResourcePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      node.Name,
					Namespace: testNamespace,
					Labels:    maps.Clone(consts.DRAOperatorLabels),
				},
				Spec: sriovdrav1alpha1.SriovResourcePolicySpec{Configs: []sriovdrav1alpha1.Config{{
					DeviceAttributesSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
						consts.DRAResourcePoolLabel: "old-nic",
					}},
				}}},
			}
			newPolicy := sriovnetworkv1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: testNamespace, UID: "policy-uid", Generation: 2},
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "new_nic",
					NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
				},
			}
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: testNamespace, Generation: 1},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{Vendor: "8086"}},
					Conditions: []metav1.Condition{{
						Type:               sriovnetworkv1.ConditionReady,
						Status:             metav1.ConditionTrue,
						ObservedGeneration: 1,
					}},
				},
			}
			Expect(nodeState.SetPolicyRevisions(map[string]sriovnetworkv1.SourceRevision{
				newPolicy.Name: {UID: newPolicy.UID, Generation: newPolicy.Generation - 1},
			})).To(Succeed())
			oldClass, err := buildExtendedResourceDeviceClass("old-nic", "old_nic", "openshift.io/old_nic", buildDeviceClassCEL("old_nic"), "")
			Expect(err).NotTo(HaveOccurred())
			beforeEachDRA(node, nodeState, oldAttrs, oldPolicy, oldClass)

			policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{newPolicy}}
			nodes := &corev1.NodeList{Items: []corev1.Node{*node}}
			Expect(r.syncDeviceAttributes(ctx, dc, policies)).To(Succeed())
			gotAttrs := &sriovdrav1alpha1.DeviceAttributes{}
			Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(oldAttrs), gotAttrs)).To(Succeed())
			Expect(r.syncSriovResourcePolicies(ctx, dc, policies, nodes, true)).To(Succeed())
			gotPolicy := &sriovdrav1alpha1.SriovResourcePolicy{}
			Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(oldPolicy), gotPolicy)).To(Succeed())
			Expect(gotPolicy.Spec.Configs[0].DeviceAttributesSelector.MatchLabels).To(HaveKeyWithValue(consts.DRAResourcePoolLabel, "old-nic"))

			// The daemon applies the renamed policy; the next reconcile publishes
			// the new pool. The old pool's attributes are still selected by the
			// pre-switch resource policy during that pass, so they survive it.
			applied := nodeState.DeepCopy()
			Expect(applied.SetPolicyRevisions(map[string]sriovnetworkv1.SourceRevision{
				newPolicy.Name: {UID: newPolicy.UID, Generation: newPolicy.Generation},
			})).To(Succeed())
			current := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(nodeState), current)).To(Succeed())
			applied.ResourceVersion = current.ResourceVersion
			Expect(r.Update(ctx, applied)).To(Succeed())

			Expect(r.syncDeviceAttributes(ctx, dc, policies)).To(Succeed())
			Expect(r.syncSriovResourcePolicies(ctx, dc, policies, nodes, true)).To(Succeed())
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, policies)).To(Succeed())
			gotPolicy = &sriovdrav1alpha1.SriovResourcePolicy{}
			Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(oldPolicy), gotPolicy)).To(Succeed())
			Expect(gotPolicy.Spec.Configs).To(HaveLen(1))
			Expect(gotPolicy.Spec.Configs[0].DeviceAttributesSelector.MatchLabels).To(HaveKeyWithValue(consts.DRAResourcePoolLabel, "new-nic"))
			Expect(gotPolicy.Spec.Configs[0].ResourceFilters[0].Vendors).To(Equal([]string{"8086"}))
			Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(oldAttrs), &sriovdrav1alpha1.DeviceAttributes{})).To(Succeed(),
				"attributes of the retired pool must survive the pass that switches the resource policy")

			// Once no resource policy selects the old pool, the next reconcile
			// prunes it together with its device class.
			Expect(r.syncDeviceAttributes(ctx, dc, policies)).To(Succeed())
			Expect(r.syncSriovResourcePolicies(ctx, dc, policies, nodes, true)).To(Succeed())
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, policies)).To(Succeed())

			newAttrs := &sriovdrav1alpha1.DeviceAttributes{}
			Expect(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "new-nic-attrs"}, newAttrs)).To(Succeed())
			attrKey := resourceapi.QualifiedName(consts.DRAResourceNameAttributeKey)
			Expect(newAttrs.Spec.Attributes[attrKey].StringValue).NotTo(BeNil())
			Expect(*newAttrs.Spec.Attributes[attrKey].StringValue).To(Equal("openshift.io/new_nic"))
			Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(oldAttrs), &sriovdrav1alpha1.DeviceAttributes{})).To(MatchError(errors.IsNotFound, "retired pool's attributes should be pruned"))
			Expect(r.Get(ctx, k8sclient.ObjectKey{Name: "new-nic"}, &resourceapi.DeviceClass{})).To(Succeed())
			Expect(r.Get(ctx, k8sclient.ObjectKey{Name: "old-nic"}, &resourceapi.DeviceClass{})).To(MatchError(errors.IsNotFound, "retired pool's device class should be pruned"))
		})

		It("retains DeviceClasses selected by controller-owned policies with a missing label until they switch pools", func() {
			oldClass, err := buildExtendedResourceDeviceClass(
				"old-nic", "old_nic", "openshift.io/old_nic", buildDeviceClassCEL("old_nic"), "")
			Expect(err).NotTo(HaveOccurred())
			beforeEachDRA(oldClass)
			oldPolicy := &sriovdrav1alpha1.SriovResourcePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "worker-0",
					Namespace: testNamespace,
				},
				Spec: sriovdrav1alpha1.SriovResourcePolicySpec{Configs: []sriovdrav1alpha1.Config{{
					DeviceAttributesSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
						consts.DRAResourcePoolLabel: "old-nic",
					}},
				}}},
			}
			Expect(controllerutil.SetControllerReference(dc, oldPolicy, scheme)).To(Succeed())
			Expect(r.Create(ctx, oldPolicy)).To(Succeed())
			policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{{
				ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: testNamespace},
				Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "new_nic"},
			}}}

			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, policies)).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Name: oldClass.Name}, &resourceapi.DeviceClass{})).To(Succeed())

			oldPolicy.Spec.Configs[0].DeviceAttributesSelector.MatchLabels[consts.DRAResourcePoolLabel] = "new-nic"
			Expect(r.Update(ctx, oldPolicy)).To(Succeed())
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, policies)).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Name: oldClass.Name}, &resourceapi.DeviceClass{})).To(MatchError(ContainSubstring("not found")))
		})

		It("syncDeviceAttributes prunes obsolete names despite pool label drift", func() {
			beforeEachDRA()
			retained := buildDeviceAttributesCR("intel-nic-attrs", "intel_nic")
			retained.UID = "retained-attributes"
			retained.Labels[consts.DRAResourcePoolLabel] = "wrong-pool"
			obsolete := buildDeviceAttributesCR("zzz-attrs", "zzz")
			obsolete.Labels[consts.DRAResourcePoolLabel] = "intel-nic"
			foreign := buildDeviceAttributesCR("user-attrs", "user")
			foreign.Labels = map[string]string{consts.DRAResourcePoolLabel: "intel-nic"}
			for _, attr := range []*sriovdrav1alpha1.DeviceAttributes{retained, obsolete, foreign} {
				Expect(r.Create(ctx, attr)).To(Succeed())
			}
			Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
			policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{{
				ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: testNamespace},
				Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
			}}}
			for range 2 {
				Expect(r.syncDeviceAttributes(ctx, dc, policies)).To(Succeed())
				got := &sriovdrav1alpha1.DeviceAttributes{}
				Expect(errors.IsNotFound(r.Get(ctx, k8sclient.ObjectKeyFromObject(obsolete), got))).To(BeTrue())
				Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(retained), got)).To(Succeed())
				Expect(got.UID).To(Equal(retained.UID), "the desired object must be repaired in place")
				Expect(got.Labels[consts.DRAResourcePoolLabel]).To(Equal("intel-nic"))
				Expect(got.Spec).To(Equal(retained.Spec))
				gotForeign := &sriovdrav1alpha1.DeviceAttributes{}
				Expect(r.Get(ctx, k8sclient.ObjectKeyFromObject(foreign), gotForeign)).To(Succeed())
				Expect(gotForeign).To(Equal(foreign))
			}
		})

		It("syncSriovResourcePolicies skips nodes missing kubernetes.io/hostname", func() {
			nodeName := "worker-unlabeled"
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"node-role.kubernetes.io/worker": "",
					},
				},
			}
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Vendor: "8086", Driver: "i40e", PciAddress: "0000:86:00.0"},
					},
				},
			}
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "intel_nic",
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
						},
					},
				},
			}
			Expect(renderNodePolicies(nodeState, pl, node, false)).To(Succeed())
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)
			beforeEachDRA(node, nodeState)
			nl := &corev1.NodeList{Items: []corev1.Node{*node}}
			Expect(r.syncSriovResourcePolicies(ctx, dc, pl, nl, true)).To(Succeed())
			policyList := &sriovdrav1alpha1.SriovResourcePolicyList{}
			Expect(r.List(ctx, policyList, k8sclient.InNamespace(testNamespace),
				k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(policyList.Items).To(BeEmpty())

			node.Labels[corev1.LabelHostname] = nodeName
			nl.Items[0] = *node
			Expect(r.syncSriovResourcePolicies(ctx, dc, pl, nl, true)).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: nodeName},
				&sriovdrav1alpha1.SriovResourcePolicy{})).To(Succeed())
		})

		It("syncSriovResourcePolicies creates SriovResourcePolicy per node", func() {
			nodeName := "worker-0"
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"node-role.kubernetes.io/worker": "",
						"kubernetes.io/hostname":         nodeName,
					},
				},
			}
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Vendor: "8086", Driver: "i40e", PciAddress: "0000:86:00.0"},
					},
				},
			}
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: testNamespace},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "intel_nic",
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
							NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
						},
					},
				},
			}
			Expect(renderNodePolicies(nodeState, pl, node, false)).To(Succeed())
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)
			beforeEachDRA(node, nodeState)
			nl := &corev1.NodeList{Items: []corev1.Node{*node}}
			Expect(r.syncSriovResourcePolicies(ctx, dc, pl, nl, true)).To(Succeed())
			policy := &sriovdrav1alpha1.SriovResourcePolicy{}
			Expect(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: nodeName}, policy)).To(Succeed())
			Expect(policy.Spec.NodeSelector).NotTo(BeNil())
			Expect(policy.Spec.NodeSelector.NodeSelectorTerms).To(HaveLen(1))
			reqs := policy.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions
			Expect(reqs).To(HaveLen(1))
			Expect(reqs[0].Key).To(Equal(corev1.LabelHostname))
			Expect(reqs[0].Operator).To(Equal(corev1.NodeSelectorOpIn))
			Expect(reqs[0].Values).To(Equal([]string{nodeName}))
			Expect(policy.Spec.Configs).To(HaveLen(1))
			Expect(policy.Spec.Configs[0].DeviceAttributesSelector).NotTo(BeNil())
			Expect(policy.Spec.Configs[0].DeviceAttributesSelector.MatchLabels).To(HaveKeyWithValue(consts.DRAResourcePoolLabel, "intel-nic"))
			Expect(policy.Spec.Configs[0].ResourceFilters).To(HaveLen(1))
			Expect(policy.Spec.Configs[0].ResourceFilters[0].Vendors).To(Equal([]string{"8086"}))
		})

		It("syncExtendedResourceDeviceClasses rejects colliding normalized names before writing", func() {
			beforeEachDRA()
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "INTEL_NIC"},
					},
				},
			}
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, pl)).To(MatchError(ContainSubstring("normalizes to the same DRA device class name")))
			dcList := &resourceapi.DeviceClassList{}
			Expect(r.List(ctx, dcList, k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(dcList.Items).To(BeEmpty())
		})

		It("syncExtendedResourceDeviceClasses creates DeviceClass per resource name", func() {
			beforeEachDRA()
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			Expect(r.syncDeviceAttributes(ctx, dc, pl)).To(Succeed())
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, pl)).To(Succeed())
			dcList := &resourceapi.DeviceClassList{}
			Expect(r.List(ctx, dcList, k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(dcList.Items).To(HaveLen(1))
			Expect(dcList.Items[0].Name).To(Equal("intel-nic"))
			Expect(dcList.Items[0].Spec.ExtendedResourceName).NotTo(BeNil())
			Expect(*dcList.Items[0].Spec.ExtendedResourceName).To(Equal("openshift.io/intel_nic"))
		})

		It("cleanupExtendedResourceDeviceClasses deletes operator-created DeviceClasses", func() {
			labels := maps.Clone(consts.DRAOperatorLabels)
			labels[consts.DRADeviceClassResourceNameLabel] = "intel_nic"
			deviceClass := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "intel-nic",
					Labels: labels,
				},
			}
			beforeEachDRA(deviceClass)
			Expect(cleanupExtendedResourceDeviceClasses(ctx, r.Client)).To(Succeed())
			dcList := &resourceapi.DeviceClassList{}
			Expect(r.List(ctx, dcList, k8sclient.MatchingLabels(consts.DRAOperatorLabels))).To(Succeed())
			Expect(dcList.Items).To(BeEmpty())
		})

		It("cleanupExtendedResourceDeviceClasses deletes extended DeviceClasses missing the resource-name label", func() {
			base := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   consts.DRADriverBaseDeviceClassName,
					Labels: maps.Clone(consts.DRAOperatorLabels),
				},
			}
			orphan := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "orphan-nic",
					Labels: maps.Clone(consts.DRAOperatorLabels),
				},
			}
			beforeEachDRA(base, orphan)
			Expect(cleanupExtendedResourceDeviceClasses(ctx, r.Client)).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Name: consts.DRADriverBaseDeviceClassName}, &resourceapi.DeviceClass{})).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Name: "orphan-nic"}, &resourceapi.DeviceClass{})).To(MatchError(ContainSubstring("not found")))
		})

		It("syncExtendedResourceDeviceClasses does not delete the base DeviceClass", func() {
			base := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   consts.DRADriverBaseDeviceClassName,
					Labels: maps.Clone(consts.DRAOperatorLabels),
				},
			}
			beforeEachDRA(base)
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, pl)).To(Succeed())
			got := &resourceapi.DeviceClass{}
			Expect(r.Get(ctx, types.NamespacedName{Name: consts.DRADriverBaseDeviceClassName}, got)).To(Succeed())
			Expect(got.Labels[consts.DRAGeneratedByLabel]).To(Equal(consts.SriovNetworkOperatorIdentifier))
			Expect(got.Labels).NotTo(HaveKey(consts.DRADeviceClassResourceNameLabel))
		})

		It("syncExtendedResourceDeviceClasses deletes obsolete DeviceClasses missing the resource-name label", func() {
			base := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   consts.DRADriverBaseDeviceClassName,
					Labels: maps.Clone(consts.DRAOperatorLabels),
				},
			}
			orphan := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "orphan-nic",
					Labels: maps.Clone(consts.DRAOperatorLabels),
				},
			}
			beforeEachDRA(base, orphan)
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, pl)).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Name: consts.DRADriverBaseDeviceClassName}, &resourceapi.DeviceClass{})).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Name: "intel-nic"}, &resourceapi.DeviceClass{})).To(Succeed())
			Expect(r.Get(ctx, types.NamespacedName{Name: "orphan-nic"}, &resourceapi.DeviceClass{})).To(MatchError(ContainSubstring("not found")))
		})

		It("cleanupExtendedResourceDeviceClasses leaves the base DeviceClass", func() {
			base := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:   consts.DRADriverBaseDeviceClassName,
					Labels: maps.Clone(consts.DRAOperatorLabels),
				},
			}
			beforeEachDRA(base)
			Expect(cleanupExtendedResourceDeviceClasses(ctx, r.Client)).To(Succeed())
			got := &resourceapi.DeviceClass{}
			Expect(r.Get(ctx, types.NamespacedName{Name: consts.DRADriverBaseDeviceClassName}, got)).To(Succeed())
		})

		It("syncExtendedResourceDeviceClasses skips gracefully when DeviceClass CRD is not available", func() {
			ctx = context.Background()
			nsSaved = vars.Namespace
			prefixSaved = vars.ResourcePrefix
			vars.Namespace = testNamespace
			vars.ResourcePrefix = "openshift.io"
			DeferCleanup(func() {
				vars.Namespace = nsSaved
				vars.ResourcePrefix = prefixSaved
			})
			noDeviceClassScheme := runtime.NewScheme()
			utilruntime.Must(sriovnetworkv1.AddToScheme(noDeviceClassScheme))
			utilruntime.Must(sriovdrav1alpha1.AddToScheme(noDeviceClassScheme))
			utilruntime.Must(corev1.AddToScheme(noDeviceClassScheme))
			dc = &sriovnetworkv1.SriovOperatorConfig{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: testNamespace, UID: "operator-owner"},
			}
			cl := fake.NewClientBuilder().WithScheme(noDeviceClassScheme).WithObjects(dc).Build()
			r = &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: noDeviceClassScheme}
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNamespace},
						Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
					},
				},
			}
			Expect(r.syncExtendedResourceDeviceClasses(ctx, dc, pl)).To(Succeed())
		})

		It("cleanupExtendedResourceDeviceClasses skips gracefully when DeviceClass CRD is not available", func() {
			ctx = context.Background()
			nsSaved = vars.Namespace
			prefixSaved = vars.ResourcePrefix
			vars.Namespace = testNamespace
			vars.ResourcePrefix = "openshift.io"
			DeferCleanup(func() {
				vars.Namespace = nsSaved
				vars.ResourcePrefix = prefixSaved
			})
			noDeviceClassScheme := runtime.NewScheme()
			utilruntime.Must(sriovnetworkv1.AddToScheme(noDeviceClassScheme))
			utilruntime.Must(sriovdrav1alpha1.AddToScheme(noDeviceClassScheme))
			utilruntime.Must(corev1.AddToScheme(noDeviceClassScheme))
			cl := fake.NewClientBuilder().WithScheme(noDeviceClassScheme).Build()
			r = &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: noDeviceClassScheme}
			Expect(cleanupExtendedResourceDeviceClasses(ctx, r.Client)).To(Succeed())
		})

		It("cleanupSriovResourcePoliciesAndDeviceAttributes deletes operator-created policies and attributes", func() {
			policy := &sriovdrav1alpha1.SriovResourcePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: testNamespace,
					Labels:    maps.Clone(consts.DRAOperatorLabels),
				},
			}
			attr := &sriovdrav1alpha1.DeviceAttributes{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "intel-nic-attrs",
					Namespace: testNamespace,
					Labels:    maps.Clone(consts.DRAOperatorLabels),
				},
			}
			beforeEachDRA(policy, attr)
			Expect(r.cleanupSriovResourcePoliciesAndDeviceAttributes(ctx, dc)).To(Succeed())
			var gotPolicy sriovdrav1alpha1.SriovResourcePolicy
			Expect(errors.IsNotFound(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "foo"}, &gotPolicy))).To(BeTrue())
			var gotAttr sriovdrav1alpha1.DeviceAttributes
			Expect(errors.IsNotFound(r.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "intel-nic-attrs"}, &gotAttr))).To(BeTrue())
		})
	})

})

var _ = It("cleans generated device classes while waiting for an active config", func() {
	for _, terminating := range []bool{false, true} {
		By(fmt.Sprintf("terminating_%t", terminating), func() {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, resourceapi.AddToScheme} {
				Expect(add(scheme)).To(Succeed())
			}
			config := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
				Name: consts.DefaultConfigName, Namespace: vars.Namespace,
				Finalizers:        []string{sriovnetworkv1.OPERATORCONFIGFINALIZERNAME},
				DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
			}, Spec: sriovnetworkv1.SriovOperatorConfigSpec{
				FeatureGates: map[string]bool{consts.DynamicResourceAllocationFeatureGate: true},
			}}
			generated := &resourceapi.DeviceClass{ObjectMeta: metav1.ObjectMeta{
				Name: "intel-nic", Labels: maps.Clone(consts.DRAOperatorLabels),
			}}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(generated)
			if terminating {
				builder.WithObjects(config)
			}
			cl := builder.Build()
			// No feature gate is needed: deletion must exit before normal reconciliation.
			r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
			for i := 0; i < 2; i++ {
				result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodePolicySyncEventName}})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).NotTo(BeZero(), "expected retry while waiting for an active config")
				{
					err := cl.Get(ctx, k8sclient.ObjectKeyFromObject(generated), &resourceapi.DeviceClass{})
					Expect(errors.IsNotFound(err)).To(BeTrue(), "generated DeviceClass survived cleanup: %v", err)
				}
				if i == 0 {
					// Simulate an in-flight reconciliation recreating a class after cleanup.
					generated.ResourceVersion = ""
					Expect(cl.Create(ctx, generated)).To(Succeed())
				}
			}
		})
	}
})

// Reconcile must use SriovOperatorConfig without changing the shared gate,
// which may reflect a newer config-controller reconciliation.
var _ = It("cleans DRA objects using the config without changing shared feature gates", func() {
	ctx := context.Background()
	nsSaved := vars.Namespace
	prefixSaved := vars.ResourcePrefix
	vars.Namespace = testNamespace
	vars.ResourcePrefix = "openshift.io"
	defer func() {
		vars.Namespace = nsSaved
		vars.ResourcePrefix = prefixSaved
	}()

	scheme := runtime.NewScheme()
	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	utilruntime.Must(sriovdrav1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(resourceapi.AddToScheme(scheme))

	dc := &sriovnetworkv1.SriovOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: testNamespace},
		Spec: sriovnetworkv1.SriovOperatorConfigSpec{
			FeatureGates: map[string]bool{consts.DynamicResourceAllocationFeatureGate: false},
		},
	}
	attr := &sriovdrav1alpha1.DeviceAttributes{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "intel-nic-attrs",
			Namespace: testNamespace,
			Labels:    maps.Clone(consts.DRAOperatorLabels),
		},
	}
	policy := &sriovdrav1alpha1.SriovResourcePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-0",
			Namespace: testNamespace,
			Labels:    maps.Clone(consts.DRAOperatorLabels),
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dc, attr, policy).Build()
	fg := featuregate.New()
	fg.Set(map[string]bool{consts.DynamicResourceAllocationFeatureGate: true})
	previousGate := vars.FeatureGate
	DeferCleanup(func() { vars.FeatureGate = previousGate })
	vars.FeatureGate = fg
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}

	Expect(vars.FeatureGate.IsEnabled(consts.DynamicResourceAllocationFeatureGate)).To(BeTrue(), "precondition: FeatureGate should still report DRA enabled")
	{
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodePolicySyncEventName}})
		Expect(err).NotTo(HaveOccurred(), "Reconcile: %v", err)
	}
	Expect(fg.IsEnabled(consts.DynamicResourceAllocationFeatureGate)).To(BeTrue(), "policy reconcile must not overwrite the shared FeatureGate")
	{
		err := cl.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.ConfigMapName}, &corev1.ConfigMap{})
		Expect(err).NotTo(HaveOccurred(), "expected device plugin ConfigMap recreated when DRA is disabled: %v", err)
	}
	gotAttr := &sriovdrav1alpha1.DeviceAttributes{}
	{
		err := cl.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "intel-nic-attrs"}, gotAttr)
		Expect(errors.IsNotFound(err)).To(BeTrue(), "expected DeviceAttributes deleted, get err=%v", err)
	}
	gotPolicy := &sriovdrav1alpha1.SriovResourcePolicy{}
	{
		err := cl.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "worker-0"}, gotPolicy)
		Expect(errors.IsNotFound(err)).To(BeTrue(), "expected SriovResourcePolicy deleted, get err=%v", err)
	}
})

var _ = It("uses one config snapshot throughout policy reconciliation", func() {
	oldNamespace, oldPrefix := vars.Namespace, vars.ResourcePrefix
	vars.Namespace, vars.ResourcePrefix = testNamespace, "openshift.io"
	DeferCleanup(func() { vars.Namespace, vars.ResourcePrefix = oldNamespace, oldPrefix })
	previousGate := vars.FeatureGate
	DeferCleanup(func() { vars.FeatureGate = previousGate })

	for _, draEnabled := range []bool{false, true} {
		By(fmt.Sprintf("draEnabled=%t", draEnabled), func() {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, sriovdrav1alpha1.AddToScheme, corev1.AddToScheme, resourceapi.AddToScheme} {
				utilruntime.Must(add(scheme))
			}
			config := &sriovnetworkv1.SriovOperatorConfig{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner"},
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{
					FeatureGates: map[string]bool{
						consts.DynamicResourceAllocationFeatureGate: draEnabled,
						consts.ManageSoftwareBridgesFeatureGate:     draEnabled,
					},
				},
			}
			policy := &sriovnetworkv1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: vars.Namespace},
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "intel_nic", DeviceType: "netdevice", NumVfs: 2,
					NicSelector: sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1"}},
					EswitchMode: sriovnetworkv1.ESwithModeSwitchDev,
					Bridge:      sriovnetworkv1.Bridge{OVS: &sriovnetworkv1.OVSConfig{}},
				},
			}
			node := testManagedNode("worker")
			node.Labels[corev1.LabelHostname] = node.Name
			state := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{Interfaces: sriovnetworkv1.InterfaceExts{{
					Name: "ens1", PciAddress: "0000:01:00.0", TotalVfs: 8,
				}}},
			}
			fg := featuregate.New()
			vars.FeatureGate = fg
			gateChanged := false
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(config, policy, node, state).WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c k8sclient.WithWatch, list k8sclient.ObjectList, opts ...k8sclient.ListOption) error {
					if err := c.List(ctx, list, opts...); err != nil {
						return err
					}
					if _, ok := list.(*sriovnetworkv1.SriovNetworkNodePolicyList); ok {
						// Simulate another controller updating the shared gate after
						// this reconcile has read its config snapshot and policies.
						fg.Set(map[string]bool{
							consts.DynamicResourceAllocationFeatureGate: !draEnabled,
							consts.ManageSoftwareBridgesFeatureGate:     !draEnabled,
						})
						gateChanged = true
					}
					return nil
				},
			}).Build()
			r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
			{
				_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodePolicySyncEventName}})
				Expect(err).NotTo(HaveOccurred(), "Reconcile: %v", err)
			}
			Expect(gateChanged).To(BeTrue(), "shared gate was not changed during reconcile")
			Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(state), state)).To(Succeed())
			{
				gotBridges := len(state.Spec.Bridges.OVS) > 0
				Expect(gotBridges).To(Equal(draEnabled), "bridges rendered = %t, want %t from config snapshot", gotBridges, draEnabled)
			}
			attrs := &sriovdrav1alpha1.DeviceAttributesList{}
			Expect(cl.List(ctx, attrs)).To(Succeed())
			{
				gotDRA := len(attrs.Items) > 0
				Expect(gotDRA).To(Equal(draEnabled), "DRA resources present = %t, want %t", gotDRA, draEnabled)
			}
			err := cl.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: consts.ConfigMapName}, &corev1.ConfigMap{})
			Expect(draEnabled && !errors.IsNotFound(err)).To(BeFalse(), "device-plugin ConfigMap lookup = %v, draEnabled = %t", err, draEnabled)
			Expect(!draEnabled && err != nil).To(BeFalse(), "device-plugin ConfigMap lookup = %v, draEnabled = %t", err, draEnabled)
		})
	}
})

var _ = It("reconciles policies from the feature gate without waiting for provider deployment", func() {
	oldNamespace, oldPrefix := vars.Namespace, vars.ResourcePrefix
	vars.Namespace, vars.ResourcePrefix = testNamespace, "openshift.io"
	DeferCleanup(func() { vars.Namespace, vars.ResourcePrefix = oldNamespace, oldPrefix })

	ctx := context.Background()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, sriovdrav1alpha1.AddToScheme, corev1.AddToScheme, resourceapi.AddToScheme} {
		utilruntime.Must(add(scheme))
	}
	config := &sriovnetworkv1.SriovOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner"},
		Spec: sriovnetworkv1.SriovOperatorConfigSpec{FeatureGates: map[string]bool{
			consts.DynamicResourceAllocationFeatureGate: true,
		}},
	}
	policy := &sriovnetworkv1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: vars.Namespace},
		Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			ResourceName: "intel_nic", NumVfs: 2,
			NicSelector: sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1"}},
		},
	}
	node := testManagedNode("worker")
	node.Labels[corev1.LabelHostname] = node.Name
	state := &sriovnetworkv1.SriovNetworkNodeState{
		ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace},
		Status: sriovnetworkv1.SriovNetworkNodeStateStatus{Interfaces: sriovnetworkv1.InterfaceExts{{
			Name: "ens1", PciAddress: "0000:01:00.0", TotalVfs: 8,
		}}},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(config, policy, node, state).Build()
	r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}

	{
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodePolicySyncEventName}})
		Expect(err).NotTo(HaveOccurred(), "Reconcile: %v", err)
	}

	gotState := &sriovnetworkv1.SriovNetworkNodeState{}
	Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(state), gotState)).To(Succeed())
	Expect(gotState.Spec.Interfaces).To(HaveLen(1), "NodeState was not reconciled by the configured provider: %+v", gotState.Spec.Interfaces)
	Expect(gotState.Spec.Interfaces[0].NumVfs).To(BeNumerically("==", 2), "NodeState was not reconciled by the configured provider: %+v", gotState.Spec.Interfaces)
	{
		err := cl.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: consts.ConfigMapName}, &corev1.ConfigMap{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "DRA must not create a device-plugin ConfigMap: %v", err)
	}
	attrs := &sriovdrav1alpha1.DeviceAttributesList{}
	Expect(cl.List(ctx, attrs)).To(Succeed())
	Expect(attrs.Items).To(HaveLen(1), "DRA resources should be generated without a provider DaemonSet: %+v", attrs.Items)
	policies := &sriovdrav1alpha1.SriovResourcePolicyList{}
	Expect(cl.List(ctx, policies)).To(Succeed())
	Expect(policies.Items).To(BeEmpty(), "VF publication still waits for the node configuration to be applied")
})

var _ = It("repairs managed DRA ownership and preserves foreign objects", func() {
	ctx := context.Background()
	oldNamespace := vars.Namespace
	vars.Namespace = "dra-ownership-test"
	DeferCleanup(func() { vars.Namespace = oldNamespace })
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, sriovdrav1alpha1.AddToScheme, corev1.AddToScheme} {
		Expect(add(scheme)).To(Succeed())
	}
	owner := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
		Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner",
	}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Labels: map[string]string{corev1.LabelHostname: "worker-1"}}}
	state := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace}}
	state.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)
	policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{{
		ObjectMeta: metav1.ObjectMeta{Name: "p1"},
		Spec:       sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic", NicSelector: sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"}},
	}}}
	userValue := "user-value"
	for _, original := range []k8sclient.Object{
		&sriovdrav1alpha1.DeviceAttributes{
			ObjectMeta: metav1.ObjectMeta{Name: "intel-nic-attrs", Namespace: vars.Namespace},
			Spec: sriovdrav1alpha1.DeviceAttributesSpec{Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"example.com/custom": {StringValue: &userValue},
			}},
		},
		&sriovdrav1alpha1.SriovResourcePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace},
			Spec: sriovdrav1alpha1.SriovResourcePolicySpec{Configs: []sriovdrav1alpha1.Config{{
				ResourceFilters: []sriovdrav1alpha1.ResourceFilter{{Vendors: []string{"15b3"}}},
			}}},
		},
	} {
		for _, marker := range []string{"none", "controller", "label", "foreign-controller"} {
			By(original.GetName()+"/"+marker, func() {
				existing := original.DeepCopyObject().(k8sclient.Object)
				switch marker {
				case "controller", "foreign-controller":
					controllerOwner := owner.DeepCopy()
					if marker == "foreign-controller" {
						controllerOwner.Name, controllerOwner.UID = "other", "other-owner"
					}
					Expect(controllerutil.SetControllerReference(controllerOwner, existing, scheme)).To(Succeed())
				case "label":
					existing.SetLabels(maps.Clone(consts.DRAOperatorLabels))
				}
				cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(owner, node, state, existing).Build()
				r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
				before := existing.DeepCopyObject().(k8sclient.Object)
				Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(existing), before)).To(Succeed())
				var err error
				switch existing.(type) {
				case *sriovdrav1alpha1.DeviceAttributes:
					err = r.syncDeviceAttributes(ctx, owner, policies)
				case *sriovdrav1alpha1.SriovResourcePolicy:
					obsolete := &sriovdrav1alpha1.SriovResourcePolicy{ObjectMeta: metav1.ObjectMeta{
						Name: "old-worker", Namespace: vars.Namespace, Labels: maps.Clone(consts.DRAOperatorLabels),
					}}
					Expect(cl.Create(ctx, obsolete)).To(Succeed())
					err = r.syncSriovResourcePolicies(ctx, owner, policies, &corev1.NodeList{Items: []corev1.Node{*node}}, true)
					{
						getErr := cl.Get(ctx, k8sclient.ObjectKeyFromObject(obsolete), obsolete)
						Expect(errors.IsNotFound(getErr)).To(BeTrue(), "obsolete managed policy must be pruned even on ownership conflict, get error: %v", getErr)
					}
				}
				got := existing.DeepCopyObject().(k8sclient.Object)
				Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(existing), got)).To(Succeed())
				if marker == "none" || marker == "foreign-controller" {
					Expect(errors.IsConflict(err)).To(BeTrue(), "expected ownership conflict, got %v", err)
					{
						diff := cmp.Diff(before, got)
						Expect(diff).To(Equal(""), "user-owned object changed (-before +after):\n%s", diff)
					}
					return
				}
				Expect(err).NotTo(HaveOccurred())
				Expect(metav1.IsControlledBy(got, owner)).To(BeTrue(), "operator ownership was not restored: %+v", got)
				Expect(got.GetLabels()[consts.DRAGeneratedByLabel]).To(Equal(consts.SriovNetworkOperatorIdentifier), "operator ownership was not restored: %+v", got)
			})
		}
	}
})

var _ = It("rejects unsafe policy configuration before any writes", func() {
	for _, tc := range []struct {
		name, resource, deviceType, wantError string
		netFilter                             string
		pfNames                               []string
	}{
		{name: "normalized collision", resource: "INTEL_NIC", wantError: "same DRA device class"},
		{name: "mixed device types", resource: "intel_nic", deviceType: "vfio-pci", wantError: "incompatible deviceType"},
		{name: "empty resource", resource: "", wantError: "must not be empty"},
		{name: "VF range", resource: "other", pfNames: []string{"ens1f0#2-5"}, wantError: "VF range"},
		{name: "netFilter", resource: "other", netFilter: "aws/NetworkID:net-1", wantError: "netFilter"},
	} {
		By(tc.name, func() {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, corev1.AddToScheme, resourceapi.AddToScheme, sriovdrav1alpha1.AddToScheme} {
				Expect(add(scheme)).To(Succeed())
			}
			config := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: vars.Namespace}, Spec: sriovnetworkv1.SriovOperatorConfigSpec{FeatureGates: map[string]bool{consts.DynamicResourceAllocationFeatureGate: true}}}
			first := &sriovnetworkv1.SriovNetworkNodePolicy{ObjectMeta: metav1.ObjectMeta{Name: "first", Namespace: vars.Namespace}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "intel_nic", DeviceType: "netdevice"}}
			second := &sriovnetworkv1.SriovNetworkNodePolicy{ObjectMeta: metav1.ObjectMeta{Name: "second", Namespace: vars.Namespace}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: tc.resource, DeviceType: tc.deviceType}}
			second.Spec.NicSelector.PfNames = tc.pfNames
			second.Spec.NicSelector.NetFilter = tc.netFilter
			writes := 0
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(config, first, second).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.CreateOption) error {
					writes++
					return fmt.Errorf("unexpected create")
				},
				Update: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.UpdateOption) error {
					writes++
					return fmt.Errorf("unexpected update")
				},
				Patch: func(context.Context, k8sclient.WithWatch, k8sclient.Object, k8sclient.Patch, ...k8sclient.PatchOption) error {
					writes++
					return fmt.Errorf("unexpected patch")
				},
				SubResourcePatch: func(context.Context, k8sclient.Client, string, k8sclient.Object, k8sclient.Patch, ...k8sclient.SubResourcePatchOption) error {
					writes++
					return fmt.Errorf("unexpected subresource patch")
				},
				SubResourceUpdate: func(context.Context, k8sclient.Client, string, k8sclient.Object, ...k8sclient.SubResourceUpdateOption) error {
					writes++
					return fmt.Errorf("unexpected subresource update")
				},
				Delete: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.DeleteOption) error {
					writes++
					return fmt.Errorf("unexpected delete")
				},
				DeleteAllOf: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.DeleteAllOfOption) error {
					writes++
					return fmt.Errorf("unexpected delete all")
				},
			}).Build()
			r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
			_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: nodePolicySyncEventName}})
			Expect(err).To(HaveOccurred(), "Reconcile error = %v, want %q", err, tc.wantError)
			Expect(err.Error()).To(ContainSubstring(tc.wantError), "Reconcile error = %v, want %q", err, tc.wantError)
			Expect(writes).To(BeZero(), "rejected configuration attempted %d writes", writes)
		})
	}
})

var _ = It("protects concurrently changed DRA objects with deletion preconditions", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, sriovdrav1alpha1.AddToScheme, resourceapi.AddToScheme} {
		utilruntime.Must(add(scheme))
	}
	owner := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
		Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner",
	}}
	for _, mode := range []string{"prune", "disable"} {
		for _, template := range []k8sclient.Object{&sriovdrav1alpha1.DeviceAttributes{}, &sriovdrav1alpha1.SriovResourcePolicy{}, &resourceapi.DeviceClass{}} {
			for _, change := range []string{"unchanged", "replacement", "ownership removed", "managed update", "already deleted"} {
				By(fmt.Sprintf("%s/%T/%s", mode, template, change), func() {
					ctx := context.Background()
					original := template.DeepCopyObject().(k8sclient.Object)
					original.SetName("obsolete")
					original.SetUID("original")
					original.SetLabels(maps.Clone(consts.DRAOperatorLabels))
					if _, clusterScoped := original.(*resourceapi.DeviceClass); !clusterScoped {
						original.SetNamespace(vars.Namespace)
					}
					deleteCalls := 0
					cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(owner, original).WithInterceptorFuncs(interceptor.Funcs{
						Delete: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.DeleteOption) error {
							deleteCalls++
							options := (&k8sclient.DeleteOptions{}).ApplyOptions(opts)
							Expect(options.Preconditions).NotTo(BeNil())
							Expect(options.Preconditions.UID).To(HaveValue(Equal(obj.GetUID())))
							Expect(options.Preconditions.ResourceVersion).To(HaveValue(Equal(obj.GetResourceVersion())))
							if deleteCalls == 1 {
								live := original.DeepCopyObject().(k8sclient.Object)
								Expect(c.Get(ctx, k8sclient.ObjectKeyFromObject(original), live)).To(Succeed())
								switch change {
								case "replacement", "already deleted":
									Expect(c.Delete(ctx, live)).To(Succeed())
									if change == "replacement" {
										live.SetUID("replacement")
										live.SetResourceVersion("")
										live.SetLabels(nil)
										Expect(c.Create(ctx, live)).To(Succeed())
									}
								case "ownership removed":
									live.SetLabels(nil)
									Expect(c.Update(ctx, live)).To(Succeed())
								case "managed update":
									live.SetAnnotations(map[string]string{"updated": "true"})
									Expect(c.Update(ctx, live)).To(Succeed())
								}
							}
							return c.Delete(ctx, obj, opts...)
						},
					}).Build()
					r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
					sync := func() error {
						policies := &sriovnetworkv1.SriovNetworkNodePolicyList{}
						if mode == "prune" {
							switch original.(type) {
							case *sriovdrav1alpha1.DeviceAttributes:
								return r.syncDeviceAttributes(ctx, owner, policies)
							case *sriovdrav1alpha1.SriovResourcePolicy:
								return r.syncSriovResourcePolicies(ctx, owner, policies, &corev1.NodeList{}, true)
							default:
								return r.syncExtendedResourceDeviceClasses(ctx, owner, policies)
							}
						}
						if _, clusterScoped := original.(*resourceapi.DeviceClass); clusterScoped {
							return cleanupExtendedResourceDeviceClasses(ctx, cl)
						}
						return r.cleanupSriovResourcePoliciesAndDeviceAttributes(ctx, owner)
					}
					err := sync()
					if change == "unchanged" || change == "already deleted" {
						Expect(err).NotTo(HaveOccurred())
					} else {
						Expect(errors.IsConflict(err)).To(BeTrue(), "a changed object must reject the stale delete: %v", err)
					}
					preserve := change == "replacement" || change == "ownership removed"
					before := original.DeepCopyObject().(k8sclient.Object)
					if preserve || change == "managed update" {
						Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(original), before)).To(Succeed())
					}
					// The next reconciliation must re-list and check ownership again.
					Expect(sync()).To(Succeed())
					after := original.DeepCopyObject().(k8sclient.Object)
					getErr := cl.Get(ctx, k8sclient.ObjectKeyFromObject(original), after)
					if preserve {
						Expect(getErr).NotTo(HaveOccurred())
						Expect(after).To(Equal(before))
					} else {
						Expect(errors.IsNotFound(getErr)).To(BeTrue())
					}
					expectedDeletes := 1
					if change == "managed update" {
						expectedDeletes = 2
					}
					Expect(deleteCalls).To(Equal(expectedDeletes))
				})
			}
		}
	}
})

var _ = It("cleans only DRA objects with repairable ownership markers", func() {
	oldNamespace := vars.Namespace
	vars.Namespace = testNamespace
	DeferCleanup(func() { vars.Namespace = oldNamespace })
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, sriovdrav1alpha1.AddToScheme, resourceapi.AddToScheme} {
		utilruntime.Must(add(scheme))
	}
	owner := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
		Name: consts.DefaultConfigName, Namespace: vars.Namespace, UID: "operator-owner",
	}}

	for _, mode := range []string{"prune", "disable"} {
		By(mode, func() {
			ctx := context.Background()
			var objects []k8sclient.Object
			var removed, preserved []k8sclient.Object
			for _, template := range []k8sclient.Object{&sriovdrav1alpha1.DeviceAttributes{}, &sriovdrav1alpha1.SriovResourcePolicy{}} {
				for _, marker := range []string{"controller", "label", "unmarked", "foreign-controller", "foreign-controller-label", "other-namespace"} {
					obj := template.DeepCopyObject().(k8sclient.Object)
					obj.SetName(marker)
					obj.SetNamespace(vars.Namespace)
					switch marker {
					case "controller", "other-namespace":
						Expect(controllerutil.SetControllerReference(owner, obj, scheme)).To(Succeed())
						if marker == "other-namespace" {
							obj.SetNamespace("other-namespace")
						}
					case "label":
						obj.SetLabels(maps.Clone(consts.DRAOperatorLabels))
					case "foreign-controller", "foreign-controller-label":
						foreign := owner.DeepCopy()
						foreign.UID = "foreign-owner"
						Expect(controllerutil.SetControllerReference(foreign, obj, scheme)).To(Succeed())
						if marker == "foreign-controller-label" {
							obj.SetLabels(maps.Clone(consts.DRAOperatorLabels))
						}
					}
					objects = append(objects, obj)
					if marker == "controller" || marker == "label" {
						removed = append(removed, obj)
					} else {
						preserved = append(preserved, obj)
					}
				}
			}
			for _, tc := range []struct {
				name   string
				labels map[string]string
				remove bool
			}{
				{name: "intel-nic", labels: map[string]string{consts.DRADeviceClassResourceNameLabel: "intel_nic"}, remove: true},
				{name: "generated", labels: maps.Clone(consts.DRAOperatorLabels), remove: true},
				{name: "user-class"},
				{name: "mismatched-name", labels: map[string]string{consts.DRADeviceClassResourceNameLabel: "intel_nic"}},
				{name: "empty-marker", labels: map[string]string{consts.DRADeviceClassResourceNameLabel: ""}},
				{name: consts.DRADriverBaseDeviceClassName, labels: maps.Clone(consts.DRAOperatorLabels)},
			} {
				obj := &resourceapi.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: tc.name, Labels: tc.labels}}
				objects = append(objects, obj)
				if tc.remove {
					removed = append(removed, obj)
				} else {
					preserved = append(preserved, obj)
				}
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			r := &SriovNetworkNodePolicyReconciler{Client: cl, Scheme: scheme}
			// Capture complete stored objects so preservation includes specs and metadata.
			for _, obj := range preserved {
				Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(obj), obj)).To(Succeed())
			}
			for range 2 {
				if mode == "prune" {
					policies := &sriovnetworkv1.SriovNetworkNodePolicyList{}
					Expect(r.syncDeviceAttributes(ctx, owner, policies)).To(Succeed())
					Expect(r.syncSriovResourcePolicies(ctx, owner, policies, &corev1.NodeList{}, true)).To(Succeed())
					Expect(r.syncExtendedResourceDeviceClasses(ctx, owner, policies)).To(Succeed())
				} else {
					Expect(r.cleanupSriovResourcePoliciesAndDeviceAttributes(ctx, owner)).To(Succeed())
					Expect(cleanupExtendedResourceDeviceClasses(ctx, cl)).To(Succeed())
				}
			}
			for _, obj := range removed {
				{
					err := cl.Get(ctx, k8sclient.ObjectKeyFromObject(obj), obj)
					Expect(errors.IsNotFound(err)).To(BeTrue(), "obsolete %T %s survived cleanup: %v", obj, obj.GetName(), err)
				}
			}
			for _, before := range preserved {
				got := before.DeepCopyObject().(k8sclient.Object)
				Expect(cl.Get(ctx, k8sclient.ObjectKeyFromObject(before), got)).To(Succeed(), "preserved %T %s", before, before.GetName())
				{
					diff := cmp.Diff(before, got)
					Expect(diff).To(Equal(""), "preserved object changed (-before +after):\n%s", diff)
				}
			}
		})
	}
})
