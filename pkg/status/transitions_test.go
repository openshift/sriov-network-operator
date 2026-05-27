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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Transitions", func() {
	Describe("DetectTransitions", func() {
		It("should detect added conditions", func() {
			oldConditions := []metav1.Condition{}
			newConditions := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "TestReason", Message: "Test message"},
			}

			transitions := DetectTransitions(oldConditions, newConditions)
			Expect(transitions).To(HaveLen(1))
			Expect(transitions[0].Type).To(Equal(TransitionAdded))
			Expect(transitions[0].ConditionType).To(Equal("Ready"))
			Expect(transitions[0].NewStatus).To(Equal(metav1.ConditionTrue))
		})

		It("should detect changed conditions", func() {
			oldConditions := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse, Reason: "OldReason", Message: "Old message"},
			}
			newConditions := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "NewReason", Message: "New message"},
			}

			transitions := DetectTransitions(oldConditions, newConditions)
			Expect(transitions).To(HaveLen(1))
			Expect(transitions[0].Type).To(Equal(TransitionChanged))
			Expect(transitions[0].ConditionType).To(Equal("Ready"))
			Expect(transitions[0].OldStatus).To(Equal(metav1.ConditionFalse))
			Expect(transitions[0].NewStatus).To(Equal(metav1.ConditionTrue))
		})

		It("should detect removed conditions", func() {
			oldConditions := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "TestReason", Message: "Test message"},
			}
			newConditions := []metav1.Condition{}

			transitions := DetectTransitions(oldConditions, newConditions)
			Expect(transitions).To(HaveLen(1))
			Expect(transitions[0].Type).To(Equal(TransitionRemoved))
			Expect(transitions[0].ConditionType).To(Equal("Ready"))
		})

		It("should not detect unchanged conditions", func() {
			conditions := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "TestReason", Message: "Test message"},
			}

			transitions := DetectTransitions(conditions, conditions)
			Expect(transitions).To(BeEmpty())
		})

		It("should detect multiple transitions", func() {
			oldConditions := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse, Reason: "OldReason", Message: "Old"},
				{Type: "Degraded", Status: metav1.ConditionTrue, Reason: "Error", Message: "Error message"},
			}
			newConditions := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "NewReason", Message: "New"},
				{Type: "Progressing", Status: metav1.ConditionTrue, Reason: "Applying", Message: "Applying changes"},
			}

			transitions := DetectTransitions(oldConditions, newConditions)
			Expect(transitions).To(HaveLen(3)) // Ready changed, Progressing added, Degraded removed
		})
	})

	Describe("Transition EventType", func() {
		It("should return Normal for new True condition", func() {
			transition := Transition{
				Type:      TransitionAdded,
				NewStatus: metav1.ConditionTrue,
			}
			Expect(transition.EventType()).To(Equal("Normal"))
		})

		It("should return Normal for new False condition", func() {
			transition := Transition{
				Type:      TransitionAdded,
				NewStatus: metav1.ConditionFalse,
			}
			Expect(transition.EventType()).To(Equal("Normal"))
		})

		It("should return Normal for transition to True", func() {
			transition := Transition{
				Type:      TransitionChanged,
				OldStatus: metav1.ConditionFalse,
				NewStatus: metav1.ConditionTrue,
			}
			Expect(transition.EventType()).To(Equal("Normal"))
		})

		It("should return Normal for transition from True to False", func() {
			transition := Transition{
				Type:      TransitionChanged,
				OldStatus: metav1.ConditionTrue,
				NewStatus: metav1.ConditionFalse,
			}
			Expect(transition.EventType()).To(Equal("Normal"))
		})
	})

	Describe("Transition EventReason", func() {
		It("should combine condition type and transition type", func() {
			transition := Transition{
				Type:          TransitionChanged,
				ConditionType: "Ready",
			}
			Expect(transition.EventReason()).To(Equal("ReadyChanged"))
		})
	})
})
