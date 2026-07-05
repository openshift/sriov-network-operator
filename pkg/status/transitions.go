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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Event type constants
const (
	EventTypeNormal  = "Normal"
	EventTypeWarning = "Warning"
)

// TransitionType represents the type of condition transition
type TransitionType string

const (
	// TransitionAdded indicates a condition was added
	TransitionAdded TransitionType = "Added"
	// TransitionChanged indicates a condition's status changed
	TransitionChanged TransitionType = "Changed"
	// TransitionRemoved indicates a condition was removed
	TransitionRemoved TransitionType = "Removed"
)

// Transition represents a change in a condition
type Transition struct {
	Type          TransitionType
	ConditionType string
	OldStatus     metav1.ConditionStatus
	NewStatus     metav1.ConditionStatus
	Reason        string
	Message       string
}

// EventType returns the Kubernetes event type for this transition
func (t *Transition) EventType() string {
	// TODO: for now we put everything in normal events, in the future we can differentiate between warnings and normal events
	return EventTypeNormal
}

// EventReason returns a suitable event reason for this transition
func (t *Transition) EventReason() string {
	return t.ConditionType + string(t.Type)
}

// DetectTransitions compares old and new conditions and returns a list of transitions
func DetectTransitions(oldConditions, newConditions []metav1.Condition) []Transition {
	transitions := []Transition{}

	// Build map of old conditions for quick lookup
	oldMap := make(map[string]metav1.Condition)
	for _, cond := range oldConditions {
		oldMap[cond.Type] = cond
	}

	// Check for added or changed conditions
	for _, newCond := range newConditions {
		oldCond, exists := oldMap[newCond.Type]
		if !exists {
			transitions = append(transitions, Transition{
				Type:          TransitionAdded,
				ConditionType: newCond.Type,
				NewStatus:     newCond.Status,
				Reason:        newCond.Reason,
				Message:       newCond.Message,
			})
		} else if oldCond.Status != newCond.Status ||
			oldCond.Reason != newCond.Reason ||
			oldCond.Message != newCond.Message ||
			oldCond.ObservedGeneration != newCond.ObservedGeneration {
			transitions = append(transitions, Transition{
				Type:          TransitionChanged,
				ConditionType: newCond.Type,
				OldStatus:     oldCond.Status,
				NewStatus:     newCond.Status,
				Reason:        newCond.Reason,
				Message:       newCond.Message,
			})
		}
		// Mark as processed
		delete(oldMap, newCond.Type)
	}

	// Check for removed conditions
	for _, oldCond := range oldMap {
		transitions = append(transitions, Transition{
			Type:          TransitionRemoved,
			ConditionType: oldCond.Type,
			OldStatus:     oldCond.Status,
		})
	}

	return transitions
}
