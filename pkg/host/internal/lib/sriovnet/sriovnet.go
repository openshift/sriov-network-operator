package sriovnet

import (
	"github.com/k8snetworkplumbingwg/sriovnet"
)

func New() SriovnetLib {
	return &libWrapper{}
}

//go:generate ../../../../../bin/mockgen -destination mock/mock_sriovnet.go -source sriovnet.go
type SriovnetLib interface {
	// GetVfRepresentor returns representor name for VF device
	GetVfRepresentor(uplink string, vfIndex int) (string, error)
}

type libWrapper struct{}

// GetVfRepresentor returns representor name for VF device
func (w *libWrapper) GetVfRepresentor(pfName string, vfIndex int) (string, error) {
	return sriovnet.GetVfRepresentor(pfName, vfIndex)
}
