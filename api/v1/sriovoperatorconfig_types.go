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

// SriovOperatorConfigSpec defines the desired state of SriovOperatorConfig
type SriovOperatorConfigSpec struct {
	// NodeSelector selects the nodes to be configured
	ConfigDaemonNodeSelector map[string]string `json:"configDaemonNodeSelector,omitempty"`
	// Flag to control whether the network resource injector webhook shall be deployed
	EnableInjector *bool `json:"enableInjector,omitempty"`
	// Flag to control whether the operator admission controller webhook shall be deployed
	EnableOperatorWebhook *bool `json:"enableOperatorWebhook,omitempty"`
	// Flag to control the log verbose level of the operator. Set to '0' to show only the basic logs. And set to '2' to show all the available logs.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	LogLevel int `json:"logLevel,omitempty"`
	// Flag to disable nodes drain during debugging
	DisableDrain bool `json:"disableDrain,omitempty"`
	// Flag to enable OVS hardware offload. Set to 'true' to provision switchdev-configuration.service and enable OpenvSwitch hw-offload on nodes.
	EnableOvsOffload bool `json:"enableOvsOffload,omitempty"`
	// Flag to enable the sriov-network-config-daemon to use a systemd service to configure SR-IOV devices on boot
	// Default mode: daemon
	// +kubebuilder:validation:Enum=daemon;systemd
	ConfigurationMode ConfigurationModeType `json:"configurationMode,omitempty"`
	// Flag to enable Container Device Interface mode for SR-IOV Network Device Plugin
	UseCDI bool `json:"useCDI,omitempty"`
}

// SriovOperatorConfigStatus defines the observed state of SriovOperatorConfig
type SriovOperatorConfigStatus struct {
	// Show the runtime status of the network resource injector webhook
	Injector string `json:"injector,omitempty"`
	// Show the runtime status of the operator admission controller webhook
	OperatorWebhook string `json:"operatorWebhook,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SriovOperatorConfig is the Schema for the sriovoperatorconfigs API
type SriovOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovOperatorConfigSpec   `json:"spec,omitempty"`
	Status SriovOperatorConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SriovOperatorConfigList contains a list of SriovOperatorConfig
type SriovOperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovOperatorConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovOperatorConfig{}, &SriovOperatorConfigList{})
}
