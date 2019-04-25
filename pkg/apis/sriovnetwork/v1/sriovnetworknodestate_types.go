package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovNetworkNodeStateSpec defines the desired state of SriovNetworkNodeState
// +k8s:openapi-gen=true
type SriovNetworkNodeStateSpec struct {
	Interfaces  []Interface `json:"interfaces,omitempty"`
}

type Interface struct {
	Name           string `json:"name"`
	ResourceName   string `json:"resourceName"`
	NumVfs         int `json:"numVfs,omitempty"`
	Mtu            int `json:"mtu,omitempty"`
	DeviceType     string `json:"deviceType,omitempty"`
}

type InterfaceExt struct {
	Interface `json:",inline"`
	Vendor string `json:"vendor,omitempty"`
	LinkSpeed string `json:"linkSpeed,omitempty"`
	PciAddress     string `json:"pciAddress"`
	KernelDriver   string `json:"kernelDriver"`
	VF VirutalFunction `json:"Vfs,omitempty"`
}

type VirutalFunction struct {
	No           int      `json:"no,omitempty"`
	Mac          string      `json:"mac,omitempty"`
	Assigned     string      `json:"assigned,omitempty"`
	KernelDriver string      `json:"kernelDriver,omitempty"`
	PciAddress   string      `json:"pciAddress,omitempty"`
	Vlan         int         `json:"Vlan,omitempty"`
}

// SriovNetworkNodeStateStatus defines the observed state of SriovNetworkNodeState
// +k8s:openapi-gen=true
type SriovNetworkNodeStateStatus struct {
	Interfaces  []InterfaceExt `json:"interfaces,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovNetworkNodeState is the Schema for the sriovnetworknodestates API
// +k8s:openapi-gen=true
type SriovNetworkNodeState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovNetworkNodeStateSpec   `json:"spec,omitempty"`
	Status SriovNetworkNodeStateStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SriovNetworkNodeStateList contains a list of SriovNetworkNodeState
type SriovNetworkNodeStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovNetworkNodeState `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovNetworkNodeState{}, &SriovNetworkNodeStateList{})
}
