package tests

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

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

// conditionGetter is implemented by all network CRDs that expose conditions.
type conditionGetter interface {
	GetConditions() []metav1.Condition
}

// assertCondition verifies that a SR-IOV resource has the expected condition status.
// The object must implement the conditionGetter interface (SriovNetwork, SriovIBNetwork, OVSNetwork).
func assertCondition(obj runtimeclient.Object, conditionType string, expectedStatus metav1.ConditionStatus) {
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
	}, 2*time.Minute, time.Second).Should(Succeed())
}
