package vars

import (
	"os"
	"regexp"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

var (
	// Namespace contains k8s namespace
	Namespace string

	// ClusterType used by the operator to specify the platform it's running on
	// supported values [kubernetes,openshift]
	ClusterType string

	// DevMode controls the developer mode in the operator
	// developer mode allows the operator to use un-supported network devices
	DevMode bool

	// NodeName initialize and used by the config-daemon to identify the node it's running on
	NodeName = ""

	// Destdir destination directory for the checkPoint file on the host
	Destdir string

	// PlatformType specify the current platform the operator is running on
	PlatformType = consts.Baremetal
	// PlatformsMap contains supported platforms for virtual VF
	PlatformsMap = map[string]consts.PlatformTypes{
		"openstack": consts.VirtualOpenStack,
	}

	// SupportedVfIds list of supported virtual functions IDs
	// loaded on daemon initialization by reading the supported-nics configmap
	SupportedVfIds []string

	// DpdkDrivers supported DPDK drivers for virtual functions
	DpdkDrivers = []string{"igb_uio", "vfio-pci", "uio_pci_generic"}

	// InChroot global variable to mark that the config-daemon code is inside chroot on the host file system
	InChroot = false

	// UsingSystemdMode global variable to mark the config-daemon is running on systemd mode
	UsingSystemdMode = false

	// ParallelNicConfig global variable to perform NIC configuration in parallel
	ParallelNicConfig = false

	// FilesystemRoot used by test to mock interactions with filesystem
	FilesystemRoot = ""

	// OVSDBSocketPath path to OVSDB socket
	OVSDBSocketPath = "unix:///var/run/openvswitch/db.sock"

	//Cluster variables
	Config *rest.Config    = nil
	Scheme *runtime.Scheme = nil

	// PfPhysPortNameRe regex to find switchdev devices on the host
	PfPhysPortNameRe = regexp.MustCompile(`p\d+`)

	// ResourcePrefix is the device plugin prefix we use to expose the devices to the nodes
	ResourcePrefix = ""

	// DisableablePlugins contains which plugins can be disabled in sriov config daemon
	DisableablePlugins = map[string]struct{}{"mellanox": {}}
)

func init() {
	Namespace = os.Getenv("NAMESPACE")

	ClusterType = os.Getenv("CLUSTER_TYPE")

	DevMode = false
	mode := os.Getenv("DEV_MODE")
	if mode == "TRUE" {
		DevMode = true
	}

	Destdir = "/host/tmp"
	destdir := os.Getenv("DEST_DIR")
	if destdir != "" {
		Destdir = destdir
	}

	ResourcePrefix = os.Getenv("RESOURCE_PREFIX")
}
