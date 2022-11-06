package utils

import mcclientset "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned"

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
	// OpenshiftFlavor holds metadata about the type of Openshift environment the operator is in.
	OpenshiftFlavor OpenshiftFlavor
}

func (c OpenshiftContext) IsHypershift() bool {
	return c.OpenshiftFlavor == OpenshiftFlavorHypershift
}
