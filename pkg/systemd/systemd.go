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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	SriovSystemdConfigPath        = consts.SriovConfBasePath + "/sriov-interface-config.yaml"
	SriovSystemdResultPath        = consts.SriovConfBasePath + "/sriov-interface-result.yaml"
	sriovSystemdSupportedNicPath  = consts.SriovConfBasePath + "/sriov-supported-nics-ids.yaml"
	sriovSystemdServiceBinaryPath = "/var/lib/sriov/sriov-network-config-daemon"

	SriovServicePath            = "/etc/systemd/system/sriov-config.service"
	SriovPostNetworkServicePath = "/etc/systemd/system/sriov-config-post-network.service"
)

// TODO: move this to the host interface also

type SriovConfig struct {
	Spec                  sriovnetworkv1.SriovNetworkNodeStateSpec `yaml:"spec"`
	UnsupportedNics       bool                                     `yaml:"unsupportedNics"`
	PlatformType          consts.PlatformTypes                     `yaml:"platformType"`
	ManageSoftwareBridges bool                                     `yaml:"manageSoftwareBridges"`
}

type SriovResult struct {
	SyncStatus    string `yaml:"syncStatus"`
	LastSyncError string `yaml:"lastSyncError"`
}

func ReadConfFile() (spec *SriovConfig, err error) {
	rawConfig, err := os.ReadFile(utils.GetHostExtensionPath(SriovSystemdConfigPath))
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(rawConfig, &spec)

	return spec, err
}

func WriteConfFile(newState *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	newFile := false
	sriovConfig := &SriovConfig{
		newState.Spec,
		vars.DevMode,
		vars.PlatformType,
		vars.ManageSoftwareBridges,
	}

	_, err := os.Stat(utils.GetHostExtensionPath(SriovSystemdConfigPath))
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
				"path", utils.GetHostExtensionPath(SriovSystemdConfigPath))
			_, err = os.Create(utils.GetHostExtensionPath(SriovSystemdConfigPath))
			if err != nil {
				log.Log.Error(err, "WriteConfFile(): fail to create file")
				return false, err
			}
			newFile = true
		} else {
			return false, err
		}
	}

	oldContent, err := os.ReadFile(utils.GetHostExtensionPath(SriovSystemdConfigPath))
	if err != nil {
		log.Log.Error(err, "WriteConfFile(): fail to read file", "path", utils.GetHostExtensionPath(SriovSystemdConfigPath))
		return false, err
	}

	oldContentObj := &SriovConfig{}
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
		"content", newContent, "path", utils.GetHostExtensionPath(SriovSystemdConfigPath))
	err = os.WriteFile(utils.GetHostExtensionPath(SriovSystemdConfigPath), newContent, 0644)
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

func WriteSriovResult(result *SriovResult) error {
	_, err := os.Stat(utils.GetHostExtensionPath(SriovSystemdResultPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("WriteSriovResult(): file not existed, create it")
			_, err = os.Create(utils.GetHostExtensionPath(SriovSystemdResultPath))
			if err != nil {
				log.Log.Error(err, "WriteSriovResult(): failed to create sriov result file", "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
				return err
			}
		} else {
			log.Log.Error(err, "WriteSriovResult(): failed to check sriov result file", "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
			return err
		}
	}

	out, err := yaml.Marshal(result)
	if err != nil {
		log.Log.Error(err, "WriteSriovResult(): failed to marshal sriov result")
		return err
	}

	log.Log.V(2).Info("WriteSriovResult(): write results",
		"content", string(out), "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
	err = os.WriteFile(utils.GetHostExtensionPath(SriovSystemdResultPath), out, 0644)
	if err != nil {
		log.Log.Error(err, "WriteSriovResult(): failed to write sriov result file", "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
		return err
	}

	return nil
}

func ReadSriovResult() (*SriovResult, error) {
	_, err := os.Stat(utils.GetHostExtensionPath(SriovSystemdResultPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("ReadSriovResult(): file does not exist, return empty result")
			return &SriovResult{}, nil
		} else {
			log.Log.Error(err, "ReadSriovResult(): failed to check sriov result file", "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
			return nil, err
		}
	}

	rawConfig, err := os.ReadFile(utils.GetHostExtensionPath(SriovSystemdResultPath))
	if err != nil {
		log.Log.Error(err, "ReadSriovResult(): failed to read sriov result file", "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
		return nil, err
	}

	result := &SriovResult{}
	err = yaml.Unmarshal(rawConfig, &result)
	if err != nil {
		log.Log.Error(err, "ReadSriovResult(): failed to unmarshal sriov result file", "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
		return nil, err
	}
	return result, err
}

func RemoveSriovResult() error {
	err := os.Remove(utils.GetHostExtensionPath(SriovSystemdResultPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("RemoveSriovResult(): result file not found")
			return nil
		}
		log.Log.Error(err, "RemoveSriovResult(): failed to remove sriov result file", "path", utils.GetHostExtensionPath(SriovSystemdResultPath))
		return err
	}
	log.Log.V(2).Info("RemoveSriovResult(): result file removed")
	return nil
}

func WriteSriovSupportedNics() error {
	_, err := os.Stat(utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("WriteSriovSupportedNics(): file does not exist, create it")
			_, err = os.Create(utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
			if err != nil {
				log.Log.Error(err, "WriteSriovSupportedNics(): failed to create sriov supporter nics ids file",
					"path", utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
				return err
			}
		} else {
			log.Log.Error(err, "WriteSriovSupportedNics(): failed to check sriov supported nics ids file", "path", utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
			return err
		}
	}

	rawNicList := []byte{}
	for _, line := range sriovnetworkv1.NicIDMap {
		rawNicList = append(rawNicList, []byte(fmt.Sprintf("%s\n", line))...)
	}

	err = os.WriteFile(utils.GetHostExtensionPath(sriovSystemdSupportedNicPath), rawNicList, 0644)
	if err != nil {
		log.Log.Error(err, "WriteSriovSupportedNics(): failed to write sriov supported nics ids file",
			"path", utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
		return err
	}

	return nil
}

func ReadSriovSupportedNics() ([]string, error) {
	_, err := os.Stat(utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("ReadSriovSupportedNics(): file does not exist, return empty result")
			return nil, err
		} else {
			log.Log.Error(err, "ReadSriovSupportedNics(): failed to check sriov supported nics file", "path", utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
			return nil, err
		}
	}

	rawConfig, err := os.ReadFile(utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
	if err != nil {
		log.Log.Error(err, "ReadSriovSupportedNics(): failed to read sriov supported nics file", "path", utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
		return nil, err
	}

	lines := strings.Split(string(rawConfig), "\n")
	return lines, nil
}

func CleanSriovFilesFromHost(isOpenShift bool) error {
	err := os.Remove(utils.GetHostExtensionPath(SriovSystemdConfigPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(utils.GetHostExtensionPath(SriovSystemdResultPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(utils.GetHostExtensionPath(sriovSystemdSupportedNicPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(utils.GetHostExtensionPath(sriovSystemdServiceBinaryPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// in openshift we should not remove the systemd service it will be done by the machine config operator
	if !isOpenShift {
		err = os.Remove(utils.GetHostExtensionPath(SriovServicePath))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		err = os.Remove(utils.GetHostExtensionPath(SriovPostNetworkServicePath))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
