package platform

import (
	"fmt"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform/baremetal"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform/openstack"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

//go:generate ../../bin/mockgen -destination mock/mock_platform.go -source platform.go
type Interface interface {
	Init() error
	GetHostHelpers() helper.HostHelpersInterface

	DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error)
	DiscoverBridges() (sriovnetworkv1.Bridges, error)

	GetPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) (plugin.VendorPlugin, []plugin.VendorPlugin, error)
	SystemdGetPlugin(phase string) (plugin.VendorPlugin, error)
}

func New(hostHelpers helper.HostHelpersInterface) (Interface, error) {
	switch vars.PlatformType {
	case consts.Baremetal:
		return baremetal.New(hostHelpers)
	case consts.VirtualOpenStack:
		return openstack.New(hostHelpers)
	default:
		return nil, fmt.Errorf("unknown platform type %s", vars.PlatformType)
	}
}
