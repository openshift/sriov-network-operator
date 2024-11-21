package udev

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

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

// PrepareVFRepUdevRule creates a script which helps to configure representor name for the VF
func (u *udev) PrepareVFRepUdevRule() error {
	log.Log.V(2).Info("PrepareVFRepUdevRule()")
	targetPath := filepath.Join(vars.FilesystemRoot, consts.HostUdevFolder, filepath.Base(consts.UdevRepName))
	data, err := os.ReadFile(filepath.Join(vars.FilesystemRoot, consts.UdevRepName))
	if err != nil {
		log.Log.Error(err, "PrepareVFRepUdevRule(): failed to read source for representor name UDEV script")
		return err
	}
	if err := os.WriteFile(targetPath, data, 0755); err != nil {
		log.Log.Error(err, "PrepareVFRepUdevRule(): failed to write representor name UDEV script")
		return err
	}
	if err := os.Chmod(targetPath, 0755); err != nil {
		log.Log.Error(err, "PrepareVFRepUdevRule(): failed to set permissions on representor name UDEV script")
		return err
	}
	return nil
}

// AddDisableNMUdevRule adds udev rule that disables NetworkManager for VFs on the concrete PF:
func (u *udev) AddDisableNMUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("AddDisableNMUdevRule()", "device", pfPciAddress)
	udevRuleContent := fmt.Sprintf(consts.NMUdevRule, strings.Join(vars.SupportedVfIds, "|"), pfPciAddress)
	return u.addUdevRule(pfPciAddress, "10-nm-disable", udevRuleContent)
}

// RemoveDisableNMUdevRule removes udev rule that disables NetworkManager for VFs on the concrete PF
func (u *udev) RemoveDisableNMUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("RemoveDisableNMUdevRule()", "device", pfPciAddress)
	return u.removeUdevRule(pfPciAddress, "10-nm-disable")
}

// AddPersistPFNameUdevRule add udev rule that preserves PF name after switching to switchdev mode
func (u *udev) AddPersistPFNameUdevRule(pfPciAddress, pfName string) error {
	log.Log.V(2).Info("AddPersistPFNameUdevRule()", "device", pfPciAddress)
	udevRuleContent := fmt.Sprintf(consts.PFNameUdevRule, pfPciAddress, pfName)
	return u.addUdevRule(pfPciAddress, "10-pf-name", udevRuleContent)
}

// RemovePersistPFNameUdevRule removes udev rule that preserves PF name after switching to switchdev mode
func (u *udev) RemovePersistPFNameUdevRule(pfPciAddress string) error {
	log.Log.V(2).Info("RemovePersistPFNameUdevRule()", "device", pfPciAddress)
	return u.removeUdevRule(pfPciAddress, "10-pf-name")
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

// LoadUdevRules triggers udev rules for network subsystem
func (u *udev) LoadUdevRules() error {
	log.Log.V(2).Info("LoadUdevRules()")
	udevAdmTool := "udevadm"
	_, stderr, err := u.utilsHelper.RunCommand(udevAdmTool, "control", "--reload-rules")
	if err != nil {
		log.Log.Error(err, "LoadUdevRules(): failed to reload rules", "error", stderr)
		return err
	}
	_, stderr, err = u.utilsHelper.RunCommand(udevAdmTool, "trigger", "--action", "add", "--attr-match", "subsystem=net")
	if err != nil {
		log.Log.Error(err, "LoadUdevRules(): failed to trigger rules", "error", stderr)
		return err
	}
	return nil
}

// WaitUdevEventsProcessed calls `udevadm settle“ with provided timeout
// The command watches the udev event queue, and exits if all current events are handled.
func (u *udev) WaitUdevEventsProcessed(timeout int) error {
	log.Log.V(2).Info("WaitUdevEventsProcessed()")
	_, stderr, err := u.utilsHelper.RunCommand("udevadm", "settle", "-t", strconv.Itoa(timeout))
	if err != nil {
		log.Log.Error(err, "WaitUdevEventsProcessed(): failed to wait for udev rules to process", "error", stderr, "timeout", timeout)
		return err
	}
	return nil
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
