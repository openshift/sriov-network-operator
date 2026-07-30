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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types used across SR-IOV Network Operator CRDs
const (
	// ConditionReady indicates that the resource has reached its desired state and is fully functional
	ConditionReady = "Ready"
)

// Common condition reasons used across SR-IOV Network Operator CRDs
const (
	// Reasons for Network Ready condition
	ReasonNetworkReady = "NetworkReady"

	// Reasons for Network NotReady conditions
	ReasonNetworkAttachmentDefNotFound = "NetworkAttachmentDefinitionNotFound"
	ReasonNetworkAttachmentDefInvalid  = "NetworkAttachmentDefinitionInvalid"
	ReasonNamespaceNotFound            = "NamespaceNotFound"

	// Reasons for Operator Config Ready condition
	ReasonOperatorConfigReady = "OperatorConfigReady"

	// Reasons for Operator Config NotReady conditions
	ReasonOperatorConfigSyncFailed = "OperatorConfigSyncFailed"
	ReasonUnsupportedConfiguration = "UnsupportedConfiguration"
)

// ConditionStatus defines the common observed state for SR-IOV operator CRDs
type ConditionStatus struct {
	// Conditions represent the latest available observations of the resource's state
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
