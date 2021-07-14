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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovIBNetworkSpec defines the desired state of SriovIBNetwork
type SriovIBNetworkSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Namespace of the NetworkAttachmentDefinition custom resource
	NetworkNamespace string `json:"networkNamespace,omitempty"`
	// SRIOV Network device plugin endpoint resource name
	ResourceName string `json:"resourceName"`
	//Capabilities to be configured for this network.
	//Capabilities supported: (infinibandGUID), e.g. '{"infinibandGUID": true}'
	Capabilities string `json:"capabilities,omitempty"`
	//IPAM configuration to be used for this network.
	IPAM string `json:"ipam,omitempty"`
	// VF link state (enable|disable|auto)
	// +kubebuilder:validation:Enum={"auto","enable","disable"}
	LinkState string `json:"linkState,omitempty"`
	// MetaPluginsConfig configuration to be used in order to chain metaplugins to the sriov interface returned
	// by the operator.
	MetaPluginsConfig string `json:"metaPlugins,omitempty"`
}

// SriovIBNetworkStatus defines the observed state of SriovIBNetwork
type SriovIBNetworkStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SriovIBNetwork is the Schema for the sriovibnetworks API
type SriovIBNetwork struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovIBNetworkSpec   `json:"spec,omitempty"`
	Status SriovIBNetworkStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SriovIBNetworkList contains a list of SriovIBNetwork
type SriovIBNetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovIBNetwork `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovIBNetwork{}, &SriovIBNetworkList{})
}
