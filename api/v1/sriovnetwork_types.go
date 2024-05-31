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

// SriovNetworkSpec defines the desired state of SriovNetwork
type SriovNetworkSpec struct {
	// Namespace of the NetworkAttachmentDefinition custom resource
	NetworkNamespace string `json:"networkNamespace,omitempty"`
	// SRIOV Network device plugin endpoint resource name
	ResourceName string `json:"resourceName"`
	//Capabilities to be configured for this network.
	//Capabilities supported: (mac|ips), e.g. '{"mac": true}'
	Capabilities string `json:"capabilities,omitempty"`
	//IPAM configuration to be used for this network.
	IPAM string `json:"ipam,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4096
	// VLAN ID to assign for the VF. Defaults to 0.
	Vlan int `json:"vlan,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	// VLAN QoS ID to assign for the VF. Defaults to 0.
	VlanQoS int `json:"vlanQoS,omitempty"`
	// +kubebuilder:validation:Enum={"802.1q","802.1Q", "802.1ad", "802.1AD"}
	// VLAN proto to assign for the VF. Defaults to 802.1q.
	VlanProto string `json:"vlanProto,omitempty"`
	// VF spoof check, (on|off)
	// +kubebuilder:validation:Enum={"on","off"}
	SpoofChk string `json:"spoofChk,omitempty"`
	// VF trust mode (on|off)
	// +kubebuilder:validation:Enum={"on","off"}
	Trust string `json:"trust,omitempty"`
	// VF link state (enable|disable|auto)
	// +kubebuilder:validation:Enum={"auto","enable","disable"}
	LinkState string `json:"linkState,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// Minimum tx rate, in Mbps, for the VF. Defaults to 0 (no rate limiting). min_tx_rate should be <= max_tx_rate.
	MinTxRate *int `json:"minTxRate,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// Maximum tx rate, in Mbps, for the VF. Defaults to 0 (no rate limiting)
	MaxTxRate *int `json:"maxTxRate,omitempty"`
	// MetaPluginsConfig configuration to be used in order to chain metaplugins to the sriov interface returned
	// by the operator.
	MetaPluginsConfig string `json:"metaPlugins,omitempty"`
	// LogLevel sets the log level of the SRIOV CNI plugin - either of panic, error, warning, info, debug. Defaults
	// to info if left blank.
	// +kubebuilder:validation:Enum={"panic", "error","warning","info","debug",""}
	// +kubebuilder:default:= "info"
	LogLevel string `json:"logLevel,omitempty"`
	// LogFile sets the log file of the SRIOV CNI plugin logs. If unset (default), this will log to stderr and thus
	// to multus and container runtime logs.
	LogFile string `json:"logFile,omitempty"`
}

// SriovNetworkStatus defines the observed state of SriovNetwork
type SriovNetworkStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SriovNetwork is the Schema for the sriovnetworks API
type SriovNetwork struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovNetworkSpec   `json:"spec,omitempty"`
	Status SriovNetworkStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SriovNetworkList contains a list of SriovNetwork
type SriovNetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovNetwork `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovNetwork{}, &SriovNetworkList{})
}
