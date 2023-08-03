package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

const (
	SriovConfBasePath = "/etc/sriov-operator"
	PfAppliedConfig   = SriovConfBasePath + "/pci"
)

// Contains all the file storing on the host
//
//go:generate ../../bin/mockgen -destination mock/mock_store.go -source store.go
type StoreManagerInterface interface {
	ClearPCIAddressFolder() error
	SaveLastPfAppliedStatus(pciAddress string, PfInfo *sriovnetworkv1.Interface) error
	LoadPfsStatus(pciAddress string) (*sriovnetworkv1.Interface, bool, error)
}

type StoreManager struct {
	RunOnHost bool
}

// NewStoreManager: create the initial folders needed to store the info about the PF
// and return a storeManager struct that implements the StoreManagerInterface interface
func NewStoreManager(runOnHost bool) (StoreManagerInterface, error) {
	if err := createOperatorConfigFolderIfNeeded(runOnHost); err != nil {
		return nil, err
	}

	return &StoreManager{runOnHost}, nil
}

// createOperatorConfigFolderIfNeeded: create the operator base folder on the host
// together with the pci folder to save the PF status objects as json files
func createOperatorConfigFolderIfNeeded(runOnHost bool) error {
	hostExtension := getHostExtension(runOnHost)
	SriovConfBasePathUse := filepath.Join(hostExtension, SriovConfBasePath)
	_, err := os.Stat(SriovConfBasePathUse)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(SriovConfBasePathUse, os.ModeDir)
			if err != nil {
				return fmt.Errorf("failed to create the sriov config folder on host in path %s: %v", SriovConfBasePathUse, err)
			}
		} else {
			return fmt.Errorf("failed to check if the sriov config folder on host in path %s exist: %v", SriovConfBasePathUse, err)
		}
	}

	PfAppliedConfigUse := filepath.Join(hostExtension, PfAppliedConfig)
	_, err = os.Stat(PfAppliedConfigUse)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(PfAppliedConfigUse, os.ModeDir)
			if err != nil {
				return fmt.Errorf("failed to create the pci folder on host in path %s: %v", PfAppliedConfigUse, err)
			}
		} else {
			return fmt.Errorf("failed to check if the pci folder on host in path %s exist: %v", PfAppliedConfigUse, err)
		}
	}

	return nil
}

// ClearPCIAddressFolder: removes all the PFs storage information
func (s *StoreManager) ClearPCIAddressFolder() error {
	hostExtension := getHostExtension(s.RunOnHost)
	PfAppliedConfigUse := filepath.Join(hostExtension, PfAppliedConfig)
	_, err := os.Stat(PfAppliedConfigUse)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to check the pci address folder path %s", PfAppliedConfigUse)
	}

	err = os.RemoveAll(PfAppliedConfigUse)
	if err != nil {
		return fmt.Errorf("failed to remove the PCI address folder on path %s: %v", PfAppliedConfigUse, err)
	}

	err = os.Mkdir(PfAppliedConfigUse, os.ModeDir)
	if err != nil {
		return fmt.Errorf("failed to create the pci folder on host in path %s: %v", PfAppliedConfigUse, err)
	}

	return nil
}

// SaveLastPfAppliedStatus will save the PF object as a json into the /etc/sriov-operator/pci/<pci-address>
// this function must be called after running the chroot function
func (s *StoreManager) SaveLastPfAppliedStatus(pciAddress string, PfInfo *sriovnetworkv1.Interface) error {
	data, err := json.Marshal(PfInfo)
	if err != nil {
		glog.Errorf("failed to marshal PF status %+v: %v", *PfInfo, err)
		return err
	}

	hostExtension := getHostExtension(s.RunOnHost)
	pathFile := filepath.Join(hostExtension, PfAppliedConfig, pciAddress)
	err = os.WriteFile(pathFile, data, 0644)
	return err
}

// LoadPfsStatus convert the /etc/sriov-operator/pci/<pci-address> json to pfstatus
// returns false if the file doesn't exist.
func (s *StoreManager) LoadPfsStatus(pciAddress string) (*sriovnetworkv1.Interface, bool, error) {
	hostExtension := getHostExtension(s.RunOnHost)
	pathFile := filepath.Join(hostExtension, PfAppliedConfig, pciAddress)
	pfStatus := &sriovnetworkv1.Interface{}
	data, err := os.ReadFile(pathFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		glog.Errorf("failed to read PF status from path %s: %v", pathFile, err)
		return nil, false, err
	}

	err = json.Unmarshal(data, pfStatus)
	if err != nil {
		glog.Errorf("failed to unmarshal PF status %s: %v", data, err)
		return nil, false, err
	}

	return pfStatus, true, nil
}

func getHostExtension(runOnHost bool) string {
	if !runOnHost {
		return path.Join(FilesystemRoot, "/host")
	}
	return FilesystemRoot
}
