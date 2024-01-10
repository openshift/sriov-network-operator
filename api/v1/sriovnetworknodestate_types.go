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

// SriovNetworkNodeStateSpec defines the desired state of SriovNetworkNodeState
type SriovNetworkNodeStateSpec struct {
	DpConfigVersion string     `json:"dpConfigVersion,omitempty"`
	Interfaces      Interfaces `json:"interfaces,omitempty"`
}

type Interfaces []Interface

type Interface struct {
	PciAddress        string    `json:"pciAddress"`
	NumVfs            int       `json:"numVfs,omitempty"`
	Mtu               int       `json:"mtu,omitempty"`
	Name              string    `json:"name,omitempty"`
	LinkType          string    `json:"linkType,omitempty"`
	EswitchMode       string    `json:"eSwitchMode,omitempty"`
	VfGroups          []VfGroup `json:"vfGroups,omitempty"`
	ExternallyManaged bool      `json:"externallyManaged,omitempty"`
}

type VfGroup struct {
	ResourceName string `json:"resourceName,omitempty"`
	DeviceType   string `json:"deviceType,omitempty"`
	VfRange      string `json:"vfRange,omitempty"`
	PolicyName   string `json:"policyName,omitempty"`
	Mtu          int    `json:"mtu,omitempty"`
	IsRdma       bool   `json:"isRdma,omitempty"`
	VdpaType     string `json:"vdpaType,omitempty"`
}

type InterfaceExt struct {
	Name              string            `json:"name,omitempty"`
	Mac               string            `json:"mac,omitempty"`
	Driver            string            `json:"driver,omitempty"`
	PciAddress        string            `json:"pciAddress"`
	Vendor            string            `json:"vendor,omitempty"`
	DeviceID          string            `json:"deviceID,omitempty"`
	NetFilter         string            `json:"netFilter,omitempty"`
	Mtu               int               `json:"mtu,omitempty"`
	NumVfs            int               `json:"numVfs,omitempty"`
	LinkSpeed         string            `json:"linkSpeed,omitempty"`
	LinkType          string            `json:"linkType,omitempty"`
	EswitchMode       string            `json:"eSwitchMode,omitempty"`
	ExternallyManaged bool              `json:"externallyManaged,omitempty"`
	TotalVfs          int               `json:"totalvfs,omitempty"`
	VFs               []VirtualFunction `json:"Vfs,omitempty"`
}
type InterfaceExts []InterfaceExt

type VirtualFunction struct {
	Name       string `json:"name,omitempty"`
	Mac        string `json:"mac,omitempty"`
	Assigned   string `json:"assigned,omitempty"`
	Driver     string `json:"driver,omitempty"`
	PciAddress string `json:"pciAddress"`
	Vendor     string `json:"vendor,omitempty"`
	DeviceID   string `json:"deviceID,omitempty"`
	Vlan       int    `json:"Vlan,omitempty"`
	Mtu        int    `json:"mtu,omitempty"`
	VfID       int    `json:"vfID"`
	VdpaType   string `json:"vdpaType,omitempty"`
}

// SriovNetworkNodeStateStatus defines the observed state of SriovNetworkNodeState
type SriovNetworkNodeStateStatus struct {
	Interfaces    InterfaceExts `json:"interfaces,omitempty"`
	SyncStatus    string        `json:"syncStatus,omitempty"`
	LastSyncError string        `json:"lastSyncError,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Sync Status",type=string,JSONPath=`.status.syncStatus`
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// SriovNetworkNodeState is the Schema for the sriovnetworknodestates API
type SriovNetworkNodeState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovNetworkNodeStateSpec   `json:"spec,omitempty"`
	Status SriovNetworkNodeStateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SriovNetworkNodeStateList contains a list of SriovNetworkNodeState
type SriovNetworkNodeStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovNetworkNodeState `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovNetworkNodeState{}, &SriovNetworkNodeStateList{})
}
