package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovNetworkNodePolicySpec defines the desired state of SriovAcceleratorNodePolicy
// +k8s:openapi-gen=true
// +kubebuilder:pruning:PreserveUnknownFields
type SriovAcceleratorNodePolicySpec struct {
	// SRIOV Accelerator device plugin endpoint resource name,
	ResourceName string `json:"resourceName"`
	// DeviceName selects the config_bbdev code, Allowed value "FPGA_LTE", "FPGA_5GNR"
	DeviceName string `json:"deviceName"`
	// NodeSelector selects the nodes to be configured
	NodeSelector map[string]string `json:"nodeSelector"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=99
	// Priority of the policy, higher priority policies can override lower ones.
	Priority int `json:"priority,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// Number of VFs for each PF
	NumVfs int `json:"numVfs"`
	// AccelSelector selects the PCI to be configured
	AccelSelector SriovAcceleratorNicSelector `json:"accelSelector"`
	// Config configure the accelerator card
	Config string `json:"config"`
}

// +k8s:openapi-gen=false
type SriovAcceleratorNicSelector struct {
	// The vendor hex code of SR-IoV device. Allowed value "8086", "15b3".
	Vendor string `json:"vendor,omitempty"`
	// The device hex code of SR-IoV device. Allowed value "158b", "1015", "1017".
	DeviceID string `json:"deviceID,omitempty"`
	// PCI address of SR-IoV PF.
	RootDevices []string `json:"rootDevices,omitempty"`
}

// SriovAcceleratorNodePolicyStatus defines the observed state of SriovAcceleratorNodePolicy
// +k8s:openapi-gen=true
type SriovAcceleratorNodePolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovAcceleratorNodePolicy is the Schema for the sriovacceleratornodepolicies API
// +genclient
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=sriovacceleratornodepolicies,scope=Namespaced
type SriovAcceleratorNodePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovAcceleratorNodePolicySpec   `json:"spec,omitempty"`
	Status SriovAcceleratorNodePolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovAcceleratorNodePolicyList contains a list of SriovAcceleratorNodePolicy
type SriovAcceleratorNodePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovAcceleratorNodePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovAcceleratorNodePolicy{}, &SriovAcceleratorNodePolicyList{})
}
