/*
 * Copyright 2025 The Kubernetes Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package v1alpha1

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/consts"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovResourcePolicy defines a policy for advertising SR-IOV devices as Kubernetes resources.
// Devices matching the policy's resource filters are advertised in the ResourceSlice.
type SriovResourcePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SriovResourcePolicySpec `json:"spec"`
}

// SriovResourcePolicySpec is the spec for a SriovResourcePolicy
type SriovResourcePolicySpec struct {
	NodeSelector *corev1.NodeSelector `json:"nodeSelector,omitempty"`
	Configs      []Config             `json:"configs,omitempty"`
}

// Config pairs a device selection (ResourceFilters) with an optional set of
// extra attributes to apply (DeviceAttributesSelector). Devices matching the
// filters are advertised regardless of whether a DeviceAttributesSelector is set.
type Config struct {
	// DeviceAttributesSelector selects DeviceAttributes objects by label.
	// Attributes from all matching DeviceAttributes are merged and applied
	// to devices selected by ResourceFilters. Optional.
	DeviceAttributesSelector *metav1.LabelSelector `json:"deviceAttributesSelector,omitempty"`
	ResourceFilters          []ResourceFilter      `json:"resourceFilters,omitempty"`
}

// ResourceFilter is a filter for a resource
type ResourceFilter struct {
	Vendors        []string `json:"vendors,omitempty"`
	Devices        []string `json:"devices,omitempty"`
	PciAddresses   []string `json:"pciAddresses,omitempty"`
	PfNames        []string `json:"pfNames,omitempty"`
	PfPciAddresses []string `json:"pfPciAddresses,omitempty"`
	Drivers        []string `json:"drivers,omitempty"`
	// +kubebuilder:validation:Enum=eth;ib;ethernet;infiniband
	// NIC Link Type. Accepted values: "eth", "ib", "ethernet", "infiniband".
	LinkType string `json:"linkType,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovResourcePolicyList contains a list of SriovResourcePolicy
type SriovResourcePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovResourcePolicy `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DeviceAttributes defines a set of arbitrary attributes that can be applied
// to devices selected by a SriovResourcePolicy. Policies reference
// DeviceAttributes objects via label selectors.
type DeviceAttributes struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DeviceAttributesSpec `json:"spec"`
}

// DeviceAttributesSpec holds the attributes to apply to devices.
type DeviceAttributesSpec struct {
	// Attributes is a map of qualified attribute name to value.
	// Keys should be fully qualified (e.g. "k8s.cni.cncf.io/resourceName").
	Attributes map[resourceapi.QualifiedName]resourceapi.DeviceAttribute `json:"attributes,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DeviceAttributesList contains a list of DeviceAttributes
type DeviceAttributesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeviceAttributes `json:"items"`
}

// NormalizeLinkType normalizes a link type string to a canonical form.
// "eth" and "ethernet" (case-insensitive) map to consts.LinkTypeEthernet,
// "ib" and "infiniband" (case-insensitive) map to consts.LinkTypeInfiniband.
func NormalizeLinkType(val string) string {
	switch strings.ToLower(val) {
	case consts.LinkTypeEth, consts.LinkTypeEthernet:
		return consts.LinkTypeEthernet
	case consts.LinkTypeIB, consts.LinkTypeInfiniband:
		return consts.LinkTypeInfiniband
	default:
		return strings.ToLower(val)
	}
}
