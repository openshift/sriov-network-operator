package utils

import (
	mcclientset "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OpenshiftFlavor holds metadata about the type of Openshift environment the operator is in.
type OpenshiftFlavor string

const (
	// Hypershift flavor of openshift: https://github.com/openshift/hypershift
	OpenshiftFlavorHypershift OpenshiftFlavor = "hypershift"
	// OpenshiftFlavorDefault covers all remaining flavors of openshift not explicitly called out above
	OpenshiftFlavorDefault OpenshiftFlavor = "default"
)

// OpenshiftContext contains metadata and structs utilized to interact with Openshift clusters
type OpenshiftContext struct {
	// McClient is a client for MachineConfigs in an Openshift environment
	McClient mcclientset.Interface

	// IsOpenShiftCluster boolean to point out if the cluster is an OpenShift cluster
	IsOpenShiftCluster bool

	// OpenshiftFlavor holds metadata about the type of Openshift environment the operator is in.
	OpenshiftFlavor OpenshiftFlavor
}

func NewOpenshiftContext(config *rest.Config, scheme *runtime.Scheme) (*OpenshiftContext, error) {
	if ClusterType != ClusterTypeOpenshift {
		return &OpenshiftContext{nil, false, ""}, nil
	}

	mcclient, err := mcclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	openshiftFlavor := OpenshiftFlavorDefault
	infraClient, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, err
	}

	isHypershift, err := IsExternalControlPlaneCluster(infraClient)
	if err != nil {
		return nil, err
	}

	if isHypershift {
		openshiftFlavor = OpenshiftFlavorHypershift
	}

	return &OpenshiftContext{mcclient, true, openshiftFlavor}, nil
}

func (c OpenshiftContext) IsOpenshiftCluster() bool {
	return c.IsOpenShiftCluster
}

func (c OpenshiftContext) IsHypershift() bool {
	return c.OpenshiftFlavor == OpenshiftFlavorHypershift
}
