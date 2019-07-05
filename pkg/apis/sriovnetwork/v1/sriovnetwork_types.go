package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovNetworkSpec defines the desired state of SriovNetwork
// +k8s:openapi-gen=true
type SriovNetworkSpec struct {
	NetworkNamespace string `json:"networkNamespace,omitempty"`
	ResourceName     string `json:"resourceName"`
	IPAM             string `json:"ipam,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4096
	Vlan             int    `json:"vlan,omitempty"`
}

// // Ipam defines the desired state of IPAM
// type Ipam struct {
// 	Type string `json:"type,omitempty"`
// }

// SriovNetworkStatus defines the observed state of SriovNetwork
// +k8s:openapi-gen=true
type SriovNetworkStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovNetwork is the Schema for the sriovnetworks API
// +k8s:openapi-gen=true
type SriovNetwork struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovNetworkSpec   `json:"spec,omitempty"`
	Status SriovNetworkStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovNetworkList contains a list of SriovNetwork
type SriovNetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovNetwork `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovNetwork{}, &SriovNetworkList{})
}
