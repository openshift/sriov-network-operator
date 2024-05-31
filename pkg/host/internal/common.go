package internal

import (
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

const (
	HostPathFromDaemon    = consts.Host
	RedhatReleaseFile     = "/etc/redhat-release"
	RhelRDMAConditionFile = "/usr/libexec/rdma-init-kernel"
	RhelRDMAServiceName   = "rdma"
	RhelPackageManager    = "yum"

	UbuntuRDMAConditionFile = "/usr/sbin/rdma-ndd"
	UbuntuRDMAServiceName   = "rdma-ndd"
	UbuntuPackageManager    = "apt-get"

	GenericOSReleaseFile = "/etc/os-release"
)
