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

package status

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	patcher   Interface
)

func TestStatus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Status Package Suite")
}

var _ = BeforeSuite(func() {
	err := os.Chdir(filepath.Join("..", ".."))
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	patcher = NewPatcher(k8sClient, nil, scheme.Scheme, "status-test")
})

var _ = AfterSuite(func() {
	if testEnv != nil {
		Eventually(func() error {
			return testEnv.Stop()
		}, 30*time.Second, time.Second).ShouldNot(HaveOccurred())
	}
})

var _ = Describe("NewCondition", func() {
	It("should create a condition with all fields set", func() {
		cond := NewCondition("Ready", metav1.ConditionTrue, "TestReason", "Test message", 3)

		Expect(cond.Type).To(Equal("Ready"))
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("TestReason"))
		Expect(cond.Message).To(Equal("Test message"))
		Expect(cond.ObservedGeneration).To(Equal(int64(3)))
	})
})

var _ = Describe("ApplyCondition", func() {
	var (
		ctx context.Context
		cr  *sriovnetworkv1.SriovNetwork
	)

	BeforeEach(func() {
		ctx = context.Background()
		cr = &sriovnetworkv1.SriovNetwork{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-patcher-",
				Namespace:    "default",
			},
			Spec: sriovnetworkv1.SriovNetworkSpec{
				ResourceName: "test",
			},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, cr)
		})
	})

	It("should apply a Ready=True condition", func() {
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "all good", cr.Generation),
		)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())

		Expect(updated.Status.Conditions).To(HaveLen(1))
		Expect(updated.Status.Conditions[0].Type).To(Equal("Ready"))
		Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
		Expect(updated.Status.Conditions[0].Reason).To(Equal("NetworkReady"))
		Expect(updated.Status.Conditions[0].Message).To(Equal("all good"))
	})

	It("should apply a Ready=False condition", func() {
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionFalse, "NamespaceNotFound", "namespace missing", cr.Generation),
		)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())

		Expect(updated.Status.Conditions).To(HaveLen(1))
		Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
		Expect(updated.Status.Conditions[0].Reason).To(Equal("NamespaceNotFound"))
	})

	It("should update an existing condition in place", func() {
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionFalse, "NotReady", "waiting", cr.Generation),
		)).To(Succeed())

		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "done", cr.Generation),
		)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())

		Expect(updated.Status.Conditions).To(HaveLen(1))
		Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
		Expect(updated.Status.Conditions[0].Reason).To(Equal("NetworkReady"))
	})

	It("should not conflict when applying without re-fetching the object", func() {
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionFalse, "NotReady", "first", cr.Generation),
		)).To(Succeed())

		// Apply again using the same stale object (no Get between calls)
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "second", cr.Generation),
		)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())
		Expect(updated.Status.Conditions[0].Message).To(Equal("second"))
	})

	It("should preserve LastTransitionTime when status has not changed", func() {
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "ready", cr.Generation),
		)).To(Succeed())

		// Fetch to get the persisted LastTransitionTime
		first := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), first)).To(Succeed())
		originalTime := first.Status.Conditions[0].LastTransitionTime

		// Apply the same status again using the fetched object (simulates next reconcile)
		Expect(patcher.ApplyCondition(ctx, first,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "ready", first.Generation),
		)).To(Succeed())

		second := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), second)).To(Succeed())
		Expect(second.Status.Conditions[0].LastTransitionTime.Equal(&originalTime)).
			To(BeTrue(), "LastTransitionTime should be preserved when status is unchanged")
	})

	It("should preserve LastTransitionTime when only reason or message changes", func() {
		oldTime := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		cond := NewCondition("Ready", metav1.ConditionFalse, "ReasonA", "message A", cr.Generation)
		cond.LastTransitionTime = oldTime

		Expect(patcher.ApplyCondition(ctx, cr, cond)).To(Succeed())

		first := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), first)).To(Succeed())
		Expect(first.Status.Conditions[0].LastTransitionTime.Equal(&oldTime)).To(BeTrue())

		// Change reason and message but keep Status=False
		Expect(patcher.ApplyCondition(ctx, first,
			NewCondition("Ready", metav1.ConditionFalse, "ReasonB", "message B", first.Generation),
		)).To(Succeed())

		second := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), second)).To(Succeed())
		Expect(second.Status.Conditions[0].Reason).To(Equal("ReasonB"))
		Expect(second.Status.Conditions[0].Message).To(Equal("message B"))
		Expect(second.Status.Conditions[0].LastTransitionTime.Equal(&oldTime)).
			To(BeTrue(), "LastTransitionTime should be preserved when only reason/message change")
	})

	It("should update LastTransitionTime when status changes", func() {
		oldTime := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		cond := NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "ready", cr.Generation)
		cond.LastTransitionTime = oldTime

		Expect(patcher.ApplyCondition(ctx, cr, cond)).To(Succeed())

		first := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), first)).To(Succeed())
		Expect(first.Status.Conditions[0].LastTransitionTime.Equal(&oldTime)).To(BeTrue())

		// Transition from True -> False: LastTransitionTime must be updated
		Expect(patcher.ApplyCondition(ctx, first,
			NewCondition("Ready", metav1.ConditionFalse, "NotReady", "broken", first.Generation),
		)).To(Succeed())

		second := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), second)).To(Succeed())
		Expect(second.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
		Expect(second.Status.Conditions[0].LastTransitionTime.Equal(&oldTime)).
			To(BeFalse(), "LastTransitionTime should change when status transitions")
	})

	It("should be a no-op when called with zero conditions", func() {
		Expect(patcher.ApplyCondition(ctx, cr)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())
		Expect(updated.Status.Conditions).To(BeEmpty())
	})

	It("should apply multiple conditions at once", func() {
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "ready", cr.Generation),
			NewCondition("Custom", metav1.ConditionFalse, "Pending", "pending work", cr.Generation),
		)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())

		Expect(updated.Status.Conditions).To(HaveLen(2))
	})

	It("should skip the API call when condition has not changed", func() {
		Expect(patcher.ApplyCondition(ctx, cr,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "ready", cr.Generation),
		)).To(Succeed())

		// Fetch to get the current state
		fetched := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), fetched)).To(Succeed())
		rv := fetched.ResourceVersion

		// Apply the exact same condition using the fetched object
		Expect(patcher.ApplyCondition(ctx, fetched,
			NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "ready", fetched.Generation),
		)).To(Succeed())

		// ResourceVersion should not change since no API call was made
		afterSkip := &sriovnetworkv1.SriovNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), afterSkip)).To(Succeed())
		Expect(afterSkip.ResourceVersion).To(Equal(rv),
			"ResourceVersion should not change when condition is unchanged")
	})

	Context("with two field managers", func() {
		var patcher2 Interface

		BeforeEach(func() {
			patcher2 = NewPatcher(k8sClient, nil, scheme.Scheme, "other-manager")
		})

		It("should allow different managers to own different condition types", func() {
			Expect(patcher.ApplyCondition(ctx, cr,
				NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "from manager 1", cr.Generation),
			)).To(Succeed())

			Expect(patcher2.ApplyCondition(ctx, cr,
				NewCondition("Custom", metav1.ConditionFalse, "Pending", "from manager 2", cr.Generation),
			)).To(Succeed())

			updated := &sriovnetworkv1.SriovNetwork{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())

			Expect(updated.Status.Conditions).To(HaveLen(2))

			var readyCond, customCond *metav1.Condition
			for i := range updated.Status.Conditions {
				switch updated.Status.Conditions[i].Type {
				case "Ready":
					readyCond = &updated.Status.Conditions[i]
				case "Custom":
					customCond = &updated.Status.Conditions[i]
				}
			}
			Expect(readyCond).ToNot(BeNil())
			Expect(readyCond.Message).To(Equal("from manager 1"))
			Expect(customCond).ToNot(BeNil())
			Expect(customCond.Message).To(Equal("from manager 2"))
		})

		It("should not remove conditions owned by another manager", func() {
			Expect(patcher.ApplyCondition(ctx, cr,
				NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "manager 1", cr.Generation),
			)).To(Succeed())

			Expect(patcher2.ApplyCondition(ctx, cr,
				NewCondition("Custom", metav1.ConditionTrue, "Done", "manager 2", cr.Generation),
			)).To(Succeed())

			// Manager 1 applies again — should NOT remove Custom
			Expect(patcher.ApplyCondition(ctx, cr,
				NewCondition("Ready", metav1.ConditionFalse, "NotReady", "updated by manager 1", cr.Generation),
			)).To(Succeed())

			updated := &sriovnetworkv1.SriovNetwork{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())

			Expect(updated.Status.Conditions).To(HaveLen(2))
		})

		It("should not modify the value of conditions owned by another manager", func() {
			Expect(patcher.ApplyCondition(ctx, cr,
				NewCondition("Ready", metav1.ConditionTrue, "NetworkReady", "manager 1", cr.Generation),
			)).To(Succeed())

			Expect(patcher2.ApplyCondition(ctx, cr,
				NewCondition("Custom", metav1.ConditionFalse, "Pending", "original value", cr.Generation),
			)).To(Succeed())

			// Manager 1 updates its own condition — Custom should stay exactly as manager 2 set it
			Expect(patcher.ApplyCondition(ctx, cr,
				NewCondition("Ready", metav1.ConditionFalse, "NotReady", "changed", cr.Generation),
			)).To(Succeed())

			updated := &sriovnetworkv1.SriovNetwork{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), updated)).To(Succeed())

			Expect(updated.Status.Conditions).To(HaveLen(2))
			for _, c := range updated.Status.Conditions {
				if c.Type == "Custom" {
					Expect(c.Status).To(Equal(metav1.ConditionFalse))
					Expect(c.Reason).To(Equal("Pending"))
					Expect(c.Message).To(Equal("original value"))
				}
			}
		})
	})
})
