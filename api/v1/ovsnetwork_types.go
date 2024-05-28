/*
Copyright 2024.

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

// OVSNetworkSpec defines the desired state of OVSNetwork
type OVSNetworkSpec struct {
	// Namespace of the NetworkAttachmentDefinition custom resource
	NetworkNamespace string `json:"networkNamespace,omitempty"`
	// OVS Network device plugin endpoint resource name
	ResourceName string `json:"resourceName"`
	// Capabilities to be configured for this network.
	// Capabilities supported: (mac|ips), e.g. '{"mac": true}'
	Capabilities string `json:"capabilities,omitempty"`
	// IPAM configuration to be used for this network.
	IPAM string `json:"ipam,omitempty"`
	// MetaPluginsConfig configuration to be used in order to chain metaplugins
	MetaPluginsConfig string `json:"metaPlugins,omitempty"`
	// name of the OVS bridge, if not set OVS will automatically select bridge
	// based on VF PCI address
	Bridge string `json:"bridge,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4095
	// Vlan to assign for the OVS port
	Vlan uint `json:"vlan,omitempty"`
	// Mtu for the OVS port
	MTU uint `json:"mtu,omitempty"`
	// Trunk configuration for the OVS port
	Trunk []*TrunkConfig `json:"trunk,omitempty"`
	// The type of interface on ovs.
	InterfaceType string `json:"interfaceType,omitempty"`
}

// TrunkConfig contains configuration for bridge trunk
type TrunkConfig struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4095
	MinID *uint `json:"minID,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4095
	MaxID *uint `json:"maxID,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4095
	ID *uint `json:"id,omitempty"`
}

// OVSNetworkStatus defines the observed state of OVSNetwork
type OVSNetworkStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// OVSNetwork is the Schema for the ovsnetworks API
type OVSNetwork struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OVSNetworkSpec   `json:"spec,omitempty"`
	Status OVSNetworkStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// OVSNetworkList contains a list of OVSNetwork
type OVSNetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OVSNetwork `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OVSNetwork{}, &OVSNetworkList{})
}
