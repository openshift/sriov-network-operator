package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

type Kubernetes struct{}

func New() (*Kubernetes, error) {
	return &Kubernetes{}, nil
}

func (k *Kubernetes) ClusterType() consts.ClusterType {
	return consts.ClusterTypeKubernetes
}

func (k *Kubernetes) Flavor() consts.ClusterFlavor {
	return consts.ClusterFlavorVanillaK8s
}

func (k *Kubernetes) BeforeDrainNode(_ context.Context, _ *corev1.Node) (bool, error) {
	return true, nil
}

func (k *Kubernetes) AfterCompleteDrainNode(_ context.Context, _ *corev1.Node) (bool, error) {
	return true, nil
}
