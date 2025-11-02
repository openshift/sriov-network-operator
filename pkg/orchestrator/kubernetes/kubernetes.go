package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

// Kubernetes implements the orchestrator.Interface for vanilla Kubernetes clusters.
type Kubernetes struct{}

// New creates a new Kubernetes orchestrator instance.
func New() (*Kubernetes, error) {
	return &Kubernetes{}, nil
}

// Name returns the name of the Kubernetes orchestrator.
func (k *Kubernetes) Name() string {
	return "Kubernetes"
}

// ClusterType returns the cluster type for Kubernetes.
func (k *Kubernetes) ClusterType() consts.ClusterType {
	return consts.ClusterTypeKubernetes
}

// Flavor returns the cluster flavor for vanilla Kubernetes.
func (k *Kubernetes) Flavor() consts.ClusterFlavor {
	return consts.ClusterFlavorDefault
}

// BeforeDrainNode is a no-op for vanilla Kubernetes as no special preparation is needed.
// Always returns true to allow the drain to proceed immediately.
func (k *Kubernetes) BeforeDrainNode(_ context.Context, _ *corev1.Node) (bool, error) {
	return true, nil
}

// AfterCompleteDrainNode is a no-op for vanilla Kubernetes as no cleanup is needed.
// Always returns true to indicate completion.
func (k *Kubernetes) AfterCompleteDrainNode(_ context.Context, _ *corev1.Node) (bool, error) {
	return true, nil
}
