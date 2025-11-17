package orchestrator

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/kubernetes"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/openshift"
)

//go:generate ../../bin/mockgen -destination mock/mock_orchestrator.go -source orchestrator.go

// Interface represents an orchestrator abstraction that handles cluster-specific operations
// for SR-IOV network configuration. Different orchestrators (OpenShift, Kubernetes) may have
// different requirements for node draining and machine configuration pool management.
type Interface interface {
	// Name returns the name of the orchestrator implementation (e.g., "Kubernetes", "OpenShift").
	// This is used for logging and identification purposes.
	Name() string

	// ClusterType returns the type of cluster orchestrator (OpenShift or Kubernetes).
	// This is used to determine cluster-specific behavior throughout the operator.
	ClusterType() consts.ClusterType

	// Flavor returns the specific flavor of the cluster orchestrator.
	// Most implementations return ClusterFlavorDefault for standard clusters.
	// For OpenShift, this can be ClusterFlavorDefault (standard) or ClusterFlavorHypershift.
	// If the implementation does not have a specific flavor, ClusterFlavorDefault should be returned.
	Flavor() consts.ClusterFlavor

	// BeforeDrainNode performs orchestrator-specific logic before draining a node.
	// This is called by the drain controller before starting the node drain process.
	// For OpenShift, this may pause the MachineConfigPool to prevent automatic reboots.
	// Returns:
	//   - bool: true if the drain can proceed, false if the orchestrator needs more time to prepare
	//   - error: an error if the preparation failed
	BeforeDrainNode(context.Context, *corev1.Node) (bool, error)

	// AfterCompleteDrainNode performs orchestrator-specific logic after node draining is completed.
	// This is called by the drain controller after the node has been successfully drained and uncordoned.
	// For OpenShift, this may unpause the MachineConfigPool if this was the last node being drained.
	// Returns:
	//   - bool: true if the post-drain operations completed successfully, false if more time is needed
	//   - error: an error if the post-drain operations failed
	AfterCompleteDrainNode(context.Context, *corev1.Node) (bool, error)
}

// New creates a new orchestrator interface based on the provided cluster type.
// The cluster type is detected at operator startup based on the presence of OpenShift-specific APIs.
// Returns the orchestrator interface for the specified cluster type, or an error if the cluster type is unsupported.
func New(clusterType consts.ClusterType) (Interface, error) {
	switch clusterType {
	case consts.ClusterTypeOpenshift:
		return openshift.New()
	case consts.ClusterTypeKubernetes:
		return kubernetes.New()
	default:
		return nil, fmt.Errorf("unknown cluster type: %s", clusterType)
	}
}
