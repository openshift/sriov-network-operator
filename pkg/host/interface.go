package host

import (
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

const (
	hostPathFromDaemon    = consts.Host
	redhatReleaseFile     = "/etc/redhat-release"
	rhelRDMAConditionFile = "/usr/libexec/rdma-init-kernel"
	rhelRDMAServiceName   = "rdma"
	rhelPackageManager    = "yum"

	ubuntuRDMAConditionFile = "/usr/sbin/rdma-ndd"
	ubuntuRDMAServiceName   = "rdma-ndd"
	ubuntuPackageManager    = "apt-get"

	genericOSReleaseFile = "/etc/os-release"
)

// Contains all the host manipulation functions
type HostManagerInterface interface {
	KernelInterface
	NetworkInterface
	ServiceInterface
	UdevInterface
	SriovInterface
}

type hostManager struct {
	utils.CmdInterface
	KernelInterface
	NetworkInterface
	ServiceInterface
	UdevInterface
	SriovInterface
}

func NewHostManager(utilsInterface utils.CmdInterface) HostManagerInterface {
	k := newKernelInterface(utilsInterface)
	n := newNetworkInterface(utilsInterface)
	sv := newServiceInterface(utilsInterface)
	u := newUdevInterface(utilsInterface)
	sr := newSriovInterface(utilsInterface, k, n, u)

	return &hostManager{
		utilsInterface,
		k,
		n,
		sv,
		u,
		sr,
	}
}
