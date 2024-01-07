package helper

import (
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	mlx "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vendors/mellanox"
)

//go:generate ../../bin/mockgen -destination mock/mock_helper.go -source host.go
type HostHelpersInterface interface {
	utils.CmdInterface
	host.HostManagerInterface
	host.StoreManagerInterface
	mlx.MellanoxInterface
}

type hostHelpers struct {
	utils.CmdInterface
	host.HostManagerInterface
	host.StoreManagerInterface
	mlx.MellanoxInterface
}

// Use for unit tests
func NewHostHelpers(utilsHelper utils.CmdInterface,
	hostManager host.HostManagerInterface,
	storeManager host.StoreManagerInterface,
	mlxHelper mlx.MellanoxInterface) HostHelpersInterface {
	return &hostHelpers{utilsHelper, hostManager, storeManager, mlxHelper}
}

func NewDefaultHostHelpers() (HostHelpersInterface, error) {
	utilsHelper := utils.New()
	mlxHelper := mlx.New(utilsHelper)
	hostManager := host.NewHostManager(utilsHelper)
	storeManager, err := host.NewStoreManager()
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
