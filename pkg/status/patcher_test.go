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

var _ = Describe("ApplyStatus", func() {
	var (
		ctx       context.Context
		nodeState *sriovnetworkv1.SriovNetworkNodeState
		daemonMgr Interface
		drainMgr  Interface
	)

	BeforeEach(func() {
		ctx = context.Background()
		daemonMgr = NewPatcher(k8sClient, nil, scheme.Scheme, "daemon-manager")
		drainMgr = NewPatcher(k8sClient, nil, scheme.Scheme, "drain-manager")

		nodeState = &sriovnetworkv1.SriovNetworkNodeState{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-nodestate-",
				Namespace:    "default",
			},
		}
		Expect(k8sClient.Create(ctx, nodeState)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, nodeState)
		})
	})

	It("should apply non-condition status fields and conditions together", func() {
		statusFields := map[string]interface{}{
			"syncStatus":    "Succeeded",
			"lastSyncError": "",
		}
		conditions := []metav1.Condition{
			NewCondition("Ready", metav1.ConditionTrue, "NodeReady", "all good", nodeState.Generation),
			NewCondition("Progressing", metav1.ConditionFalse, "NotProgressing", "done", nodeState.Generation),
		}

		Expect(daemonMgr.ApplyStatus(ctx, nodeState, statusFields, conditions)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())

		Expect(updated.Status.SyncStatus).To(Equal("Succeeded"))
		Expect(updated.Status.LastSyncError).To(BeEmpty())
		Expect(updated.Status.Conditions).To(HaveLen(2))
	})

	It("should ignore conditions in statusFields and apply ownedConditions only", func() {
		statusFields := map[string]interface{}{
			"syncStatus":    "Succeeded",
			"lastSyncError": "",
			"conditions": []map[string]interface{}{
				{
					"type":    "Ready",
					"status":  "False",
					"reason":  "StaleReason",
					"message": "stale condition from statusFields",
				},
			},
		}
		ownedConditions := []metav1.Condition{
			NewCondition("Ready", metav1.ConditionTrue, "NodeReady", "from ownedConditions", nodeState.Generation),
		}

		Expect(daemonMgr.ApplyStatus(ctx, nodeState, statusFields, ownedConditions)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())

		Expect(updated.Status.SyncStatus).To(Equal("Succeeded"))
		Expect(updated.Status.Conditions).To(HaveLen(1))
		Expect(updated.Status.Conditions[0].Type).To(Equal("Ready"))
		Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
		Expect(updated.Status.Conditions[0].Reason).To(Equal("NodeReady"))
		Expect(updated.Status.Conditions[0].Message).To(Equal("from ownedConditions"))
	})

	It("should apply conditions when status fields are nil", func() {
		conditions := []metav1.Condition{
			NewCondition("Ready", metav1.ConditionTrue, "NodeReady", "all good", nodeState.Generation),
		}

		Expect(daemonMgr.ApplyStatus(ctx, nodeState, nil, conditions)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())
		Expect(updated.Status.Conditions).To(HaveLen(1))
		Expect(updated.Status.Conditions[0].Type).To(Equal("Ready"))
		Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
	})

	It("should apply complex status fields like interfaces and bridges", func() {
		ifaces := sriovnetworkv1.InterfaceExts{
			{PciAddress: "0000:00:01.0", Name: "eth0", NumVfs: 4, TotalVfs: 8},
		}
		statusFields := map[string]interface{}{
			"syncStatus":    "Succeeded",
			"lastSyncError": "",
			"interfaces":    ifaces,
		}

		Expect(daemonMgr.ApplyStatus(ctx, nodeState, statusFields, nil)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())

		Expect(updated.Status.SyncStatus).To(Equal("Succeeded"))
		Expect(updated.Status.Interfaces).To(HaveLen(1))
		Expect(updated.Status.Interfaces[0].PciAddress).To(Equal("0000:00:01.0"))
		Expect(updated.Status.Interfaces[0].NumVfs).To(Equal(4))
	})

	It("should clear lastSyncError when set to empty string", func() {
		statusFields := map[string]interface{}{
			"syncStatus":    "Failed",
			"lastSyncError": "some error",
		}
		Expect(daemonMgr.ApplyStatus(ctx, nodeState, statusFields, nil)).To(Succeed())

		fetched := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), fetched)).To(Succeed())
		Expect(fetched.Status.LastSyncError).To(Equal("some error"))

		statusFields["syncStatus"] = "Succeeded"
		statusFields["lastSyncError"] = ""
		Expect(daemonMgr.ApplyStatus(ctx, fetched, statusFields, nil)).To(Succeed())

		updated := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())
		Expect(updated.Status.SyncStatus).To(Equal("Succeeded"))
		Expect(updated.Status.LastSyncError).To(BeEmpty())
	})

	Context("mixed ownership with drain controller", func() {
		It("should not disturb drain-owned conditions when daemon applies status", func() {
			By("drain controller sets Draining condition")
			Expect(drainMgr.ApplyCondition(ctx, nodeState,
				NewCondition("Draining", metav1.ConditionTrue, "DrainingNode", "drain in progress", nodeState.Generation),
			)).To(Succeed())

			By("daemon applies status fields and its owned conditions")
			fetched := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), fetched)).To(Succeed())

			statusFields := map[string]interface{}{
				"syncStatus":    "InProgress",
				"lastSyncError": "",
			}
			daemonConditions := []metav1.Condition{
				NewCondition("Progressing", metav1.ConditionTrue, "Applying", "configuring", fetched.Generation),
				NewCondition("Ready", metav1.ConditionFalse, "NotReady", "not yet", fetched.Generation),
			}

			Expect(daemonMgr.ApplyStatus(ctx, fetched, statusFields, daemonConditions)).To(Succeed())

			By("verifying all conditions coexist")
			updated := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())

			Expect(updated.Status.SyncStatus).To(Equal("InProgress"))
			Expect(updated.Status.Conditions).To(HaveLen(3))

			condMap := make(map[string]metav1.Condition)
			for _, c := range updated.Status.Conditions {
				condMap[c.Type] = c
			}
			Expect(condMap).To(HaveKey("Draining"))
			Expect(condMap["Draining"].Status).To(Equal(metav1.ConditionTrue))
			Expect(condMap["Draining"].Message).To(Equal("drain in progress"))
			Expect(condMap).To(HaveKey("Progressing"))
			Expect(condMap["Progressing"].Status).To(Equal(metav1.ConditionTrue))
			Expect(condMap).To(HaveKey("Ready"))
			Expect(condMap["Ready"].Status).To(Equal(metav1.ConditionFalse))
		})

		It("should preserve drain conditions across repeated daemon status updates", func() {
			By("drain controller sets Draining condition")
			Expect(drainMgr.ApplyCondition(ctx, nodeState,
				NewCondition("Draining", metav1.ConditionFalse, "DrainCompleted", "drain done", nodeState.Generation),
			)).To(Succeed())

			By("daemon applies status — first time")
			fetched := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), fetched)).To(Succeed())

			statusFields := map[string]interface{}{
				"syncStatus":    "InProgress",
				"lastSyncError": "",
			}
			Expect(daemonMgr.ApplyStatus(ctx, fetched, statusFields,
				[]metav1.Condition{
					NewCondition("Progressing", metav1.ConditionTrue, "Applying", "working", fetched.Generation),
					NewCondition("Ready", metav1.ConditionFalse, "NotReady", "not yet", fetched.Generation),
				},
			)).To(Succeed())

			By("daemon applies status — second time with new status")
			fetched2 := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), fetched2)).To(Succeed())

			statusFields["syncStatus"] = "Succeeded"
			Expect(daemonMgr.ApplyStatus(ctx, fetched2, statusFields,
				[]metav1.Condition{
					NewCondition("Progressing", metav1.ConditionFalse, "Done", "finished", fetched2.Generation),
					NewCondition("Ready", metav1.ConditionTrue, "NodeReady", "ready", fetched2.Generation),
				},
			)).To(Succeed())

			By("verifying drain conditions survived both daemon updates")
			updated := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())

			Expect(updated.Status.SyncStatus).To(Equal("Succeeded"))
			Expect(updated.Status.Conditions).To(HaveLen(3))

			condMap := make(map[string]metav1.Condition)
			for _, c := range updated.Status.Conditions {
				condMap[c.Type] = c
			}
			Expect(condMap["Draining"].Status).To(Equal(metav1.ConditionFalse))
			Expect(condMap["Draining"].Message).To(Equal("drain done"))
			Expect(condMap["Progressing"].Status).To(Equal(metav1.ConditionFalse))
			Expect(condMap["Ready"].Status).To(Equal(metav1.ConditionTrue))
		})

		It("should allow drain controller to update its conditions after daemon status apply", func() {
			By("daemon applies initial status")
			statusFields := map[string]interface{}{
				"syncStatus":    "Succeeded",
				"lastSyncError": "",
			}
			Expect(daemonMgr.ApplyStatus(ctx, nodeState, statusFields,
				[]metav1.Condition{
					NewCondition("Ready", metav1.ConditionTrue, "NodeReady", "ready", nodeState.Generation),
				},
			)).To(Succeed())

			By("drain controller sets its own condition")
			fetched := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), fetched)).To(Succeed())

			Expect(drainMgr.ApplyCondition(ctx, fetched,
				NewCondition("Draining", metav1.ConditionTrue, "DrainingNode", "draining node", fetched.Generation),
			)).To(Succeed())

			By("verifying both daemon status and drain conditions are present")
			updated := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())

			Expect(updated.Status.SyncStatus).To(Equal("Succeeded"))
			Expect(updated.Status.Conditions).To(HaveLen(2))

			condMap := make(map[string]metav1.Condition)
			for _, c := range updated.Status.Conditions {
				condMap[c.Type] = c
			}
			Expect(condMap["Ready"].Status).To(Equal(metav1.ConditionTrue))
			Expect(condMap["Draining"].Status).To(Equal(metav1.ConditionTrue))
		})
	})

	It("should preserve LastTransitionTime for conditions when status hasn't changed", func() {
		oldTime := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		cond := NewCondition("Ready", metav1.ConditionTrue, "NodeReady", "ready", nodeState.Generation)
		cond.LastTransitionTime = oldTime

		statusFields := map[string]interface{}{
			"syncStatus":    "Succeeded",
			"lastSyncError": "",
		}
		Expect(daemonMgr.ApplyStatus(ctx, nodeState, statusFields, []metav1.Condition{cond})).To(Succeed())

		fetched := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), fetched)).To(Succeed())
		Expect(fetched.Status.Conditions[0].LastTransitionTime.Equal(&oldTime)).To(BeTrue())

		newCond := NewCondition("Ready", metav1.ConditionTrue, "NodeReady", "still ready", fetched.Generation)
		Expect(daemonMgr.ApplyStatus(ctx, fetched, statusFields, []metav1.Condition{newCond})).To(Succeed())

		updated := &sriovnetworkv1.SriovNetworkNodeState{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeState), updated)).To(Succeed())
		Expect(updated.Status.Conditions[0].LastTransitionTime.Equal(&oldTime)).
			To(BeTrue(), "LastTransitionTime should be preserved when condition status is unchanged")
	})
})
