package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovAcceleratorNodeStateSpec defines the desired state of SriovAcceleratorNodeState
// +k8s:openapi-gen=true
type SriovAcceleratorNodeStateSpec struct {
	DpConfigVersion string `json:"dpConfigVersion,omitempty"`
	Cards           Cards  `json:"cards,omitempty"`
}

type Cards []Card

type Card struct {
	DeviceName string `json:"deviceName"`
	PciAddress string `json:"pciAddress"`
	NumVfs     int    `json:"numVfs"`
	Config     string `json:"config"`
}

type AcceleratorExt struct {
	Driver     string                       `json:"driver,omitempty"`
	PciAddress string                       `json:"pciAddress"`
	DeviceName string                       `json:"deviceName,omitempty"`
	Vendor     string                       `json:"vendor,omitempty"`
	DeviceID   string                       `json:"deviceID,omitempty"`
	NumVfs     int                          `json:"numVfs,omitempty"`
	TotalVfs   int                          `json:"totalvfs,omitempty"`
	Config     string                       `json:"config,omitempty"`
	VFs        []AcceleratorVirtualFunction `json:"Vfs,omitempty"`
}

type AcceleratorExts []AcceleratorExt

type AcceleratorVirtualFunction struct {
	Driver     string `json:"driver,omitempty"`
	PciAddress string `json:"pciAddress"`
	Vendor     string `json:"vendor,omitempty"`
	DeviceID   string `json:"deviceID,omitempty"`
	VfID       int    `json:"vfID"`
}

// SriovAcceleratorNodeStateStatus defines the observed state of SriovAcceleratorNodeState
// +k8s:openapi-gen=true
type SriovAcceleratorNodeStateStatus struct {
	Accelerators  AcceleratorExts `json:"accelerators,omitempty"`
	SyncStatus    string          `json:"syncStatus,omitempty"`
	LastSyncError string          `json:"lastSyncError,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// SriovAcceleratorNodeState is the Schema for the sriovacceleratornodestates API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=sriovacceleratornodestates,scope=Namespaced
type SriovAcceleratorNodeState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovAcceleratorNodeStateSpec   `json:"spec,omitempty"`
	Status SriovAcceleratorNodeStateStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovAcceleratorNodeStateList contains a list of SriovAcceleratorNodeState
type SriovAcceleratorNodeStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovAcceleratorNodeState `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovAcceleratorNodeState{}, &SriovAcceleratorNodeStateList{})
}
