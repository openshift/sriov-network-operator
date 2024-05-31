package platforms

import (
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openshift"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openstack"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

//go:generate ../../bin/mockgen -destination mock/mock_platforms.go -source platforms.go
type Interface interface {
	openshift.OpenshiftContextInterface
	openstack.OpenstackInterface
}

type platformHelper struct {
	openshift.OpenshiftContextInterface
	openstack.OpenstackInterface
}

func NewDefaultPlatformHelper() (Interface, error) {
	openshiftContext, err := openshift.New()
	if err != nil {
		return nil, err
	}
	utilsHelper := utils.New()
	hostManager := host.NewHostManager(utilsHelper)
	openstackContext := openstack.New(hostManager)

	return &platformHelper{
		openshiftContext,
		openstackContext,
	}, nil
}
