package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovNetworkNodePolicySpec defines the desired state of SriovNetworkNodePolicy
// +k8s:openapi-gen=true
type SriovNetworkNodePolicySpec struct {
	ResourceName string                  `json:"resourceName"`
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:UniqueItems=true
	NodeSelector map[string]string       `json:"nodeSelector"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=99
	Priority     int                     `json:"priority,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=9000
	Mtu          int                     `json:"mtu,omitempty"`
	// +kubebuilder:validation:Minimum=0
	NumVfs       int                     `json:"numVfs"`
	NicSelector  SriovNetworkNicSelector `json:"nicSelector"`
	// +kubebuilder:validation:Enum=netdevice,vfio-pci
	DeviceType   string                  `json:"deviceType,omitempty"`
	IsRdma       bool                    `json:"isRdma,omitempty"`
}

type SriovNetworkNicSelector struct {
	// +kubebuilder:validation:Enum=8086,15b3
	Vendor      string   `json:"vendor,omitempty"`
	DeviceID    string   `json:"deviceID,omitempty"`
	RootDevices []string `json:"rootDevices,omitempty"`
	PfNames     []string `json:"pfNames,omitempty"`
}

// SriovNetworkNodePolicyStatus defines the observed state of SriovNetworkNodePolicy
// +k8s:openapi-gen=true
type SriovNetworkNodePolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovNetworkNodePolicy is the Schema for the sriovnetworknodepolicies API
// +k8s:openapi-gen=true
type SriovNetworkNodePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovNetworkNodePolicySpec   `json:"spec,omitempty"`
	Status SriovNetworkNodePolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovNetworkNodePolicyList contains a list of SriovNetworkNodePolicy
type SriovNetworkNodePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovNetworkNodePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovNetworkNodePolicy{}, &SriovNetworkNodePolicyList{})
}
