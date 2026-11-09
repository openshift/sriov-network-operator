package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/status"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// testManagedNode builds a node matching the default managed-node selector.
func testManagedNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
		"node-role.kubernetes.io/worker": "", "kubernetes.io/os": "linux",
	}}}
}

// testOperatorConfig builds the default operator configuration for the test namespace.
func testOperatorConfig() *sriovnetworkv1.SriovOperatorConfig {
	return &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
		Name: constants.DefaultConfigName, Namespace: vars.Namespace,
	}}
}

// createAggregateObjects creates test objects and registers their cleanup.
func createAggregateObjects(objects ...client.Object) {
	for _, obj := range objects {
		ExpectWithOffset(1, k8sClient.Create(context.Background(), obj)).To(Succeed())
		DeferCleanup(func() {
			err := k8sClient.Delete(context.Background(), obj)
			if !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

// createDrainOnlyPool verifies that creating a typed pool does not enable host configuration.
func createDrainOnlyPool(pool *sriovnetworkv1.SriovNetworkPoolConfig) {
	createAggregateObjects(pool)
	ExpectWithOffset(1, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
	ExpectWithOffset(1, pool.Spec.OvsHardwareOffloadConfig).To(BeNil())
}

// aggregateNodeState creates a ready NodeState with a discovered test interface.
func aggregateNodeState(node *corev1.Node) *sriovnetworkv1.SriovNetworkNodeState {
	state := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace}}
	createAggregateObjects(state)
	state.Status.Interfaces = sriovnetworkv1.InterfaceExts{{Name: "ens1", PciAddress: "0000:01:00.0"}}
	state.SetNodeStateConfigurationConditions(constants.SyncStatusSucceeded, "", false)
	ExpectWithOffset(1, k8sClient.Status().Update(context.Background(), state)).To(Succeed())
	return state
}

// policyStatus computes policy readiness from the current test cluster objects.
func policyStatus(ctx context.Context, c client.Client, policy *sriovnetworkv1.SriovNetworkNodePolicy) aggregateResult {
	nodes, nodesErr := managedNodes(ctx, c)
	states, statesErr := nodeStatesByName(ctx, c)
	return policyStatusFrom(policy, nodes, nodesErr, states, statesErr)
}

// regularPoolStatus computes pool readiness from the current test cluster objects.
func regularPoolStatus(ctx context.Context, c client.Client, pool *sriovnetworkv1.SriovNetworkPoolConfig) aggregateResult {
	nodes, nodesErr := managedNodes(ctx, c)
	states, statesErr := nodeStatesByName(ctx, c)
	pools := &sriovnetworkv1.SriovNetworkPoolConfigList{}
	if err := c.List(ctx, pools); err != nil {
		return aggregateUnknown(pool.Generation, err.Error())
	}
	return regularPoolStatusFrom(pool, nodesErr, states, statesErr, selectPoolNodes(pools.Items, nodes))
}

// expectAggregate checks counts, generation, reason, and Ready status.
func expectAggregate(result aggregateResult, generation int64, matched, ready int32, reason string) {
	ExpectWithOffset(1, result.matched).To(Equal(&matched))
	ExpectWithOffset(1, result.ready).To(Equal(&ready))
	ExpectWithOffset(1, result.condition.Reason).To(Equal(reason))
	ExpectWithOffset(1, result.condition.ObservedGeneration).To(Equal(generation))
	conditionStatus := metav1.ConditionFalse
	if reason == sriovnetworkv1.ReasonPolicyReady || reason == sriovnetworkv1.ReasonPoolConfigReady {
		conditionStatus = metav1.ConditionTrue
	}
	ExpectWithOffset(1, result.condition.Status).To(Equal(conditionStatus))
}

var _ = Describe("Aggregate status", func() {
	BeforeEach(func() {
		previousNamespace := vars.Namespace
		previousClusterType := vars.ClusterType
		vars.ClusterType = constants.ClusterTypeKubernetes
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aggregate-status-"}}
		createAggregateObjects(ns)
		vars.Namespace = ns.Name
		DeferCleanup(func() { vars.Namespace = previousNamespace; vars.ClusterType = previousClusterType })
	})

	It("clears counts when observation fails and reports zero targets when observation succeeds", func(ctx context.Context) {
		policy := &sriovnetworkv1.SriovNetworkNodePolicy{ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: vars.Namespace}, Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{NodeSelector: map[string]string{}}}
		createAggregateObjects(policy)
		unknown := policyStatus(ctx, k8sClient, policy)
		Expect(unknown.condition.Status).To(Equal(metav1.ConditionUnknown))
		Expect(unknown.matched).To(BeNil())
		Expect(unknown.ready).To(BeNil())
		createAggregateObjects(testOperatorConfig())
		expectAggregate(policyStatus(ctx, k8sClient, policy), policy.Generation, 0, 0, sriovnetworkv1.ReasonNoMatchingNodes)
	})

	It("requires current policy identity, revision, and NodeState readiness", func(ctx context.Context) {
		policy := &sriovnetworkv1.SriovNetworkNodePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: vars.Namespace},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "resource", NumVfs: 2, NodeSelector: map[string]string{},
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1"}}},
		}
		node := testManagedNode("worker1")
		createAggregateObjects(policy, node, testOperatorConfig())
		state := aggregateNodeState(node)
		expectAggregate(policyStatus(ctx, k8sClient, policy), policy.Generation, 1, 0, sriovnetworkv1.ReasonApplyingConfiguration)
		Expect(renderNodePolicies(state, &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{*policy}}, node, nil)).To(Succeed())
		Expect(k8sClient.Update(ctx, state)).To(Succeed())
		state.SetNodeStateConfigurationConditions(constants.SyncStatusSucceeded, "", false)
		Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
		expectAggregate(policyStatus(ctx, k8sClient, policy), policy.Generation, 1, 1, sriovnetworkv1.ReasonPolicyReady)

		policy.Spec.NumVfs++
		Expect(k8sClient.Update(ctx, policy)).To(Succeed())
		expectAggregate(policyStatus(ctx, k8sClient, policy), policy.Generation, 1, 0, sriovnetworkv1.ReasonApplyingConfiguration)
		Expect(renderNodePolicies(state, &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{*policy}}, node, nil)).To(Succeed())
		Expect(k8sClient.Update(ctx, state)).To(Succeed())
		expectAggregate(policyStatus(ctx, k8sClient, policy), policy.Generation, 1, 0, sriovnetworkv1.ReasonApplyingConfiguration)
		state.SetNodeStateConfigurationConditions(constants.SyncStatusSucceeded, "", false)
		Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
		expectAggregate(policyStatus(ctx, k8sClient, policy), policy.Generation, 1, 1, sriovnetworkv1.ReasonPolicyReady)
		Expect(k8sClient.Delete(ctx, policy)).To(Succeed())
		replacement := &sriovnetworkv1.SriovNetworkNodePolicy{ObjectMeta: metav1.ObjectMeta{Name: policy.Name, Namespace: vars.Namespace}, Spec: policy.Spec}
		createAggregateObjects(replacement)
		expectAggregate(policyStatus(ctx, k8sClient, replacement), replacement.Generation, 1, 0, sriovnetworkv1.ReasonApplyingConfiguration)
	})

	It("recounts membership and configuration progress as nodes move, appear, and disappear", func(ctx context.Context) {
		first := &sriovnetworkv1.SriovNetworkNodePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "first", Namespace: vars.Namespace},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "first", NumVfs: 2,
				NodeSelector: map[string]string{"policy-group": "a"},
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1"}}},
		}
		second := first.DeepCopy()
		second.Name, second.Spec.ResourceName = "second", "second"
		second.Spec.NodeSelector["policy-group"] = "b"
		createAggregateObjects(first, second, testOperatorConfig())
		policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{*first, *second}}
		nodes := []*corev1.Node{testManagedNode("worker1"), testManagedNode("worker2"), testManagedNode("worker3")}
		states := make([]*sriovnetworkv1.SriovNetworkNodeState, len(nodes))
		for i, node := range nodes {
			node.Labels["policy-group"] = "a"
			if i == 2 {
				node.Labels["policy-group"] = "b"
			}
			createAggregateObjects(node)
			states[i] = aggregateNodeState(node)
			Expect(renderNodePolicies(states[i], policies, node, nil)).To(Succeed())
			Expect(k8sClient.Update(ctx, states[i])).To(Succeed())
			states[i].SetNodeStateConfigurationConditions(constants.SyncStatusSucceeded, "", false)
			Expect(k8sClient.Status().Update(ctx, states[i])).To(Succeed())
		}
		unmanaged := testManagedNode("unmanaged")
		unmanaged.Labels["kubernetes.io/os"], unmanaged.Labels["policy-group"] = "windows", "b"
		createAggregateObjects(unmanaged)
		assertPolicy := func(p *sriovnetworkv1.SriovNetworkNodePolicy, matched, ready int32, reason string) {
			expectAggregate(policyStatus(ctx, k8sClient, p), p.Generation, matched, ready, reason)
		}
		assertPolicy(first, 2, 2, sriovnetworkv1.ReasonPolicyReady)
		assertPolicy(second, 1, 1, sriovnetworkv1.ReasonPolicyReady)
		nodes[1].Labels["policy-group"] = "b"
		Expect(k8sClient.Update(ctx, nodes[1])).To(Succeed())
		assertPolicy(first, 1, 1, sriovnetworkv1.ReasonPolicyReady)
		assertPolicy(second, 2, 1, sriovnetworkv1.ReasonApplyingConfiguration)
		state := states[1]
		state.Spec = sriovnetworkv1.SriovNetworkNodeStateSpec{}
		Expect(renderNodePolicies(state, policies, nodes[1], nil)).To(Succeed())
		Expect(k8sClient.Update(ctx, state)).To(Succeed())
		state.SetNodeStateConfigurationConditions(constants.SyncStatusInProgress, "", false)
		Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
		assertPolicy(second, 2, 1, sriovnetworkv1.ReasonApplyingConfiguration)
		for _, step := range []struct {
			name, syncStatus, reason string
			waiting                  bool
			ready                    int32
		}{
			{"blocked by PDB", constants.SyncStatusInProgress, sriovnetworkv1.ReasonWaitingForDrain, true, 1},
			{"node sync failed", constants.SyncStatusFailed, sriovnetworkv1.ReasonNodeSyncFailed, false, 1},
			{"configuration applied", constants.SyncStatusSucceeded, sriovnetworkv1.ReasonPolicyReady, false, 2},
		} {
			By(step.name)
			state.Status.SyncStatus = step.syncStatus
			state.SetNodeStateConfigurationConditions(step.syncStatus, "", step.waiting)
			Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
			assertPolicy(first, 1, 1, sriovnetworkv1.ReasonPolicyReady)
			assertPolicy(second, 2, step.ready, step.reason)
		}
		Expect(k8sClient.Delete(ctx, nodes[0])).To(Succeed())
		assertPolicy(first, 0, 0, sriovnetworkv1.ReasonNoMatchingNodes)
		assertPolicy(second, 2, 2, sriovnetworkv1.ReasonPolicyReady)
		added := testManagedNode("worker4")
		added.Labels["policy-group"] = "b"
		createAggregateObjects(added)
		assertPolicy(second, 3, 2, sriovnetworkv1.ReasonApplyingConfiguration)
		Expect(k8sClient.Delete(ctx, added)).To(Succeed())
		assertPolicy(second, 2, 2, sriovnetworkv1.ReasonPolicyReady)
		Expect(k8sClient.Delete(ctx, state)).To(Succeed())
		assertPolicy(second, 2, 1, sriovnetworkv1.ReasonApplyingConfiguration)
	})

	It("reports a missing NIC on a selected node", func(ctx context.Context) {
		policy := &sriovnetworkv1.SriovNetworkNodePolicy{ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: vars.Namespace},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{ResourceName: "resource", NumVfs: 2, NodeSelector: map[string]string{},
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1"}}}}
		node := testManagedNode("worker1")
		createAggregateObjects(policy, node, testOperatorConfig())
		state := aggregateNodeState(node)
		state.Status.Interfaces[0].Name = "ens2"
		Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
		expectAggregate(policyStatus(ctx, k8sClient, policy), policy.Generation, 1, 0, sriovnetworkv1.ReasonNoMatchingInterfaces)

		matchingNode := testManagedNode("worker2")
		createAggregateObjects(matchingNode)
		matchingState := aggregateNodeState(matchingNode)
		Expect(matchingState.SetPolicyRevisions(map[string]sriovnetworkv1.SourceRevision{
			policy.Name: {UID: policy.UID, Generation: policy.Generation},
		})).To(Succeed())
		Expect(k8sClient.Update(ctx, matchingState)).To(Succeed())
		result := policyStatus(ctx, k8sClient, policy)
		expectAggregate(result, policy.Generation, 2, 1, sriovnetworkv1.ReasonNoMatchingInterfaces)
		Expect(result.condition.Message).To(Equal("1/2 target nodes ready; no interfaces match the policy NIC selector on: worker1"))
	})

	It("reports drain-only pools ready and recovers from overlapping membership", func(ctx context.Context) {
		pool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: vars.Namespace},
			Spec: sriovnetworkv1.SriovNetworkPoolConfigSpec{NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"node-role.kubernetes.io/worker": ""}}}}
		createDrainOnlyPool(pool)
		createAggregateObjects(testManagedNode("worker1"), testOperatorConfig())
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 1, sriovnetworkv1.ReasonPoolConfigReady)
		other := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "overlap", Namespace: vars.Namespace}, Spec: pool.Spec}
		createDrainOnlyPool(other)
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 0, sriovnetworkv1.ReasonPoolConflict)
		expectAggregate(regularPoolStatus(ctx, k8sClient, other), other.Generation, 1, 0, sriovnetworkv1.ReasonPoolConflict)
		Expect(k8sClient.Delete(ctx, other)).To(Succeed())
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 1, sriovnetworkv1.ReasonPoolConfigReady)
	})

	It("requires current pool identity, revision, and NodeState readiness", func(ctx context.Context) {
		pool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: vars.Namespace},
			Spec: sriovnetworkv1.SriovNetworkPoolConfigSpec{RdmaMode: "shared"}}
		node := testManagedNode("worker1")
		createAggregateObjects(pool, node, testOperatorConfig())
		state := aggregateNodeState(node)
		Expect(pool.SetRevisionOnObject(state)).To(Succeed())
		state.Spec.System.RdmaMode = "shared"
		Expect(k8sClient.Update(ctx, state)).To(Succeed())
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 0, sriovnetworkv1.ReasonApplyingConfiguration)
		state.SetNodeStateConfigurationConditions(constants.SyncStatusSucceeded, "", false)
		Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 1, sriovnetworkv1.ReasonPoolConfigReady)
		pool.Spec.RdmaMode = "exclusive"
		Expect(k8sClient.Update(ctx, pool)).To(Succeed())
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 0, sriovnetworkv1.ReasonApplyingConfiguration)
		Expect(pool.SetRevisionOnObject(state)).To(Succeed())
		Expect(k8sClient.Update(ctx, state)).To(Succeed())
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 1, sriovnetworkv1.ReasonPoolConfigReady)
		Expect(k8sClient.Delete(ctx, pool)).To(Succeed())
		replacement := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: vars.Namespace}, Spec: pool.Spec}
		createAggregateObjects(replacement)
		expectAggregate(regularPoolStatus(ctx, k8sClient, replacement), replacement.Generation, 1, 0, sriovnetworkv1.ReasonApplyingConfiguration)
	})

	It("uses a matching pool stamp without requiring a system spec", func(ctx context.Context) {
		pool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: vars.Namespace},
			Spec: sriovnetworkv1.SriovNetworkPoolConfigSpec{RdmaMode: "shared"}}
		node := testManagedNode("worker1")
		createAggregateObjects(pool, node, testOperatorConfig())
		state := aggregateNodeState(node)
		Expect(pool.SetRevisionOnObject(state)).To(Succeed())
		Expect(k8sClient.Update(ctx, state)).To(Succeed())
		expectAggregate(regularPoolStatus(ctx, k8sClient, pool), pool.Generation, 1, 1, sriovnetworkv1.ReasonPoolConfigReady)
	})

	It("converges through the running Kubernetes controller without MachineConfig types", func(ctx context.Context) {
		kubernetesScheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(kubernetesScheme)).To(Succeed())
		Expect(sriovnetworkv1.AddToScheme(kubernetesScheme)).To(Succeed())
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: kubernetesScheme, Metrics: server.Options{BindAddress: "0"}})
		Expect(err).NotTo(HaveOccurred())
		r := &AggregateStatusReconciler{Client: mgr.GetClient(),
			StatusPatcher: status.NewPatcher(mgr.GetClient(), nil, mgr.GetScheme(), "aggregate-status-test")}
		Expect(r.SetupWithManager(mgr)).To(Succeed())
		pool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: vars.Namespace}}
		createAggregateObjects(testOperatorConfig(), testManagedNode("worker"), pool)
		managerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- mgr.Start(managerCtx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 30*time.Second).Should(Receive(Succeed())) })
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			g.Expect(pool.Status.MatchedNodeCount).To(Equal(new(int32(1))))
			g.Expect(pool.Status.Conditions).To(HaveLen(1))
			g.Expect(pool.Status.Conditions[0].ObservedGeneration).To(Equal(pool.Generation))
		}, 30*time.Second, 50*time.Millisecond).Should(Succeed())
		pool.Spec.OvsHardwareOffloadConfig = &sriovnetworkv1.OvsHardwareOffloadConfig{Name: "infra"}
		Expect(k8sClient.Update(ctx, pool)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			g.Expect(pool.Status.MatchedNodeCount).To(Equal(new(int32(1))))
			g.Expect(pool.Status.ReadyNodeCount).To(Equal(new(int32(0))))
			g.Expect(pool.Status.Conditions).To(HaveLen(1))
			g.Expect(pool.Status.Conditions[0].Reason).To(Equal(sriovnetworkv1.ReasonApplyingConfiguration))
			g.Expect(pool.Status.Conditions[0].ObservedGeneration).To(Equal(pool.Generation))
		}, 30*time.Second, 50*time.Millisecond).Should(Succeed())
	})
})

var _ = Describe("Aggregate status helpers", func() {
	It("enqueues relevant NodeState changes", func() {
		base := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{
			Name: "worker1", Generation: 2,
			Annotations: map[string]string{constants.NodeStatePolicyRevisionsAnnotation: `{"policy":{"generation":1}}`},
		}, Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
			SyncStatus: constants.SyncStatusSucceeded,
			Interfaces: sriovnetworkv1.InterfaceExts{{Name: "ens1", PciAddress: "0000:01:00.0"}},
			Conditions: []metav1.Condition{{Type: sriovnetworkv1.ConditionReady, Status: metav1.ConditionTrue,
				Reason: sriovnetworkv1.ReasonNodeReady, ObservedGeneration: 2}},
		}}
		Expect(base.AffectsAggregate(base.DeepCopy())).To(BeFalse(), "unchanged node state must not enqueue")
		sameHostRefresh := base.DeepCopy()
		sameHostRefresh.Status.Interfaces[0].Mac = "aa:bb:cc:dd:ee:ff"
		Expect(!base.AffectsAggregate(sameHostRefresh)).To(BeFalse(), "any interface status change must enqueue; aggregate reconciliation handles unchanged readiness")
		renamed := base.DeepCopy()
		renamed.Status.Interfaces[0].Name = "ens2"
		Expect(!base.AffectsAggregate(renamed)).To(BeFalse(), "NIC selector fields changing must enqueue even when interface count is unchanged")
		added := base.DeepCopy()
		added.Status.Interfaces = append(added.Status.Interfaces, sriovnetworkv1.InterfaceExt{Name: "ens2"})
		Expect(!base.AffectsAggregate(added)).To(BeFalse(), "NIC discovery must enqueue even when interfaces were already present")
		stamped := base.DeepCopy()
		stamped.Annotations[constants.PoolRevisionAnnotation] = `{"name":"pool","generation":2}`
		Expect(!base.AffectsAggregate(stamped)).To(BeFalse(), "pool revision stamp change must enqueue")
		discovered := base.DeepCopy()
		discovered.Status.Interfaces = nil
		Expect(!base.AffectsAggregate(discovered)).To(BeFalse(), "interface presence change must enqueue")
	})
	It("ignores interface discovery order without mutating the inputs", func() {
		base := &sriovnetworkv1.SriovNetworkNodeState{
			Status: sriovnetworkv1.SriovNetworkNodeStateStatus{Interfaces: sriovnetworkv1.InterfaceExts{
				{Name: "ens1", PciAddress: "0000:01:00.0", AltNames: []string{"pf0", "alt0"},
					VFs: []sriovnetworkv1.VirtualFunction{{VfID: 1, PciAddress: "0000:01:00.2"}, {VfID: 0, PciAddress: "0000:01:00.1"}}},
				{Name: "ens2", PciAddress: "0000:02:00.0"},
			}},
		}
		for _, tc := range []struct {
			name          string
			change        func(*sriovnetworkv1.SriovNetworkNodeState)
			wantReconcile bool
		}{
			{"interface order", func(s *sriovnetworkv1.SriovNetworkNodeState) {
				s.Status.Interfaces[0], s.Status.Interfaces[1] = s.Status.Interfaces[1], s.Status.Interfaces[0]
			}, false},
			{"alternate name order", func(s *sriovnetworkv1.SriovNetworkNodeState) {
				names := s.Status.Interfaces[0].AltNames
				names[0], names[1] = names[1], names[0]
			}, false},
			{"VF order", func(s *sriovnetworkv1.SriovNetworkNodeState) {
				vfs := s.Status.Interfaces[0].VFs
				vfs[0], vfs[1] = vfs[1], vfs[0]
			}, false},
			{"nil and empty slices", func(s *sriovnetworkv1.SriovNetworkNodeState) {
				s.Status.Interfaces[1].AltNames = []string{}
				s.Status.Interfaces[1].VFs = []sriovnetworkv1.VirtualFunction{}
			}, false},
			{"interface field change", func(s *sriovnetworkv1.SriovNetworkNodeState) { s.Status.Interfaces[0].Mtu = 9000 }, true},
			{"VF field change", func(s *sriovnetworkv1.SriovNetworkNodeState) { s.Status.Interfaces[0].VFs[0].Mtu = 9000 }, true},
			{"alternate name change", func(s *sriovnetworkv1.SriovNetworkNodeState) { s.Status.Interfaces[0].AltNames[0] = "new-alias" }, true},
		} {
			By(tc.name)
			changed := base.DeepCopy()
			tc.change(changed)
			originalBase, originalChanged := base.DeepCopy(), changed.DeepCopy()
			Expect(base.AffectsAggregate(changed)).To(Equal(tc.wantReconcile))
			Expect(base).To(Equal(originalBase))
			Expect(changed).To(Equal(originalChanged))
		}
	})
	It("ignores JSON field order while detecting changed values", func() {
		var a, b sriovnetworkv1.InterfaceExts
		for _, input := range []struct {
			data   string
			target *sriovnetworkv1.InterfaceExts
		}{
			{`[{"name":"ens1","pciAddress":"0000:01:00.0","mtu":1500}]`, &a},
			{`[{"mtu":1500,"pciAddress":"0000:01:00.0","name":"ens1"}]`, &b},
		} {
			Expect(json.Unmarshal([]byte(input.data), input.target)).To(Succeed())
		}
		Expect(equality.Semantic.DeepEqual(a, b)).To(BeTrue())
		source := &sriovnetworkv1.SriovNetworkNodeState{Status: sriovnetworkv1.SriovNetworkNodeStateStatus{Interfaces: a}}
		dest := &sriovnetworkv1.SriovNetworkNodeState{Status: sriovnetworkv1.SriovNetworkNodeStateStatus{Interfaces: b}}
		Expect(source.AffectsAggregate(dest)).To(BeFalse())
		b[0].Mtu = 9000
		Expect(equality.Semantic.DeepEqual(a, b)).To(BeFalse())
		Expect(source.AffectsAggregate(dest)).To(BeTrue())
	})
	It("limits messages to three sorted nodes with the selected reason", func() {
		issues := []aggregateIssue{{node: "d"}, {node: "c"}, {node: "b"}, {node: "a"}}
		message := aggregateMessage(0, 4, issues, "")
		Expect(message).To(ContainSubstring("affected: a, b, c"))
		Expect(message).NotTo(ContainSubstring("a, b, c, d"))
		filtered := aggregateMessage(0, 2, []aggregateIssue{
			{reason: sriovnetworkv1.ReasonNodeSyncFailed, node: "failed"},
			{reason: sriovnetworkv1.ReasonNoMatchingInterfaces, node: "other"},
		}, sriovnetworkv1.ReasonNodeSyncFailed)
		Expect(filtered).To(ContainSubstring("affected: failed"))
		Expect(filtered).NotTo(ContainSubstring("other"))
	})
	It("reports invalid selectors and failed observations", func() {
		pool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "invalid"},
			Spec: sriovnetworkv1.SriovNetworkPoolConfigSpec{NodeSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "role", Operator: "invalid"}},
			}}}
		valid := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "valid"}}
		memberships := selectPoolNodes([]sriovnetworkv1.SriovNetworkPoolConfig{*pool, *valid}, []corev1.Node{*testManagedNode("worker")})
		invalid := regularPoolStatusFrom(pool, nil, nil, nil, memberships)
		Expect(invalid.condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(invalid.condition.Reason).To(Equal(sriovnetworkv1.ReasonPoolConfigSyncFailed))
		unknown := regularPoolStatusFrom(valid, nil, nil, nil, memberships)
		Expect(unknown.condition.Status).To(Equal(metav1.ConditionUnknown))
		Expect(unknown.matched).To(BeNil())
		Expect(unknown.ready).To(BeNil())
		failedRead := regularPoolStatusFrom(valid, errors.New("nodes unavailable"), nil, nil, memberships)
		Expect(failedRead.condition.Status).To(Equal(metav1.ConditionUnknown))
	})
})

var _ = Describe("Aggregate hardware offload status", func() {
	BeforeEach(func() {
		previousNamespace, previousClusterType := vars.Namespace, vars.ClusterType
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aggregate-offload-"}}
		createAggregateObjects(ns)
		vars.Namespace = ns.Name
		DeferCleanup(func() { vars.Namespace = previousNamespace; vars.ClusterType = previousClusterType })
	})

	DescribeTable("uses the platform's configuration flow",
		func(clusterType constants.ClusterType) {
			ctx := context.Background()
			vars.ClusterType = clusterType
			pool := &sriovnetworkv1.SriovNetworkPoolConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "offload", Namespace: vars.Namespace},
				Spec: sriovnetworkv1.SriovNetworkPoolConfigSpec{
					OvsHardwareOffloadConfig: &sriovnetworkv1.OvsHardwareOffloadConfig{
						Name: "infra", OvsConfig: map[string]string{"hw-offload": "true", "max-idle": "10000"}}},
			}
			node, config := testManagedNode("offload-worker"), testOperatorConfig()
			createAggregateObjects(pool, node, config)
			state := aggregateNodeState(node)
			r := &AggregateStatusReconciler{Client: k8sClient,
				StatusPatcher: status.NewPatcher(k8sClient, nil, k8sClient.Scheme(), "aggregate-offload-test")}
			reconcile := func() {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: aggregateStatusSyncEventName}})
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			}
			assertCounts := func(ready int32, reason string) {
				Expect(pool.Status.MatchedNodeCount).To(Equal(new(int32(1))))
				Expect(pool.Status.ReadyNodeCount).To(Equal(&ready))
				Expect(pool.Status.Conditions).To(HaveLen(1))
				Expect(pool.Status.Conditions[0].Reason).To(Equal(reason))
				Expect(pool.Status.Conditions[0].ObservedGeneration).To(Equal(pool.Generation))
				wantStatus := metav1.ConditionFalse
				if ready == 1 {
					wantStatus = metav1.ConditionTrue
				}
				Expect(pool.Status.Conditions[0].Status).To(Equal(wantStatus))
			}
			if clusterType == constants.ClusterTypeOpenshift {
				pool.Spec.OvsHardwareOffloadConfig.Name = ""
				Expect(k8sClient.Update(ctx, pool)).To(Succeed())
				reconcile()
				assertCounts(0, sriovnetworkv1.ReasonApplyingConfiguration)
				pool.Spec.OvsHardwareOffloadConfig.Name = "infra"
				Expect(k8sClient.Update(ctx, pool)).To(Succeed())
			}
			selected, _, err := findNodePoolConfig(ctx, node, k8sClient)
			Expect(err).NotTo(HaveOccurred())
			if clusterType == constants.ClusterTypeOpenshift {
				Expect(selected.Name).NotTo(Equal(pool.Name))
			} else {
				Expect(selected.Name).To(Equal(pool.Name))
			}
			reconcile()
			if clusterType == constants.ClusterTypeOpenshift {
				Expect(pool.Status.Conditions).To(HaveLen(1))
				Expect(pool.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
				Expect(pool.Status.Conditions[0].Reason).To(Equal(sriovnetworkv1.ReasonUnsupportedConfiguration))
				Expect(pool.Status.Conditions[0].ObservedGeneration).To(Equal(pool.Generation))
				Expect(pool.Status.MatchedNodeCount).To(BeNil())
				Expect(pool.Status.ReadyNodeCount).To(BeNil())
			} else {
				assertCounts(0, sriovnetworkv1.ReasonApplyingConfiguration)
				policyReconciler := &SriovNetworkNodePolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
				syncNode := func() {
					Expect(policyReconciler.syncAllSriovNetworkNodeStates(ctx, config,
						&sriovnetworkv1.SriovNetworkNodePolicyList{}, &corev1.NodeList{Items: []corev1.Node{*node}})).To(Succeed())
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(state), state)).To(Succeed())
					Expect(state.Spec.System.OvsConfig).To(Equal(pool.Spec.OvsHardwareOffloadConfig.OvsConfig))
					Expect(pool.RevisionMatchesInObject(state)).To(BeTrue())
				}
				applyNode := func() {
					state.SetNodeStateConfigurationConditions(constants.SyncStatusSucceeded, "", false)
					Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
					reconcile()
					assertCounts(1, sriovnetworkv1.ReasonPoolConfigReady)
				}
				syncNode()
				reconcile()
				assertCounts(0, sriovnetworkv1.ReasonApplyingConfiguration)
				applyNode()
				pool.Spec.OvsHardwareOffloadConfig.OvsConfig["max-idle"] = "30000"
				Expect(k8sClient.Update(ctx, pool)).To(Succeed())
				reconcile()
				assertCounts(0, sriovnetworkv1.ReasonApplyingConfiguration)
				syncNode()
				reconcile()
				assertCounts(0, sriovnetworkv1.ReasonApplyingConfiguration)
				applyNode()
			}

			other := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{Name: "overlap", Namespace: vars.Namespace}}
			createDrainOnlyPool(other)
			reconcile()
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(other), other)).To(Succeed())
			if clusterType == constants.ClusterTypeOpenshift {
				Expect(other.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
				Expect(other.Status.ReadyNodeCount).To(Equal(new(int32(1))))
			} else {
				assertCounts(0, sriovnetworkv1.ReasonPoolConflict)
				Expect(other.Status.Conditions[0].Reason).To(Equal(sriovnetworkv1.ReasonPoolConflict))
			}
		},
		Entry("OpenShift skips MachineConfig hardware offload", constants.ClusterTypeOpenshift),
		Entry("Kubernetes applies OVS configuration through NodeStates", constants.ClusterTypeKubernetes),
	)
})
