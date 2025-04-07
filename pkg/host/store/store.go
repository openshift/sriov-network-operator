package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// Contains all the file storing on the host
//
//go:generate ../../../bin/mockgen -destination mock/mock_store.go -source store.go
type ManagerInterface interface {
	ClearPCIAddressFolder() error
	SaveLastPfAppliedStatus(PfInfo *sriovnetworkv1.Interface) error
	RemovePfAppliedStatus(pciAddress string) error
	LoadPfsStatus(pciAddress string) (*sriovnetworkv1.Interface, bool, error)

	GetCheckPointNodeState() (*sriovnetworkv1.SriovNetworkNodeState, error)
	WriteCheckpointFile(*sriovnetworkv1.SriovNetworkNodeState) error
}

type manager struct{}

// NewManager: create the initial folders needed to store the info about the PF
// and return a manager struct that implements the ManagerInterface interface
func NewManager() (ManagerInterface, error) {
	if err := createOperatorConfigFolderIfNeeded(); err != nil {
		return nil, err
	}

	return &manager{}, nil
}

// createOperatorConfigFolderIfNeeded: create the operator base folder on the host
// together with the pci folder to save the PF status objects as json files
func createOperatorConfigFolderIfNeeded() error {
	hostExtension := utils.GetHostExtension()
	SriovConfBasePathUse := filepath.Join(hostExtension, consts.SriovConfBasePath)
	_, err := os.Stat(SriovConfBasePathUse)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(SriovConfBasePathUse, 0777)
			if err != nil {
				return fmt.Errorf("failed to create the sriov config folder on host in path %s: %v", SriovConfBasePathUse, err)
			}
		} else {
			return fmt.Errorf("failed to check if the sriov config folder on host in path %s exist: %v", SriovConfBasePathUse, err)
		}
	}

	PfAppliedConfigUse := filepath.Join(hostExtension, consts.PfAppliedConfig)
	_, err = os.Stat(PfAppliedConfigUse)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(PfAppliedConfigUse, 0777)
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
func (s *manager) ClearPCIAddressFolder() error {
	hostExtension := utils.GetHostExtension()
	PfAppliedConfigUse := filepath.Join(hostExtension, consts.PfAppliedConfig)
	_, err := os.Stat(PfAppliedConfigUse)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to check the pci address folder path %s: %v", PfAppliedConfigUse, err)
	}

	err = os.RemoveAll(PfAppliedConfigUse)
	if err != nil {
		return fmt.Errorf("failed to remove the PCI address folder on path %s: %v", PfAppliedConfigUse, err)
	}

	err = os.Mkdir(PfAppliedConfigUse, 0777)
	if err != nil {
		return fmt.Errorf("failed to create the pci folder on host in path %s: %v", PfAppliedConfigUse, err)
	}

	return nil
}

// SaveLastPfAppliedStatus will save the PF object as a json into the /etc/sriov-operator/pci/<pci-address>
// this function must be called after running the chroot function
func (s *manager) SaveLastPfAppliedStatus(PfInfo *sriovnetworkv1.Interface) error {
	data, err := json.Marshal(PfInfo)
	if err != nil {
		log.Log.Error(err, "failed to marshal PF status", "status", *PfInfo)
		return err
	}

	hostExtension := utils.GetHostExtension()
	pathFile := filepath.Join(hostExtension, consts.PfAppliedConfig, PfInfo.PciAddress)
	err = os.WriteFile(pathFile, data, 0644)
	return err
}

func (s *manager) RemovePfAppliedStatus(pciAddress string) error {
	hostExtension := utils.GetHostExtension()
	pathFile := filepath.Join(hostExtension, consts.PfAppliedConfig, pciAddress)
	err := os.RemoveAll(pathFile)
	if err != nil {
		log.Log.Error(err, "failed to remove PF status", "pathFile", pathFile)
		return err
	}
	return nil
}

// LoadPfsStatus convert the /etc/sriov-operator/pci/<pci-address> json to pfstatus
// returns false if the file doesn't exist.
func (s *manager) LoadPfsStatus(pciAddress string) (*sriovnetworkv1.Interface, bool, error) {
	hostExtension := utils.GetHostExtension()
	pathFile := filepath.Join(hostExtension, consts.PfAppliedConfig, pciAddress)
	pfStatus := &sriovnetworkv1.Interface{}
	data, err := os.ReadFile(pathFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		log.Log.Error(err, "failed to read PF status", "path", pathFile)
		return nil, false, err
	}

	err = json.Unmarshal(data, pfStatus)
	if err != nil {
		log.Log.Error(err, "failed to unmarshal PF status", "data", string(data))
		return nil, false, err
	}

	return pfStatus, true, nil
}

func (s *manager) GetCheckPointNodeState() (*sriovnetworkv1.SriovNetworkNodeState, error) {
	log.Log.Info("getCheckPointNodeState()")
	configdir := filepath.Join(vars.Destdir, consts.CheckpointFileName)
	file, err := os.OpenFile(configdir, os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	if err = json.NewDecoder(file).Decode(&sriovnetworkv1.InitialState); err != nil {
		return nil, err
	}

	return &sriovnetworkv1.InitialState, nil
}

func (s *manager) WriteCheckpointFile(ns *sriovnetworkv1.SriovNetworkNodeState) error {
	configdir := filepath.Join(vars.Destdir, consts.CheckpointFileName)
	file, err := os.OpenFile(configdir, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	log.Log.Info("WriteCheckpointFile(): try to decode the checkpoint file")
	if err = json.NewDecoder(file).Decode(&sriovnetworkv1.InitialState); err != nil {
		log.Log.Error(err, "WriteCheckpointFile(): fail to decode, writing new file instead")
		log.Log.Info("WriteCheckpointFile(): write checkpoint file")
		if err = file.Truncate(0); err != nil {
			return err
		}
		if _, err = file.Seek(0, 0); err != nil {
			return err
		}
		if err = json.NewEncoder(file).Encode(*ns); err != nil {
			return err
		}
		sriovnetworkv1.InitialState = *ns
	}
	return nil
}
