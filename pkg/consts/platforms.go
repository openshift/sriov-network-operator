package consts

import "fmt"

type ClusterType string

const (
	ClusterTypeOpenshift  ClusterType = "openshift"
	ClusterTypeKubernetes ClusterType = "kubernetes"
)

type ClusterFlavor string

const (
	ClusterFlavorVanillaK8s ClusterFlavor = "vanilla"
	ClusterFlavorOpenshift  ClusterFlavor = "default"
	ClusterFlavorHypershift ClusterFlavor = "hypershift"
)

// PlatformTypes
type PlatformTypes int

const (
	// Baremetal platform
	Baremetal PlatformTypes = iota
	// VirtualOpenStack platform
	VirtualOpenStack
)

func (e PlatformTypes) String() string {
	switch e {
	case Baremetal:
		return "Baremetal"
	case VirtualOpenStack:
		return "Virtual/Openstack"
	default:
		return fmt.Sprintf("%d", int(e))
	}
}
