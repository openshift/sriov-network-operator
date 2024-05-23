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

// SriovNetworkNodePolicySpec defines the desired state of SriovNetworkNodePolicy
type SriovNetworkNodePolicySpec struct {
	// SRIOV Network device plugin endpoint resource name
	ResourceName string `json:"resourceName"`
	// NodeSelector selects the nodes to be configured
	NodeSelector map[string]string `json:"nodeSelector"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=99
	// Priority of the policy, higher priority policies can override lower ones.
	Priority int `json:"priority,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// MTU of VF
	Mtu int `json:"mtu,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// Number of VFs for each PF
	NumVfs int `json:"numVfs"`
	// NicSelector selects the NICs to be configured
	NicSelector SriovNetworkNicSelector `json:"nicSelector"`
	// +kubebuilder:validation:Enum=netdevice;vfio-pci
	// +kubebuilder:default=netdevice
	// The driver type for configured VFs. Allowed value "netdevice", "vfio-pci". Defaults to netdevice.
	DeviceType string `json:"deviceType,omitempty"`
	// RDMA mode. Defaults to false.
	IsRdma bool `json:"isRdma,omitempty"`
	// mount vhost-net device. Defaults to false.
	NeedVhostNet bool `json:"needVhostNet,omitempty"`
	// +kubebuilder:validation:Enum=eth;ETH;ib;IB
	// NIC Link Type. Allowed value "eth", "ETH", "ib", and "IB".
	LinkType string `json:"linkType,omitempty"`
	// +kubebuilder:validation:Enum=legacy;switchdev
	// NIC Device Mode. Allowed value "legacy","switchdev".
	EswitchMode string `json:"eSwitchMode,omitempty"`
	// +kubebuilder:validation:Enum=virtio;vhost
	// VDPA device type. Allowed value "virtio", "vhost"
	VdpaType string `json:"vdpaType,omitempty"`
	// Exclude device's NUMA node when advertising this resource by SRIOV network device plugin. Default to false.
	ExcludeTopology bool `json:"excludeTopology,omitempty"`
	// don't create the virtual function only allocated them to the device plugin. Defaults to false.
	ExternallyManaged bool `json:"externallyManaged,omitempty"`
	// contains bridge configuration for matching PFs,
	// valid only for eSwitchMode==switchdev
	Bridge Bridge `json:"bridge,omitempty"`
}

type SriovNetworkNicSelector struct {
	// The vendor hex code of SR-IoV device. Allowed value "8086", "15b3".
	Vendor string `json:"vendor,omitempty"`
	// The device hex code of SR-IoV device. Allowed value "0d58", "1572", "158b", "1013", "1015", "1017", "101b".
	DeviceID string `json:"deviceID,omitempty"`
	// PCI address of SR-IoV PF.
	RootDevices []string `json:"rootDevices,omitempty"`
	// Name of SR-IoV PF.
	PfNames []string `json:"pfNames,omitempty"`
	// Infrastructure Networking selection filter. Allowed value "openstack/NetworkID:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	NetFilter string `json:"netFilter,omitempty"`
}

// contains spec for the bridge
type Bridge struct {
	// contains configuration for the OVS bridge,
	OVS *OVSConfig `json:"ovs,omitempty"`
}

// IsEmpty return empty if the struct doesn't contain configuration
func (b *Bridge) IsEmpty() bool {
	return b.OVS == nil
}

// OVSConfig optional configuration for OVS bridge and uplink Interface
type OVSConfig struct {
	// contains bridge level settings
	Bridge OVSBridgeConfig `json:"bridge,omitempty"`
	// contains settings for uplink (PF)
	Uplink OVSUplinkConfig `json:"uplink,omitempty"`
}

// OVSBridgeConfig contains some options from the Bridge table in OVSDB
type OVSBridgeConfig struct {
	// configure datapath_type field in the Bridge table in OVSDB
	DatapathType string `json:"datapathType,omitempty"`
	// IDs to inject to external_ids field in the Bridge table in OVSDB
	ExternalIDs map[string]string `json:"externalIDs,omitempty"`
	// additional options to inject to other_config field in the bridge table in OVSDB
	OtherConfig map[string]string `json:"otherConfig,omitempty"`
}

// OVSUplinkConfig contains PF interface configuration for the bridge
type OVSUplinkConfig struct {
	// contains settings for PF interface in the OVS bridge
	Interface OVSInterfaceConfig `json:"interface,omitempty"`
}

// OVSInterfaceConfig contains some options from the Interface table of the OVSDB for PF
type OVSInterfaceConfig struct {
	// type field in the Interface table in OVSDB
	Type string `json:"type,omitempty"`
	// options field in the Interface table in OVSDB
	Options map[string]string `json:"options,omitempty"`
	// external_ids field in the Interface table in OVSDB
	ExternalIDs map[string]string `json:"externalIDs,omitempty"`
	// other_config field in the Interface table in OVSDB
	OtherConfig map[string]string `json:"otherConfig,omitempty"`
}

// SriovNetworkNodePolicyStatus defines the observed state of SriovNetworkNodePolicy
type SriovNetworkNodePolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SriovNetworkNodePolicy is the Schema for the sriovnetworknodepolicies API
type SriovNetworkNodePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovNetworkNodePolicySpec   `json:"spec,omitempty"`
	Status SriovNetworkNodePolicyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SriovNetworkNodePolicyList contains a list of SriovNetworkNodePolicy
type SriovNetworkNodePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovNetworkNodePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SriovNetworkNodePolicy{}, &SriovNetworkNodePolicyList{})
}
