package govdpa

import (
	"github.com/k8snetworkplumbingwg/govdpa/pkg/kvdpa"
)

func New() GoVdpaLib {
	return &libWrapper{}
}

type VdpaDevice interface {
	kvdpa.VdpaDevice
}

//go:generate ../../../../../bin/mockgen -destination mock/mock_govdpa.go -source govdpa.go
type GoVdpaLib interface {
	// GetVdpaDevice returns the vdpa device information by a vdpa device name
	GetVdpaDevice(vdpaDeviceName string) (VdpaDevice, error)
	// AddVdpaDevice adds a new vdpa device to the given management device
	AddVdpaDevice(mgmtDeviceName string, vdpaDeviceName string) error
	// DeleteVdpaDevice deletes a vdpa device
	DeleteVdpaDevice(vdpaDeviceName string) error
}

type libWrapper struct{}

// GetVdpaDevice returns the vdpa device information by a vdpa device name
func (w *libWrapper) GetVdpaDevice(name string) (VdpaDevice, error) {
	return kvdpa.GetVdpaDevice(name)
}

// AddVdpaDevice adds a new vdpa device to the given management device
func (w *libWrapper) AddVdpaDevice(mgmtDeviceName string, vdpaDeviceName string) error {
	return kvdpa.AddVdpaDevice(mgmtDeviceName, vdpaDeviceName)
}

// DeleteVdpaDevice deletes a vdpa device
func (w *libWrapper) DeleteVdpaDevice(name string) error {
	return kvdpa.DeleteVdpaDevice(name)
}
