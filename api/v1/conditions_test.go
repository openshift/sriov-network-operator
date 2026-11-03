/*
Copyright 2021.

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

package v1_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

var _ = Describe("ConditionStatus", func() {
	It("should have empty conditions by default", func() {
		status := v1.ConditionStatus{}
		Expect(status.Conditions).To(BeNil())
	})

	It("should store conditions", func() {
		status := v1.ConditionStatus{
			Conditions: []metav1.Condition{
				{
					Type:    v1.ConditionReady,
					Status:  metav1.ConditionTrue,
					Reason:  v1.ReasonNetworkReady,
					Message: "ready",
				},
			},
		}
		Expect(status.Conditions).To(HaveLen(1))
		Expect(status.Conditions[0].Type).To(Equal(v1.ConditionReady))
		Expect(status.Conditions[0].Reason).To(Equal(v1.ReasonNetworkReady))
	})
})

var _ = Describe("SetNodeStateConfigurationConditions", func() {
	var nodeState *v1.SriovNetworkNodeState

	BeforeEach(func() {
		nodeState = &v1.SriovNetworkNodeState{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-node",
				Namespace:  "test-ns",
				Generation: 5,
			},
		}
	})

	Context("when SyncStatus is InProgress", func() {
		It("should set Progressing=True and Ready=False", func() {
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusInProgress, "", false)

			progressing := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionProgressing)
			Expect(progressing).ToNot(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(v1.ReasonApplyingConfiguration))
			Expect(progressing.ObservedGeneration).To(Equal(int64(5)))

			ready := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionReady)
			Expect(ready).ToNot(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(v1.ReasonNotReady))
			Expect(meta.FindStatusCondition(nodeState.Status.Conditions, "Degraded")).To(BeNil())
		})
	})

	Context("when SyncStatus is Succeeded", func() {
		It("should set Progressing=False and Ready=True", func() {
			nodeState.Generation = 10
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)

			progressing := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionProgressing)
			Expect(progressing).ToNot(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))
			Expect(progressing.Reason).To(Equal(v1.ReasonNotProgressing))

			ready := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionReady)
			Expect(ready).ToNot(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			Expect(ready.Reason).To(Equal(v1.ReasonNodeReady))
			Expect(meta.FindStatusCondition(nodeState.Status.Conditions, "Degraded")).To(BeNil())
		})
	})

	Context("when SyncStatus is Failed", func() {
		It("should set Progressing=False and Ready=False with error message", func() {
			nodeState.Generation = 7
			errorMsg := "driver load failed"
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusFailed, errorMsg, false)

			progressing := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionProgressing)
			Expect(progressing).ToNot(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))

			ready := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionReady)
			Expect(ready).ToNot(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Message).To(Equal("Node configuration failed: " + errorMsg))
			Expect(meta.FindStatusCondition(nodeState.Status.Conditions, "Degraded")).To(BeNil())
		})
	})

	Context("when SyncStatus is InProgress but configuration is waiting for drain", func() {
		It("should set Progressing=True and Ready=False with waiting-for-drain reasons", func() {
			nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusInProgress, "", true)

			progressing := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionProgressing)
			Expect(progressing).ToNot(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(v1.ReasonWaitingForDrain))
			Expect(progressing.Message).To(ContainSubstring("Waiting for node drain"))

			ready := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionReady)
			Expect(ready).ToNot(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(v1.ReasonWaitingForDrain))
			Expect(ready.Message).To(ContainSubstring("Waiting for node drain"))
		})
	})
})

var _ = Describe("SetDrainConditions", func() {
	var nodeState *v1.SriovNetworkNodeState

	BeforeEach(func() {
		nodeState = &v1.SriovNetworkNodeState{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-node",
				Namespace:  "test-ns",
				Generation: 3,
			},
		}
	})

	Context("when DrainState is Idle", func() {
		It("should set Draining=False with DrainNotNeeded reason", func() {
			nodeState.SetNodeStateDrainConditions(v1.DrainStateIdle, "")

			draining := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
			Expect(draining).ToNot(BeNil())
			Expect(draining.Status).To(Equal(metav1.ConditionFalse))
			Expect(draining.Reason).To(Equal(v1.ReasonDrainNotNeeded))
		})
	})

	Context("when DrainState is Draining", func() {
		It("should set Draining=True with DrainingNode reason", func() {
			nodeState.Generation = 4
			nodeState.SetNodeStateDrainConditions(v1.DrainStateDraining, "")

			draining := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
			Expect(draining).ToNot(BeNil())
			Expect(draining.Status).To(Equal(metav1.ConditionTrue))
			Expect(draining.Reason).To(Equal(v1.ReasonDrainingNode))
		})
	})

	Context("when DrainState is Pending", func() {
		It("should set Draining=True with DrainPending reason", func() {
			nodeState.Generation = 5
			nodeState.SetNodeStateDrainConditions(v1.DrainStatePending, "Waiting for an available drain slot")

			draining := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
			Expect(draining).ToNot(BeNil())
			Expect(draining.Status).To(Equal(metav1.ConditionTrue))
			Expect(draining.Reason).To(Equal(v1.ReasonDrainPending))
			Expect(draining.Message).To(Equal("Waiting for an available drain slot"))
		})
	})

	Context("when DrainState is DrainingWithErrors", func() {
		It("should set Draining=True with DrainFailed reason and error message", func() {
			nodeState.Generation = 6
			errorMsg := "Cannot evict pod as it would violate the pod's disruption budget"
			nodeState.SetNodeStateDrainConditions(v1.DrainStateDrainingWithErrors, errorMsg)

			draining := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
			Expect(draining).ToNot(BeNil())
			Expect(draining.Status).To(Equal(metav1.ConditionTrue))
			Expect(draining.Reason).To(Equal(v1.ReasonDrainFailed))
			Expect(draining.Message).To(Equal("Node drain encountered errors: " + errorMsg))
		})
	})

	Context("when DrainState is Complete", func() {
		It("should set Draining=False with DrainCompleted reason", func() {
			nodeState.Generation = 8
			nodeState.SetNodeStateDrainConditions(v1.DrainStateComplete, "")

			draining := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
			Expect(draining).ToNot(BeNil())
			Expect(draining.Status).To(Equal(metav1.ConditionFalse))
			Expect(draining.Reason).To(Equal(v1.ReasonDrainCompleted))
		})
	})
})

var _ = Describe("ConditionsEqual", func() {
	DescribeTable("comparing conditions",
		func(a, b []metav1.Condition, expected bool) {
			result := v1.ConditionsEqual(a, b)
			Expect(result).To(Equal(expected))
		},
		Entry("both empty",
			[]metav1.Condition{},
			[]metav1.Condition{},
			true,
		),
		Entry("different length",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue},
			},
			[]metav1.Condition{},
			false,
		),
		Entry("same conditions",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			true,
		),
		Entry("different status",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionFalse, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			false,
		),
		Entry("different reason",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNotReady, Message: "ready", ObservedGeneration: 1},
			},
			false,
		),
		Entry("different message",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "not ready", ObservedGeneration: 1},
			},
			false,
		),
		Entry("different observedGeneration",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 2},
			},
			false,
		),
		Entry("different LastTransitionTime should still be equal",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
			},
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
			},
			true,
		),
		Entry("multiple conditions same",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
				{Type: v1.ConditionProgressing, Status: metav1.ConditionFalse, Reason: v1.ReasonNotProgressing, Message: "not progressing", ObservedGeneration: 1},
			},
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
				{Type: v1.ConditionProgressing, Status: metav1.ConditionFalse, Reason: v1.ReasonNotProgressing, Message: "not progressing", ObservedGeneration: 1},
			},
			true,
		),
		Entry("condition type not found in second slice",
			[]metav1.Condition{
				{Type: v1.ConditionReady, Status: metav1.ConditionTrue, Reason: v1.ReasonNodeReady, Message: "ready", ObservedGeneration: 1},
			},
			[]metav1.Condition{
				{Type: v1.ConditionProgressing, Status: metav1.ConditionFalse, Reason: v1.ReasonNotProgressing, Message: "not progressing", ObservedGeneration: 1},
			},
			false,
		),
	)
})

var _ = Describe("Conditions isolation", func() {
	It("should preserve drain conditions when setting configuration conditions", func() {
		nodeState := &v1.SriovNetworkNodeState{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-node",
				Namespace:  "test-ns",
				Generation: 5,
			},
		}

		nodeState.SetNodeStateDrainConditions(v1.DrainStateDraining, "")

		draining := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
		Expect(draining).ToNot(BeNil())
		Expect(draining.Status).To(Equal(metav1.ConditionTrue))

		nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusInProgress, "", false)

		progressing := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionProgressing)
		Expect(progressing).ToNot(BeNil())
		Expect(progressing.Status).To(Equal(metav1.ConditionTrue))

		draining = meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
		Expect(draining).ToNot(BeNil())
		Expect(draining.Status).To(Equal(metav1.ConditionTrue))
	})

	It("should preserve configuration conditions when setting drain conditions", func() {
		nodeState := &v1.SriovNetworkNodeState{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-node",
				Namespace:  "test-ns",
				Generation: 5,
			},
		}

		nodeState.SetNodeStateConfigurationConditions(consts.SyncStatusSucceeded, "", false)

		ready := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionReady)
		Expect(ready).ToNot(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))

		nodeState.SetNodeStateDrainConditions(v1.DrainStateDraining, "")

		draining := meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionDraining)
		Expect(draining).ToNot(BeNil())
		Expect(draining.Status).To(Equal(metav1.ConditionTrue))

		ready = meta.FindStatusCondition(nodeState.Status.Conditions, v1.ConditionReady)
		Expect(ready).ToNot(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
	})
})

var _ = Describe("SriovNetworkNodeState IsReady", func() {
	DescribeTable("requires a true Ready condition for the current generation",
		func(conditions []metav1.Condition, expected bool) {
			state := &v1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Generation: 5},
				Status:     v1.SriovNetworkNodeStateStatus{Conditions: conditions},
			}
			Expect(state.IsReady()).To(Equal(expected))
		},
		Entry("missing Ready", []metav1.Condition{{Type: v1.ConditionProgressing, Status: metav1.ConditionTrue, ObservedGeneration: 5}}, false),
		Entry("false Ready", []metav1.Condition{{Type: v1.ConditionReady, Status: metav1.ConditionFalse, ObservedGeneration: 5}}, false),
		Entry("unknown Ready", []metav1.Condition{{Type: v1.ConditionReady, Status: metav1.ConditionUnknown, ObservedGeneration: 5}}, false),
		Entry("stale Ready", []metav1.Condition{{Type: v1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 4}}, false),
		Entry("current Ready", []metav1.Condition{{Type: v1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 5}}, true),
	)
})

var _ = Describe("SriovNetworkNodeState FailureReason", func() {
	DescribeTable("uses current conditions before sync status",
		func(conditions []metav1.Condition, syncStatus, expected string) {
			state := &v1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Generation: 5},
				Status:     v1.SriovNetworkNodeStateStatus{Conditions: conditions, SyncStatus: syncStatus},
			}
			Expect(state.FailureReason()).To(Equal(expected))
		},
		Entry("missing Ready", nil, consts.SyncStatusFailed, v1.ReasonApplyingConfiguration),
		Entry("stale Ready", []metav1.Condition{{Type: v1.ConditionReady, ObservedGeneration: 4, Reason: v1.ReasonWaitingForDrain}}, consts.SyncStatusFailed, v1.ReasonApplyingConfiguration),
		Entry("waiting for drain takes precedence", []metav1.Condition{{Type: v1.ConditionReady, ObservedGeneration: 5, Reason: v1.ReasonWaitingForDrain}}, consts.SyncStatusFailed, v1.ReasonWaitingForDrain),
		Entry("sync failed", []metav1.Condition{{Type: v1.ConditionReady, ObservedGeneration: 5}}, consts.SyncStatusFailed, v1.ReasonNodeSyncFailed),
		Entry("configuration in progress", []metav1.Condition{{Type: v1.ConditionReady, ObservedGeneration: 5}}, consts.SyncStatusInProgress, v1.ReasonApplyingConfiguration),
	)
})

var _ = Describe("SriovNetworkNodeState AffectsAggregate", func() {
	var source *v1.SriovNetworkNodeState

	BeforeEach(func() {
		source = &v1.SriovNetworkNodeState{
			ObjectMeta: metav1.ObjectMeta{Generation: 5, Annotations: map[string]string{
				consts.NodeStatePolicyRevisionsAnnotation: "policy-revision",
				consts.PoolRevisionAnnotation:             "pool-revision",
			}},
			Status: v1.SriovNetworkNodeStateStatus{
				SyncStatus: consts.SyncStatusSucceeded,
				Interfaces: v1.InterfaceExts{
					{Name: "ens1", PciAddress: "0000:01:00.0", AltNames: []string{"pf0", "alt0"},
						VFs: []v1.VirtualFunction{{VfID: 1}, {VfID: 0}}},
					{Name: "ens2", PciAddress: "0000:02:00.0"},
				},
				Conditions: []metav1.Condition{{Type: v1.ConditionReady, Status: metav1.ConditionTrue,
					Reason: v1.ReasonNodeReady, ObservedGeneration: 5}},
			},
		}
	})

	DescribeTable("detects aggregate changes without changing either object",
		func(change func(*v1.SriovNetworkNodeState), expected bool) {
			dest := source.DeepCopy()
			change(dest)
			originalSource, originalDest := source.DeepCopy(), dest.DeepCopy()
			Expect(source.AffectsAggregate(dest)).To(Equal(expected))
			Expect(dest.AffectsAggregate(source)).To(Equal(expected))
			Expect(source).To(Equal(originalSource))
			Expect(dest).To(Equal(originalDest))
		},
		Entry("unchanged", func(*v1.SriovNetworkNodeState) {}, false),
		Entry("generation changed", func(s *v1.SriovNetworkNodeState) { s.Generation++ }, true),
		Entry("sync status changed", func(s *v1.SriovNetworkNodeState) { s.Status.SyncStatus = consts.SyncStatusFailed }, true),
		Entry("policy revision changed", func(s *v1.SriovNetworkNodeState) { s.Annotations[consts.NodeStatePolicyRevisionsAnnotation] = "new" }, true),
		Entry("pool revision removed", func(s *v1.SriovNetworkNodeState) { delete(s.Annotations, consts.PoolRevisionAnnotation) }, true),
		Entry("unrelated annotation changed", func(s *v1.SriovNetworkNodeState) { s.Annotations["unrelated"] = "new" }, false),
		Entry("resource version changed", func(s *v1.SriovNetworkNodeState) { s.ResourceVersion = "new" }, false),
		Entry("interface added", func(s *v1.SriovNetworkNodeState) {
			s.Status.Interfaces = append(s.Status.Interfaces, v1.InterfaceExt{PciAddress: "0000:03:00.0"})
		}, true),
		Entry("interfaces removed", func(s *v1.SriovNetworkNodeState) { s.Status.Interfaces = nil }, true),
		Entry("interface field changed", func(s *v1.SriovNetworkNodeState) { s.Status.Interfaces[0].Mtu = 9000 }, true),
		Entry("VF field changed", func(s *v1.SriovNetworkNodeState) { s.Status.Interfaces[0].VFs[0].Driver = "vfio-pci" }, true),
		Entry("alternate name changed", func(s *v1.SriovNetworkNodeState) { s.Status.Interfaces[0].AltNames[0] = "new" }, true),
		Entry("interface order changed", func(s *v1.SriovNetworkNodeState) {
			s.Status.Interfaces[0], s.Status.Interfaces[1] = s.Status.Interfaces[1], s.Status.Interfaces[0]
		}, false),
		Entry("VF order changed", func(s *v1.SriovNetworkNodeState) { vfs := s.Status.Interfaces[0].VFs; vfs[0], vfs[1] = vfs[1], vfs[0] }, false),
		Entry("alternate name order changed", func(s *v1.SriovNetworkNodeState) {
			names := s.Status.Interfaces[0].AltNames
			names[0], names[1] = names[1], names[0]
		}, false),
		Entry("nil slices become empty", func(s *v1.SriovNetworkNodeState) {
			s.Status.Interfaces[1].AltNames = []string{}
			s.Status.Interfaces[1].VFs = []v1.VirtualFunction{}
		}, false),
		Entry("Ready removed", func(s *v1.SriovNetworkNodeState) { s.Status.Conditions = nil }, true),
		Entry("Ready status changed", func(s *v1.SriovNetworkNodeState) { s.Status.Conditions[0].Status = metav1.ConditionFalse }, true),
		Entry("Ready reason changed", func(s *v1.SriovNetworkNodeState) { s.Status.Conditions[0].Reason = v1.ReasonWaitingForDrain }, true),
		Entry("Ready observed generation changed", func(s *v1.SriovNetworkNodeState) { s.Status.Conditions[0].ObservedGeneration-- }, true),
		Entry("Ready message changed", func(s *v1.SriovNetworkNodeState) { s.Status.Conditions[0].Message = "new" }, false),
		Entry("Ready transition time changed", func(s *v1.SriovNetworkNodeState) { s.Status.Conditions[0].LastTransitionTime = metav1.Now() }, false),
		Entry("unrelated condition added", func(s *v1.SriovNetworkNodeState) {
			s.Status.Conditions = append(s.Status.Conditions, metav1.Condition{Type: v1.ConditionDraining, Status: metav1.ConditionTrue})
		}, false),
	)

	It("ignores condition order and missing Ready on both objects", func() {
		dest := source.DeepCopy()
		dest.Status.Conditions = append([]metav1.Condition{{Type: v1.ConditionDraining}}, dest.Status.Conditions...)
		Expect(source.AffectsAggregate(dest)).To(BeFalse())
		source.Status.Conditions = nil
		dest.Status.Conditions = []metav1.Condition{{Type: v1.ConditionDraining}}
		Expect(source.AffectsAggregate(dest)).To(BeFalse())
	})
})
