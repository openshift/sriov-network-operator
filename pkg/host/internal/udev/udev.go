package udev

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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type udev struct {
	utilsHelper utils.CmdInterface
}

func New(utilsHelper utils.CmdInterface) types.UdevInterface {
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

// AddUdevRule adds a udev rule that disables network-manager for VFs on the concrete PF
func (u *udev) AddUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("AddUdevRule()", "device", pfPciAddress)
	udevRuleContent := fmt.Sprintf(consts.NMUdevRule, strings.Join(vars.SupportedVfIds, "|"), pfPciAddress)
	return u.addUdevRule(pfPciAddress, "10-nm-disable", udevRuleContent)
}

// RemoveUdevRule removes a udev rule that disables network-manager for VFs on the concrete PF
func (u *udev) RemoveUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("RemoveUdevRule()", "device", pfPciAddress)
	return u.removeUdevRule(pfPciAddress, "10-nm-disable")
}

// AddVfRepresentorUdevRule adds udev rule that renames VF representors on the concrete PF
func (u *udev) AddVfRepresentorUdevRule(pfPciAddress, pfName, pfSwitchID, pfSwitchPort string) error {
	log.Log.V(2).Info("AddVfRepresentorUdevRule()",
		"device", pfPciAddress, "name", pfName, "switch", pfSwitchID, "port", pfSwitchPort)
	udevRuleContent := fmt.Sprintf(consts.SwitchdevUdevRule, pfSwitchID, strings.TrimPrefix(pfSwitchPort, "p"), pfName)
	return u.addUdevRule(pfPciAddress, "20-switchdev", udevRuleContent)
}

// RemoveVfRepresentorUdevRule removes udev rule that renames VF representors on the concrete PF
func (u *udev) RemoveVfRepresentorUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("RemoveVfRepresentorUdevRule()", "device", pfPciAddress)
	return u.removeUdevRule(pfPciAddress, "20-switchdev")
}

func (u *udev) addUdevRule(pfPciAddress, ruleName, ruleContent string) error {
	log.Log.V(2).Info("addUdevRule()", "device", pfPciAddress, "rule", ruleName)
	rulePath := u.getRuleFolderPath()
	err := os.MkdirAll(rulePath, os.ModePerm)
	if err != nil && !os.IsExist(err) {
		log.Log.Error(err, "ensureUdevRulePathExist(): failed to create dir", "path", rulePath)
		return err
	}
	filePath := u.getRulePathForPF(ruleName, pfPciAddress)
	if err := os.WriteFile(filePath, []byte(ruleContent), 0666); err != nil {
		log.Log.Error(err, "addUdevRule(): fail to write file", "path", filePath)
		return err
	}
	return nil
}

func (u *udev) removeUdevRule(pfPciAddress, ruleName string) error {
	log.Log.V(2).Info("removeUdevRule()", "device", pfPciAddress, "rule", ruleName)
	rulePath := u.getRulePathForPF(ruleName, pfPciAddress)
	err := os.Remove(rulePath)
	if err != nil && !os.IsNotExist(err) {
		log.Log.Error(err, "removeUdevRule(): fail to remove rule file", "path", rulePath)
		return err
	}
	return nil
}

func (u *udev) getRuleFolderPath() string {
	return filepath.Join(vars.FilesystemRoot, consts.UdevRulesFolder)
}

func (u *udev) getRulePathForPF(ruleName, pfPciAddress string) string {
	return path.Join(u.getRuleFolderPath(), fmt.Sprintf("%s-%s.rules", ruleName, pfPciAddress))
}
