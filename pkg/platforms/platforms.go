package platforms

import (
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openshift"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openstack"
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

	openstackContext := openstack.New()

	return &platformHelper{
		openshiftContext,
		openstackContext,
	}, nil
}
