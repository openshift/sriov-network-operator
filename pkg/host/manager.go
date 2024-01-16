package host

import (
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/kernel"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/service"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/sriov"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/udev"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

// Contains all the host manipulation functions
type HostManagerInterface interface {
	types.KernelInterface
	types.NetworkInterface
	types.ServiceInterface
	types.UdevInterface
	types.SriovInterface
}

type hostManager struct {
	utils.CmdInterface
	types.KernelInterface
	types.NetworkInterface
	types.ServiceInterface
	types.UdevInterface
	types.SriovInterface
}

func NewHostManager(utilsInterface utils.CmdInterface) HostManagerInterface {
	k := kernel.New(utilsInterface)
	n := network.New(utilsInterface)
	sv := service.New(utilsInterface)
	u := udev.New(utilsInterface)
	sr := sriov.New(utilsInterface, k, n, u)

	return &hostManager{
		utilsInterface,
		k,
		n,
		sv,
		u,
		sr,
	}
}
