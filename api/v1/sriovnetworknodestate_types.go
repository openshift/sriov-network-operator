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
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SriovNetworkNodeStateSpec defines the desired state of SriovNetworkNodeState
type SriovNetworkNodeStateSpec struct {
	Interfaces Interfaces `json:"interfaces,omitempty"`
	Bridges    Bridges    `json:"bridges,omitempty"`
	System     System     `json:"system,omitempty"`
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
	LinkAdminState    string            `json:"linkAdminState,omitempty"`
	EswitchMode       string            `json:"eSwitchMode,omitempty"`
	ExternallyManaged bool              `json:"externallyManaged,omitempty"`
	TotalVfs          int               `json:"totalvfs,omitempty"`
	VFs               []VirtualFunction `json:"Vfs,omitempty"`
	AltNames          []string          `json:"altNames,omitempty"`
}
type InterfaceExts []InterfaceExt

type VirtualFunction struct {
	Name            string `json:"name,omitempty"`
	Mac             string `json:"mac,omitempty"`
	Assigned        string `json:"assigned,omitempty"`
	Driver          string `json:"driver,omitempty"`
	PciAddress      string `json:"pciAddress"`
	Vendor          string `json:"vendor,omitempty"`
	DeviceID        string `json:"deviceID,omitempty"`
	Vlan            int    `json:"Vlan,omitempty"`
	Mtu             int    `json:"mtu,omitempty"`
	VfID            int    `json:"vfID"`
	VdpaType        string `json:"vdpaType,omitempty"`
	RepresentorName string `json:"representorName,omitempty"`
	GUID            string `json:"guid,omitempty"`
}

// Bridges contains list of bridges
type Bridges struct {
	OVS []OVSConfigExt `json:"ovs,omitempty"`
}

// OVSConfigExt contains configuration for the concrete OVS bridge
type OVSConfigExt struct {
	// name of the bridge
	Name string `json:"name"`
	// bridge-level configuration for the bridge
	Bridge OVSBridgeConfig `json:"bridge,omitempty"`
	// uplink-level bridge configuration for each uplink(PF).
	// currently must contain only one element
	Uplinks []OVSUplinkConfigExt `json:"uplinks,omitempty"`
}

// OVSUplinkConfigExt contains configuration for the concrete OVS uplink(PF)
type OVSUplinkConfigExt struct {
	// pci address of the PF
	PciAddress string `json:"pciAddress"`
	// name of the PF interface
	Name string `json:"name,omitempty"`
	// configuration from the Interface OVS table for the PF
	Interface OVSInterfaceConfig `json:"interface,omitempty"`
}

type System struct {
	// +kubebuilder:validation:Enum=shared;exclusive
	//RDMA subsystem. Allowed value "shared", "exclusive".
	RdmaMode string `json:"rdmaMode,omitempty"`
	// OVS config. It will be provided for ovs-vswitchd service as other_config option
	// +kubebuilder:default:={hw-offload: "true"}
	OvsConfig map[string]string `json:"ovsConfig,omitempty"`
}

// SriovNetworkNodeStateStatus defines the observed state of SriovNetworkNodeState
type SriovNetworkNodeStateStatus struct {
	Interfaces    InterfaceExts `json:"interfaces,omitempty"`
	Bridges       Bridges       `json:"bridges,omitempty"`
	System        System        `json:"system,omitempty"`
	SyncStatus    string        `json:"syncStatus,omitempty"`
	LastSyncError string        `json:"lastSyncError,omitempty"`

	// Conditions represent the latest available observations of the SriovNetworkNodeState's state
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Sync Status",type=string,JSONPath=`.status.syncStatus`
//+kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
//+kubebuilder:printcolumn:name="Progressing",type=string,JSONPath=`.status.conditions[?(@.type=="Progressing")].status`
//+kubebuilder:printcolumn:name="Draining",type=string,JSONPath=`.status.conditions[?(@.type=="Draining")].status`
//+kubebuilder:printcolumn:name="Desired Sync State",type=string,JSONPath=`.metadata.annotations.sriovnetwork\.openshift\.io/desired-state`
//+kubebuilder:printcolumn:name="Current Sync State",type=string,JSONPath=`.metadata.annotations.sriovnetwork\.openshift\.io/current-state`
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

// StatusFieldsEqual returns true when all non-condition status fields in the
// receiver match those in other. Conditions are excluded because they are
// compared separately per ownership boundary.
func (s *SriovNetworkNodeStateStatus) StatusFieldsEqual(other *SriovNetworkNodeStateStatus) bool {
	return s.SyncStatus == other.SyncStatus &&
		s.LastSyncError == other.LastSyncError &&
		equality.Semantic.DeepEqual(s.Interfaces, other.Interfaces) &&
		equality.Semantic.DeepEqual(s.Bridges, other.Bridges) &&
		equality.Semantic.DeepEqual(s.System, other.System)
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &SriovNetworkNodeState{}, &SriovNetworkNodeStateList{})
		return nil
	})
}
