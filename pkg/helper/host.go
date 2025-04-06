package helper

import (
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/store"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	mlx "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vendors/mellanox"
)

//go:generate ../../bin/mockgen -destination mock/mock_helper.go -source host.go
type HostHelpersInterface interface {
	utils.CmdInterface
	host.HostManagerInterface
	store.ManagerInterface
	mlx.MellanoxInterface
}

type hostHelpers struct {
	utils.CmdInterface
	host.HostManagerInterface
	store.ManagerInterface
	mlx.MellanoxInterface
}

func NewDefaultHostHelpers() (HostHelpersInterface, error) {
	utilsHelper := utils.New()
	mlxHelper := mlx.New(utilsHelper)
	hostManager, err := host.NewHostManager(utilsHelper)
	if err != nil {
		log.Log.Error(err, "failed to create host manager")
		return nil, err
	}
	storeManager, err := store.NewManager()
	if err != nil {
		log.Log.Error(err, "failed to create store manager")
		return nil, err
	}

	return &hostHelpers{
		utilsHelper,
		hostManager,
		storeManager,
		mlxHelper}, nil
}
