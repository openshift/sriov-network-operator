/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package systemd

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type systemd struct{}

func New() types.SystemdInterface {
	return &systemd{}
}

// ReadConfFile reads the SR-IOV config file from the host
// Unmarshal YAML content into SriovConfig object
func (s *systemd) ReadConfFile() (spec *types.SriovConfig, err error) {
	rawConfig, err := os.ReadFile(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(rawConfig, &spec)

	return spec, err
}

// WriteConfFile generates or updates a SriovNetwork configuration file based on the provided state.
// It creates the necessary directory structure if the file doesn't exist,
// reads the existing content to check for changes, and writes new content only when needed.
func (s *systemd) WriteConfFile(newState *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	newFile := false
	sriovConfig := &types.SriovConfig{
		newState.Spec,
		vars.DevMode,
		vars.PlatformType,
		vars.ManageSoftwareBridges,
		vars.OVSDBSocketPath,
	}

	_, err := os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
	if err != nil {
		if os.IsNotExist(err) {
			// Create the sriov-operator folder on the host if it doesn't exist
			if _, err := os.Stat(utils.GetHostExtensionPath(consts.SriovConfBasePath)); os.IsNotExist(err) {
				err = os.Mkdir(utils.GetHostExtensionPath(consts.SriovConfBasePath), os.ModeDir)
				if err != nil {
					log.Log.Error(err, "WriteConfFile(): fail to create sriov-operator folder",
						"path", utils.GetHostExtensionPath(consts.SriovConfBasePath))
					return false, err
				}
			}

			log.Log.V(2).Info("WriteConfFile(): file not existed, create it",
				"path", utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
			_, err = os.Create(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
			if err != nil {
				log.Log.Error(err, "WriteConfFile(): fail to create file")
				return false, err
			}
			newFile = true
		} else {
			return false, err
		}
	}

	oldContent, err := os.ReadFile(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
	if err != nil {
		log.Log.Error(err, "WriteConfFile(): fail to read file", "path", utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
		return false, err
	}

	oldContentObj := &types.SriovConfig{}
	err = yaml.Unmarshal(oldContent, oldContentObj)
	if err != nil {
		log.Log.Error(err, "WriteConfFile(): fail to unmarshal old file")
		return false, err
	}

	var newContent []byte
	newContent, err = yaml.Marshal(sriovConfig)
	if err != nil {
		log.Log.Error(err, "WriteConfFile(): fail to marshal sriov config")
		return false, err
	}

	if bytes.Equal(newContent, oldContent) {
		log.Log.V(2).Info("WriteConfFile(): no update")
		return false, nil
	}
	log.Log.V(2).Info("WriteConfFile(): old and new configuration are not equal",
		"old", string(oldContent), "new", string(newContent))

	log.Log.V(2).Info("WriteConfFile(): write content to file",
		"content", newContent, "path", utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
	err = os.WriteFile(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath), newContent, 0644)
	if err != nil {
		log.Log.Error(err, "WriteConfFile(): fail to write file")
		return false, err
	}

	// this will be used to mark the first time we create this file.
	// this helps to avoid the first reboot after installation
	if newFile && len(sriovConfig.Spec.Interfaces) == 0 {
		log.Log.V(2).Info("WriteConfFile(): first file creation and no interfaces to configure returning reboot false")
		return false, nil
	}

	return true, nil
}

// WriteSriovResult writes SR-IOV results to the host.
// It creates the file if it doesn't exist
func (s *systemd) WriteSriovResult(result *types.SriovResult) error {
	_, err := os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("WriteSriovResult(): file not existed, create it")
			_, err = os.Create(utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
			if err != nil {
				log.Log.Error(err, "WriteSriovResult(): failed to create sriov result file", "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
				return err
			}
		} else {
			log.Log.Error(err, "WriteSriovResult(): failed to check sriov result file", "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
			return err
		}
	}

	out, err := yaml.Marshal(result)
	if err != nil {
		log.Log.Error(err, "WriteSriovResult(): failed to marshal sriov result")
		return err
	}

	log.Log.V(2).Info("WriteSriovResult(): write results",
		"content", string(out), "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
	err = os.WriteFile(utils.GetHostExtensionPath(consts.SriovSystemdResultPath), out, 0644)
	if err != nil {
		log.Log.Error(err, "WriteSriovResult(): failed to write sriov result file", "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
		return err
	}

	return nil
}

// ReadSriovResult reads and parses the sriov result file from the host.
// The function first checks if the result file exists. If it doesn't, it returns nil with a success flag of false and no error.
// If the file exists, it reads its contents and attempts to unmarshal the YAML data into the SriovResult struct.
func (s *systemd) ReadSriovResult() (*types.SriovResult, bool, error) {
	_, err := os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("ReadSriovResult(): file does not exist")
			return nil, false, nil
		} else {
			log.Log.Error(err, "ReadSriovResult(): failed to check sriov result file", "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
			return nil, false, err
		}
	}

	rawConfig, err := os.ReadFile(utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
	if err != nil {
		log.Log.Error(err, "ReadSriovResult(): failed to read sriov result file", "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
		return nil, false, err
	}

	result := &types.SriovResult{}
	err = yaml.Unmarshal(rawConfig, &result)
	if err != nil {
		log.Log.Error(err, "ReadSriovResult(): failed to unmarshal sriov result file", "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
		return nil, false, err
	}
	return result, true, err
}

// RemoveSriovResult: Removes the Sriov result file from the host.
func (s *systemd) RemoveSriovResult() error {
	err := os.Remove(utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("RemoveSriovResult(): result file not found")
			return nil
		}
		log.Log.Error(err, "RemoveSriovResult(): failed to remove sriov result file", "path", utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
		return err
	}
	log.Log.V(2).Info("RemoveSriovResult(): result file removed")
	return nil
}

// WriteSriovSupportedNics() creates or updates a file containing the list of supported SR-IOV NIC IDs
// If the file does not exist, it will create it
// It reads from sriovnetworkv1.NicIDMap to gather the list of NIC identifiers
func (s *systemd) WriteSriovSupportedNics() error {
	_, err := os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("WriteSriovSupportedNics(): file does not exist, create it")
			_, err = os.Create(utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
			if err != nil {
				log.Log.Error(err, "WriteSriovSupportedNics(): failed to create sriov supporter nics ids file",
					"path", utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
				return err
			}
		} else {
			log.Log.Error(err, "WriteSriovSupportedNics(): failed to check sriov supported nics ids file", "path", utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
			return err
		}
	}

	rawNicList := []byte{}
	for _, line := range sriovnetworkv1.NicIDMap {
		rawNicList = append(rawNicList, []byte(fmt.Sprintf("%s\n", line))...)
	}

	err = os.WriteFile(utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath), rawNicList, 0644)
	if err != nil {
		log.Log.Error(err, "WriteSriovSupportedNics(): failed to write sriov supported nics ids file",
			"path", utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
		return err
	}

	return nil
}

// ReadSriovSupportedNics reads the list of SR-IOV supported network interface cards (NICs) from the host.
// It returns a slice of strings where each string represents a line from the file,
// with each line corresponding to an SR-IOV supported NIC. If the file does not exist, it returns nil and an error.
// If there is an error reading the file, it returns the error along with the file path for debugging purposes.
func (s *systemd) ReadSriovSupportedNics() ([]string, error) {
	_, err := os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("ReadSriovSupportedNics(): file does not exist, return empty result")
			return nil, err
		} else {
			log.Log.Error(err, "ReadSriovSupportedNics(): failed to check sriov supported nics file", "path", utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
			return nil, err
		}
	}

	rawConfig, err := os.ReadFile(utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
	if err != nil {
		log.Log.Error(err, "ReadSriovSupportedNics(): failed to read sriov supported nics file", "path", utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
		return nil, err
	}

	lines := strings.Split(string(rawConfig), "\n")
	return lines, nil
}

// CleanSriovFilesFromHost removes SR-IOV related configuration and service files from the host system.
// It deletes several systemd-related files including configuration paths, result paths, supported NICs path,
// and service binary path. If not in an OpenShift environment, it also removes the main SR-IOV
// service and post-networking service files.
func (s *systemd) CleanSriovFilesFromHost(isOpenShift bool) error {
	err := os.Remove(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(utils.GetHostExtensionPath(consts.SriovSystemdServiceBinaryPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// in openshift we should not remove the systemd service it will be done by the machine config operator
	if !isOpenShift {
		err = os.Remove(utils.GetHostExtensionPath(consts.SriovServicePath))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		err = os.Remove(utils.GetHostExtensionPath(consts.SriovPostNetworkServicePath))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
