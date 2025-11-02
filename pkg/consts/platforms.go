package consts

// ClusterType represents the type of Kubernetes cluster (e.g., OpenShift, vanilla Kubernetes)
type ClusterType string

const (
	// ClusterTypeOpenshift represents an OpenShift cluster
	ClusterTypeOpenshift ClusterType = "openshift"
	// ClusterTypeKubernetes represents a vanilla Kubernetes cluster
	ClusterTypeKubernetes ClusterType = "kubernetes"
)

// ClusterFlavor represents the specific flavor or variant of a cluster type
type ClusterFlavor string

const (
	// ClusterFlavorDefault represents the standard/default flavor of any cluster type
	ClusterFlavorDefault ClusterFlavor = "default"
	// ClusterFlavorHypershift represents an OpenShift Hypershift cluster
	ClusterFlavorHypershift ClusterFlavor = "hypershift"
)

// PlatformTypes represents the type of platform the cluster is running on (baremetal, virtual, etc.)
type PlatformTypes string

const (
	// Baremetal represents a bare metal platform
	Baremetal PlatformTypes = "Baremetal"
	// VirtualOpenStack represents a virtual OpenStack platform
	VirtualOpenStack PlatformTypes = "Virtual/Openstack"
)
