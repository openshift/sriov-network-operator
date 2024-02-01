package dputils

import (
	dputils "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/utils"
)

func New() DPUtilsLib {
	return &libWrapper{}
}

//go:generate ../../../../../bin/mockgen -destination mock/mock_dputils.go -source dputils.go
type DPUtilsLib interface {
	// GetNetNames returns host net interface names as string for a PCI device from its pci address
	GetNetNames(pciAddr string) ([]string, error)
	// GetDriverName returns current driver attached to a pci device from its pci address
	GetDriverName(pciAddr string) (string, error)
	// GetVFID returns VF ID index (within specific PF) based on PCI address
	GetVFID(pciAddr string) (vfID int, err error)
	// IsSriovVF check if a pci device has link to a PF
	IsSriovVF(pciAddr string) bool
	// IsSriovPF check if a pci device SRIOV capable given its pci address
	IsSriovPF(pciAddr string) bool
	// GetSriovVFcapacity returns SRIOV VF capacity
	GetSriovVFcapacity(pf string) int
	// GetVFconfigured returns number of VF configured for a PF
	GetVFconfigured(pf string) int
	// SriovConfigured returns true if sriov_numvfs reads > 0 else false
	SriovConfigured(addr string) bool
	// GetVFList returns a List containing PCI addr for all VF discovered in a given PF
	GetVFList(pf string) (vfList []string, err error)
}

type libWrapper struct{}

// GetNetNames returns host net interface names as string for a PCI device from its pci address
func (w *libWrapper) GetNetNames(pciAddr string) ([]string, error) {
	return dputils.GetNetNames(pciAddr)
}

// GetDriverName returns current driver attached to a pci device from its pci address
func (w *libWrapper) GetDriverName(pciAddr string) (string, error) {
	return dputils.GetDriverName(pciAddr)
}

// GetVFID returns VF ID index (within specific PF) based on PCI address
func (w *libWrapper) GetVFID(pciAddr string) (vfID int, err error) {
	return dputils.GetVFID(pciAddr)
}

// IsSriovVF check if a pci device has link to a PF
func (w *libWrapper) IsSriovVF(pciAddr string) bool {
	return dputils.IsSriovVF(pciAddr)
}

// IsSriovPF check if a pci device SRIOV capable given its pci address
func (w *libWrapper) IsSriovPF(pciAddr string) bool {
	return dputils.IsSriovPF(pciAddr)
}

// GetSriovVFcapacity returns SRIOV VF capacity
func (w *libWrapper) GetSriovVFcapacity(pf string) int {
	return dputils.GetSriovVFcapacity(pf)
}

// GetVFconfigured returns number of VF configured for a PF
func (w *libWrapper) GetVFconfigured(pf string) int {
	return dputils.GetVFconfigured(pf)
}

// SriovConfigured returns true if sriov_numvfs reads > 0 else false
func (w *libWrapper) SriovConfigured(addr string) bool {
	return dputils.SriovConfigured(addr)
}

// GetVFList returns a List containing PCI addr for all VF discovered in a given PF
func (w *libWrapper) GetVFList(pf string) (vfList []string, err error) {
	return dputils.GetVFList(pf)
}
