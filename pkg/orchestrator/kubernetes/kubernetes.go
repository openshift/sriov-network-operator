package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

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

// GetTLSConfig always returns nil for vanilla Kubernetes clusters.
// On Kubernetes, TLS configuration is managed via environment variables
// (TLS_CIPHER_SUITES, TLS_MIN_VERSION) which are read directly by the controller.
func (k *Kubernetes) GetTLSConfig(_ context.Context) (*consts.TLSConfig, error) {
	log.Log.WithName("Kubernetes").V(2).Info("TLS configuration on Kubernetes is managed via environment variables, returning nil")
	return nil, nil
}
