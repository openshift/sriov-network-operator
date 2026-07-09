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
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Interface provides methods for applying resource status using Server-Side Apply
// and emitting events for condition transitions.
type Interface interface {
	// ApplyCondition applies one or more conditions to the status subresource
	// using Server-Side Apply. Only the specified conditions are included in the
	// apply configuration, so the field manager only claims ownership of those
	// specific condition entries (keyed by type). Other conditions owned by
	// different field managers are left untouched.
	ApplyCondition(ctx context.Context, obj client.Object, conditions ...metav1.Condition) error
}

// Patcher implements Interface using Server-Side Apply for status updates.
type Patcher struct {
	client       client.Client
	recorder     events.EventRecorder
	scheme       *runtime.Scheme
	fieldManager string
}

// NewPatcher creates a new Patcher that uses Server-Side Apply with the given field manager identity.
func NewPatcher(c client.Client, recorder events.EventRecorder, scheme *runtime.Scheme, fieldManager string) Interface {
	return &Patcher{
		client:       c,
		recorder:     recorder,
		scheme:       scheme,
		fieldManager: fieldManager,
	}
}

// ApplyCondition applies one or more conditions to the status subresource using
// Server-Side Apply. The apply configuration only includes the provided conditions,
// so with the CRD's +listType=map/+listMapKey=type markers, SSA merges each condition
// entry independently by its type key. This means different field managers can own
// different condition types without conflicting.
func (p *Patcher) ApplyCondition(ctx context.Context, obj client.Object, conditions ...metav1.Condition) error {
	if len(conditions) == 0 {
		return nil
	}

	oldConditions := getConditionsFromObject(obj)

	// Per Kubernetes conventions, LastTransitionTime must only change when
	// Status transitions. Preserve it from the old condition when Status
	// is the same (even if Reason or Message changed).
	oldMap := make(map[string]metav1.Condition, len(oldConditions))
	for _, c := range oldConditions {
		oldMap[c.Type] = c
	}
	for i := range conditions {
		if old, exists := oldMap[conditions[i].Type]; exists && old.Status == conditions[i].Status {
			conditions[i].LastTransitionTime = old.LastTransitionTime
		}
	}

	transitions := DetectTransitions(oldConditions, conditions)

	// Nothing changed — skip the API call entirely
	if len(transitions) == 0 {
		return nil
	}

	ac, err := p.buildConditionApplyConfiguration(obj, conditions)
	if err != nil {
		return fmt.Errorf("failed to build condition apply configuration: %w", err)
	}

	if err := p.client.Status().Apply(ctx, ac,
		client.FieldOwner(p.fieldManager),
		client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to apply status condition: %w", err)
	}

	if p.recorder != nil {
		for _, transition := range transitions {
			p.recorder.Eventf(obj, nil, transition.EventType(), transition.EventReason(), "StatusChange", "%s", transition.Message)
		}
	}

	return nil
}

// getConditionsFromObject extracts the current conditions from the object
// if it implements a GetConditions method.
func getConditionsFromObject(obj client.Object) []metav1.Condition {
	type conditionAccessor interface {
		GetConditions() []metav1.Condition
	}
	if accessor, ok := obj.(conditionAccessor); ok {
		return accessor.GetConditions()
	}
	return nil
}

// buildConditionApplyConfiguration constructs a minimal unstructured apply configuration
// containing only the object identity and the specific conditions being applied.
func (p *Patcher) buildConditionApplyConfiguration(obj client.Object, conditions []metav1.Condition) (runtime.ApplyConfiguration, error) {
	gvks, _, err := p.scheme.ObjectKinds(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get GVK for object: %w", err)
	}
	gvk := gvks[0]

	conditionsData, err := json.Marshal(conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal conditions: %w", err)
	}

	var conditionsSlice []interface{}
	if err := json.Unmarshal(conditionsData, &conditionsSlice); err != nil {
		return nil, fmt.Errorf("failed to unmarshal conditions: %w", err)
	}

	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": gvk.GroupVersion().String(),
			"kind":       gvk.Kind,
			"metadata": map[string]interface{}{
				"name":      obj.GetName(),
				"namespace": obj.GetNamespace(),
			},
			"status": map[string]interface{}{
				"conditions": conditionsSlice,
			},
		},
	}

	return client.ApplyConfigurationFromUnstructured(u), nil
}

// NewCondition creates a new metav1.Condition with the given parameters and
// LastTransitionTime set to the current time.
func NewCondition(conditionType string, s metav1.ConditionStatus, reason, message string, generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             s,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
	}
}
