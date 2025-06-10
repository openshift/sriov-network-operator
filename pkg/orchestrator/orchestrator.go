package orchestrator

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/kubernetes"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/openshift"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

//go:generate ../../bin/mockgen -destination mock/mock_orchestrator.go -source orchestrator.go
type Interface interface {
	// ClusterType represent to a cluster type
	ClusterType() consts.ClusterType
	// Flavor represents the internal flavor of the cluster
	Flavor() consts.ClusterFlavor
	// BeforeDrainNode run a platform-specific logic before draining the node
	BeforeDrainNode(context.Context, *corev1.Node) (bool, error)
	// AfterCompleteDrainNode run a platform-specific logic after node draining was completed
	AfterCompleteDrainNode(context.Context, *corev1.Node) (bool, error)
}

func New() (Interface, error) {
	switch vars.ClusterType {
	case consts.ClusterTypeOpenshift:
		return openshift.New()
	case consts.ClusterTypeKubernetes:
		return kubernetes.New()
	default:
		return nil, fmt.Errorf("unknown orchestration type: %s", vars.ClusterType)
	}
}
