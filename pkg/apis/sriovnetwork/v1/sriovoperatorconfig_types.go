package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovOperatorConfigSpec defines the desired state of SriovOperatorConfig
// +k8s:openapi-gen=true
type SriovOperatorConfigSpec struct {
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:UniqueItems=true
	// NodeSelector selects the nodes to be configured
	ConfigDaemonNodeSelector map[string]string `json:"configDaemonNodeSelector,omitempty"`
	// Flag to control whether the network resource injector webhook shall be deployed
	EnableInjector *bool `json:"enableInjector,omitempty"`
	// Flag to control whether the operator admission controller webhook shall be deployed
	EnableOperatorWebhook *bool `json:"enableOperatorWebhook,omitempty"`
	DisableReboot         *bool `json:"disableReboot,omitempty"`
}

// SriovOperatorConfigStatus defines the observed state of SriovOperatorConfig
// +k8s:openapi-gen=true
type SriovOperatorConfigStatus struct {
	// Show the runtime status of the network resource injector webhook
	Injector string `json:"injector,omitempty"`
	// Show the runtime status of the operator admission controller webhook
	OperatorWebhook string `json:"operatorWebhook,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovOperatorConfig is the Schema for the sriovoperatorconfigs API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=sriovoperatorconfigs,scope=Namespaced
type SriovOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovOperatorConfigSpec   `json:"spec,omitempty"`
	Status SriovOperatorConfigStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovOperatorConfigList contains a list of SriovOperatorConfig
type SriovOperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovOperatorConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovOperatorConfig{}, &SriovOperatorConfigList{})
}
