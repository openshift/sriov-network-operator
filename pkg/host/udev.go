package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type UdevInterface interface {
	// WriteSwitchdevConfFile writes the needed switchdev configuration files for HW offload support
	WriteSwitchdevConfFile(*sriovnetworkv1.SriovNetworkNodeState, map[string]bool) (bool, error)
	// PrepareNMUdevRule creates the needed udev rules to disable NetworkManager from
	// our managed SR-IOV virtual functions
	PrepareNMUdevRule([]string) error
	// AddUdevRule adds a specific udev rule to the system
	AddUdevRule(string) error
	// RemoveUdevRule removes a udev rule from the system
	RemoveUdevRule(string) error
}

type udev struct {
	utilsHelper utils.CmdInterface
}

func newUdevInterface(utilsHelper utils.CmdInterface) UdevInterface {
	return &udev{utilsHelper: utilsHelper}
}

type config struct {
	Interfaces []sriovnetworkv1.Interface `json:"interfaces"`
}

func (u *udev) PrepareNMUdevRule(supportedVfIds []string) error {
	log.Log.V(2).Info("PrepareNMUdevRule()")
	filePath := filepath.Join(vars.FilesystemRoot, consts.HostUdevRulesFolder, "10-nm-unmanaged.rules")

	// remove the old unmanaged rules file
	if _, err := os.Stat(filePath); err == nil {
		err = os.Remove(filePath)
		if err != nil {
			log.Log.Error(err, "failed to remove the network manager global unmanaged rule",
				"path", filePath)
		}
	}

	// create the pf finder script for udev rules
	stdout, stderr, err := u.utilsHelper.RunCommand("/bin/bash", filepath.Join(vars.FilesystemRoot, consts.UdevDisableNM))
	if err != nil {
		log.Log.Error(err, "PrepareNMUdevRule(): failed to prepare nmUdevRule", "stderr", stderr)
		return err
	}
	log.Log.V(2).Info("PrepareNMUdevRule()", "stdout", stdout)

	//save the device list to use for udev rules
	vars.SupportedVfIds = supportedVfIds
	return nil
}

func (u *udev) WriteSwitchdevConfFile(newState *sriovnetworkv1.SriovNetworkNodeState, pfsToSkip map[string]bool) (bool, error) {
	cfg := config{}
	for _, iface := range newState.Spec.Interfaces {
		for _, ifaceStatus := range newState.Status.Interfaces {
			if iface.PciAddress != ifaceStatus.PciAddress {
				continue
			}

			if skip := pfsToSkip[iface.PciAddress]; !skip {
				continue
			}

			if iface.NumVfs > 0 {
				var vfGroups []sriovnetworkv1.VfGroup = nil
				ifc, err := sriovnetworkv1.FindInterface(newState.Spec.Interfaces, iface.Name)
				if err != nil {
					log.Log.Error(err, "WriteSwitchdevConfFile(): fail find interface")
				} else {
					vfGroups = ifc.VfGroups
				}
				i := sriovnetworkv1.Interface{
					// Not passing all the contents, since only NumVfs and EswitchMode can be configured by configure-switchdev.sh currently.
					Name:       iface.Name,
					PciAddress: iface.PciAddress,
					NumVfs:     iface.NumVfs,
					Mtu:        iface.Mtu,
					VfGroups:   vfGroups,
				}

				if iface.EswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
					i.EswitchMode = iface.EswitchMode
				}
				cfg.Interfaces = append(cfg.Interfaces, i)
			}
		}
	}
	_, err := os.Stat(consts.SriovHostSwitchDevConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			if len(cfg.Interfaces) == 0 {
				return false, nil
			}

			// TODO: refactor this function to allow using vars.FilesystemRoot for unit-tests
			// Create the sriov-operator folder on the host if it doesn't exist
			if _, err := os.Stat(consts.Host + consts.SriovConfBasePath); os.IsNotExist(err) {
				err = os.Mkdir(consts.Host+consts.SriovConfBasePath, os.ModeDir)
				if err != nil {
					log.Log.Error(err, "WriteConfFile(): failed to create sriov-operator folder")
					return false, err
				}
			}

			log.Log.V(2).Info("WriteSwitchdevConfFile(): file not existed, create it")
			_, err = os.Create(consts.SriovHostSwitchDevConfPath)
			if err != nil {
				log.Log.Error(err, "WriteSwitchdevConfFile(): failed to create file")
				return false, err
			}
		} else {
			return false, err
		}
	}
	oldContent, err := os.ReadFile(consts.SriovHostSwitchDevConfPath)
	if err != nil {
		log.Log.Error(err, "WriteSwitchdevConfFile(): failed to read file")
		return false, err
	}
	var newContent []byte
	if len(cfg.Interfaces) != 0 {
		newContent, err = json.Marshal(cfg)
		if err != nil {
			log.Log.Error(err, "WriteSwitchdevConfFile(): fail to marshal config")
			return false, err
		}
	}

	if bytes.Equal(newContent, oldContent) {
		log.Log.V(2).Info("WriteSwitchdevConfFile(): no update")
		return false, nil
	}
	log.Log.V(2).Info("WriteSwitchdevConfFile(): write to switchdev.conf", "content", newContent)
	err = os.WriteFile(consts.SriovHostSwitchDevConfPath, newContent, 0644)
	if err != nil {
		log.Log.Error(err, "WriteSwitchdevConfFile(): failed to write file")
		return false, err
	}
	return true, nil
}

func (u *udev) AddUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("AddUdevRule()", "device", pfPciAddress)
	pathFile := filepath.Join(vars.FilesystemRoot, consts.UdevRulesFolder)
	udevRuleContent := fmt.Sprintf(consts.NMUdevRule, strings.Join(vars.SupportedVfIds, "|"), pfPciAddress)

	err := os.MkdirAll(pathFile, os.ModePerm)
	if err != nil && !os.IsExist(err) {
		log.Log.Error(err, "AddUdevRule(): failed to create dir", "path", pathFile)
		return err
	}

	filePath := path.Join(pathFile, fmt.Sprintf("10-nm-disable-%s.rules", pfPciAddress))
	// if the file does not exist or if oldContent != newContent
	// write to file and create it if it doesn't exist
	err = os.WriteFile(filePath, []byte(udevRuleContent), 0666)
	if err != nil {
		log.Log.Error(err, "AddUdevRule(): fail to write file", "path", filePath)
		return err
	}
	return nil
}

func (u *udev) RemoveUdevRule(pfPciAddress string) error {
	pathFile := filepath.Join(vars.FilesystemRoot, consts.UdevRulesFolder)
	filePath := path.Join(pathFile, fmt.Sprintf("10-nm-disable-%s.rules", pfPciAddress))
	err := os.Remove(filePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
