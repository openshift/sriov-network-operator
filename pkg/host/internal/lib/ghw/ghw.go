package ghw

import (
	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/pci"
)

func New() GHWLib {
	return &libWrapper{}
}

//go:generate ../../../../../bin/mockgen -destination mock/mock_ghw.go -source ghw.go
type GHWLib interface {
	// PCI returns a pointer to an Info that provide methods to access info about devices
	PCI() (*pci.Info, error)

	// CPU returns a pointer to an Info that provide methods to access info about devices
	CPU() (*cpu.Info, error)
}

type libWrapper struct{}

// PCI returns a pointer to an Info that provide methods to access info about devices
func (w *libWrapper) PCI() (*pci.Info, error) {
	return ghw.PCI()
}

func (w *libWrapper) CPU() (*cpu.Info, error) {
	return ghw.CPU()
}
