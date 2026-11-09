package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// SriovNetworkPoolConfigSpec defines the desired state of SriovNetworkPoolConfig
type SriovNetworkPoolConfigSpec struct {
	// OvsHardwareOffloadConfig describes the OVS HWOL configuration for selected Nodes
	OvsHardwareOffloadConfig *OvsHardwareOffloadConfig `json:"ovsHardwareOffloadConfig,omitempty"`

	// nodeSelector specifies a label selector for Nodes
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`

	// maxUnavailable defines either an integer number or percentage
	// of nodes in the pool that can go Unavailable during an update.
	//
	// A value larger than 1 will mean multiple nodes going unavailable during
	// the update, which may affect your workload stress on the remaining nodes.
	// Drain will respect Pod Disruption Budgets (PDBs) such as etcd quorum guards,
	// even if maxUnavailable is greater than one.
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`

	// +kubebuilder:validation:Enum=shared;exclusive
	// RDMA subsystem. Allowed value "shared", "exclusive".
	RdmaMode string `json:"rdmaMode,omitempty"`
}

type OvsHardwareOffloadConfig struct {
	// Name is mandatory and must be unique.
	// On Kubernetes:
	// Name is the name of OvsHardwareOffloadConfig
	// On OpenShift:
	// Name is the name of MachineConfigPool to be enabled with OVS hardware offload
	// TODO: Remove MachineConfig-related configuration from SriovNetworkPoolConfig in the future.
	Name string `json:"name,omitempty"`
	// OVS config. It will be provided for ovs-vswitchd service as other_config option
	// +kubebuilder:default:={hw-offload: "true"}
	OvsConfig map[string]string `json:"otherConfig,omitempty"`
}

// SriovNetworkPoolConfigStatus defines the observed state of SriovNetworkPoolConfig
type SriovNetworkPoolConfigStatus struct {
	// MatchedNodeCount counts managed nodes targeted by a regular pool.
	// Nil means unknown or not applicable to a hardware offload MachineConfig pool.
	MatchedNodeCount *int32 `json:"matchedNodeCount,omitempty"`
	// ReadyNodeCount counts matched nodes where a regular pool is effective.
	// Nil means unknown or not applicable to a hardware offload MachineConfig pool.
	ReadyNodeCount  *int32 `json:"readyNodeCount,omitempty"`
	ConditionStatus `json:",inline"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Matched",type=integer,JSONPath=`.status.matchedNodeCount`
//+kubebuilder:printcolumn:name="Ready Nodes",type=integer,JSONPath=`.status.readyNodeCount`
//+kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SriovNetworkPoolConfig is the Schema for the sriovnetworkpoolconfigs API
type SriovNetworkPoolConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SriovNetworkPoolConfigSpec   `json:"spec,omitempty"`
	Status SriovNetworkPoolConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SriovNetworkPoolConfigList contains a list of SriovNetworkPoolConfig
type SriovNetworkPoolConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SriovNetworkPoolConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &SriovNetworkPoolConfig{}, &SriovNetworkPoolConfigList{})
		return nil
	})
}
