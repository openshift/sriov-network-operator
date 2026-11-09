package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/clean"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
)

var sriovInfos *cluster.EnabledNodes
var platformType consts.PlatformTypes

var _ = BeforeSuite(func() {
	err := clean.All()
	Expect(err).NotTo(HaveOccurred())

	isSingleNode, err := cluster.IsSingleNode(clients)
	Expect(err).ToNot(HaveOccurred())
	if isSingleNode {
		disableDrainState, err := cluster.GetNodeDrainState(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		if discovery.Enabled() {
			if !disableDrainState {
				Fail("SriovOperatorConfig DisableDrain property must be enabled in a single node environment")
			}
		}
		snoTimeoutMultiplier = 1
		if !disableDrainState {
			err = cluster.SetDisableNodeDrainState(clients, operatorNamespace, true)
			Expect(err).ToNot(HaveOccurred())
			clean.RestoreNodeDrainState = true
		}
	}

	if !isFeatureFlagEnabled(consts.ResourceInjectorMatchConditionFeatureGate) {
		setFeatureFlag(consts.ResourceInjectorMatchConditionFeatureGate, true)
	}

	platformType, err = cluster.GetPlatformType(clients, operatorNamespace)
	Expect(err).ToNot(HaveOccurred())

	err = namespaces.Create(namespaces.Test, clients)
	Expect(err).ToNot(HaveOccurred())
	err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
	Expect(err).ToNot(HaveOccurred())
	WaitForSRIOVStable()

	switch platformType {
	case consts.Baremetal:
		sriovInfos, err = cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
	case consts.AWS:
		sriovInfos, err = cluster.DiscoverSriovForAws(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
	case consts.VirtualOpenStack:
		Fail("VirtualOpenStack platform is not supported on e2e tests")
	default:
		Fail("Unknown platform type")
	}
})

var _ = AfterSuite(func() {
	err := clean.All()
	Expect(err).NotTo(HaveOccurred())
})

// conditionGetter is implemented by SR-IOV resources that expose conditions.
type conditionGetter interface {
	GetConditions() []metav1.Condition
}

// assertCondition verifies that a SR-IOV resource has the expected condition status.
// Additional checks run against the same fetched object as the condition assertion.
func assertCondition(obj runtimeclient.Object, conditionType string, expectedStatus metav1.ConditionStatus, checks ...func(Gomega)) {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	EventuallyWithOffset(1, func(g Gomega) {
		err := clients.Get(context.Background(),
			runtimeclient.ObjectKey{Name: name, Namespace: namespace}, obj)
		g.Expect(err).ToNot(HaveOccurred())

		getter, ok := obj.(conditionGetter)
		g.Expect(ok).To(BeTrue(), "object %T does not implement GetConditions", obj)
		conditions := getter.GetConditions()

		cond := meta.FindStatusCondition(conditions, conditionType)
		g.Expect(cond).ToNot(BeNil(), "Condition %s not found on %T %s/%s", conditionType, obj, namespace, name)
		g.Expect(cond.Status).To(Equal(expectedStatus),
			"Expected condition %s to be %s but got %s", conditionType, expectedStatus, cond.Status)
		for _, check := range checks {
			check(g)
		}
	}, 2*time.Minute, time.Second).Should(Succeed())
}

// assertNodeStatePolicyRevision waits for the current policy revision on a node.
func assertNodeStatePolicyRevision(nodeName string, policy *sriovv1.SriovNetworkNodePolicy) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		nodeState := &sriovv1.SriovNetworkNodeState{}
		g.Expect(clients.Get(context.Background(), runtimeclient.ObjectKey{Name: nodeName, Namespace: operatorNamespace}, nodeState)).To(Succeed())
		g.Expect(nodeState.Annotations).To(HaveKey(consts.NodeStatePolicyRevisionsAnnotation))
		g.Expect(policy.RevisionMatchesInNodeState(nodeState)).To(BeTrue(), "NodeState annotations: %v", nodeState.Annotations)
	}, 2*time.Minute, time.Second).Should(Succeed())
}

// assertNodeStatePolicyRevisionAbsent waits until a node no longer records the policy.
func assertNodeStatePolicyRevisionAbsent(nodeName string, policy *sriovv1.SriovNetworkNodePolicy) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		nodeState := &sriovv1.SriovNetworkNodeState{}
		g.Expect(clients.Get(context.Background(), runtimeclient.ObjectKey{Name: nodeName, Namespace: operatorNamespace}, nodeState)).To(Succeed())
		var revisions map[string]sriovv1.SourceRevision
		if annotation, found := nodeState.Annotations[consts.NodeStatePolicyRevisionsAnnotation]; found {
			g.Expect(json.Unmarshal([]byte(annotation), &revisions)).To(Succeed())
		}
		g.Expect(revisions).NotTo(HaveKey(policy.Name))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

// assertNodeStatePoolRevision checks the pool stamp; nil means the annotation must be absent.
func assertNodeStatePoolRevision(nodeName string, pool *sriovv1.SriovNetworkPoolConfig) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		nodeState := &sriovv1.SriovNetworkNodeState{}
		g.Expect(clients.Get(context.Background(), runtimeclient.ObjectKey{Name: nodeName, Namespace: operatorNamespace}, nodeState)).To(Succeed())
		if pool == nil {
			g.Expect(nodeState.Annotations).NotTo(HaveKey(consts.PoolRevisionAnnotation))
		} else {
			g.Expect(nodeState.Annotations).To(HaveKey(consts.PoolRevisionAnnotation))
			g.Expect(pool.RevisionMatchesInObject(nodeState)).To(BeTrue(), "NodeState annotations: %v", nodeState.Annotations)
		}
	}, 2*time.Minute, time.Second).Should(Succeed())
}

// assertAggregateReadiness checks Ready and node counts together; nil means a counter must be absent.
func assertAggregateReadiness(obj runtimeclient.Object, expectedStatus metav1.ConditionStatus, reason string, expectedMatched, expectedReady *int32) {
	GinkgoHelper()
	assertCondition(obj, sriovv1.ConditionReady, expectedStatus, func(g Gomega) {
		condition := meta.FindStatusCondition(obj.(conditionGetter).GetConditions(), sriovv1.ConditionReady)
		g.Expect(condition.Reason).To(Equal(reason))
		g.Expect(condition.ObservedGeneration).To(Equal(obj.GetGeneration()))

		var matched, ready *int32
		switch resource := obj.(type) {
		case *sriovv1.SriovNetworkNodePolicy:
			matched, ready = resource.Status.MatchedNodeCount, resource.Status.ReadyNodeCount
		case *sriovv1.SriovNetworkPoolConfig:
			matched, ready = resource.Status.MatchedNodeCount, resource.Status.ReadyNodeCount
		default:
			Fail(fmt.Sprintf("aggregate status is not supported for %T", obj))
		}
		g.Expect(matched).To(Equal(expectedMatched), "matchedNodeCount on %s", obj.GetName())
		g.Expect(ready).To(Equal(expectedReady), "readyNodeCount on %s", obj.GetName())
	})
}
