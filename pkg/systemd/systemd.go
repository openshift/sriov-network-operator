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

	"github.com/golang/glog"
	"gopkg.in/yaml.v3"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

const (
	SriovSystemdConfigPath        = utils.SriovConfBasePath + "/sriov-interface-config.yaml"
	SriovSystemdResultPath        = utils.SriovConfBasePath + "/sriov-interface-result.yaml"
	sriovSystemdSupportedNicPath  = utils.SriovConfBasePath + "/sriov-supported-nics-ids.yaml"
	sriovSystemdServiceBinaryPath = "/var/lib/sriov/sriov-network-config-daemon"

	SriovHostSystemdConfigPath        = "/host" + SriovSystemdConfigPath
	SriovHostSystemdResultPath        = "/host" + SriovSystemdResultPath
	sriovHostSystemdSupportedNicPath  = "/host" + sriovSystemdSupportedNicPath
	sriovHostSystemdServiceBinaryPath = "/host" + sriovSystemdServiceBinaryPath

	SriovServicePath     = "/etc/systemd/system/sriov-config.service"
	SriovHostServicePath = "/host" + SriovServicePath

	HostSriovConfBasePath = "/host" + utils.SriovConfBasePath
)

type SriovConfig struct {
	Spec            sriovnetworkv1.SriovNetworkNodeStateSpec `yaml:"spec"`
	UnsupportedNics bool                                     `yaml:"unsupportedNics"`
	PlatformType    utils.PlatformType                       `yaml:"platformType"`
}

type SriovResult struct {
	SyncStatus    string `yaml:"syncStatus"`
	LastSyncError string `yaml:"lastSyncError"`
}

func ReadConfFile() (spec *SriovConfig, err error) {
	rawConfig, err := os.ReadFile(SriovSystemdConfigPath)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(rawConfig, &spec)

	return spec, err
}

func WriteConfFile(newState *sriovnetworkv1.SriovNetworkNodeState, unsupportedNics bool, platformType utils.PlatformType) (bool, error) {
	newFile := false
	// remove the device plugin revision as we don't need it here
	newState.Spec.DpConfigVersion = ""

	sriovConfig := &SriovConfig{
		newState.Spec,
		unsupportedNics,
		platformType,
	}

	_, err := os.Stat(SriovHostSystemdConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create the sriov-operator folder on the host if it doesn't exist
			if _, err := os.Stat(HostSriovConfBasePath); os.IsNotExist(err) {
				err = os.Mkdir(HostSriovConfBasePath, os.ModeDir)
				if err != nil {
					glog.Errorf("WriteConfFile(): fail to create sriov-operator folder: %v", err)
					return false, err
				}
			}

			glog.V(2).Infof("WriteConfFile(): file not existed, create it")
			_, err = os.Create(SriovHostSystemdConfigPath)
			if err != nil {
				glog.Errorf("WriteConfFile(): fail to create file: %v", err)
				return false, err
			}
			newFile = true
		} else {
			return false, err
		}
	}

	oldContent, err := os.ReadFile(SriovHostSystemdConfigPath)
	if err != nil {
		glog.Errorf("WriteConfFile(): fail to read file: %v", err)
		return false, err
	}

	oldContentObj := &SriovConfig{}
	err = yaml.Unmarshal(oldContent, oldContentObj)
	if err != nil {
		glog.Errorf("WriteConfFile(): fail to unmarshal old file: %v", err)
		return false, err
	}

	var newContent []byte
	newContent, err = yaml.Marshal(sriovConfig)
	if err != nil {
		glog.Errorf("WriteConfFile(): fail to marshal config: %v", err)
		return false, err
	}

	if bytes.Equal(newContent, oldContent) {
		glog.V(2).Info("WriteConfFile(): no update")
		return false, nil
	}
	glog.V(2).Infof("WriteConfFile(): previews configuration is not equal: old config:\n%s\nnew config:\n%s\n", string(oldContent), string(newContent))

	glog.V(2).Infof("WriteConfFile(): write '%s' to %s", newContent, SriovHostSystemdConfigPath)
	err = os.WriteFile(SriovHostSystemdConfigPath, newContent, 0644)
	if err != nil {
		glog.Errorf("WriteConfFile(): fail to write file: %v", err)
		return false, err
	}

	// this will be used to mark the first time we create this file.
	// this helps to avoid the first reboot after installation
	if newFile && len(sriovConfig.Spec.Interfaces) == 0 {
		glog.V(2).Info("WriteConfFile(): first file creation and no interfaces to configure returning reboot false")
		return false, nil
	}

	return true, nil
}

func WriteSriovResult(result *SriovResult) error {
	_, err := os.Stat(SriovSystemdResultPath)
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("WriteSriovResult(): file not existed, create it")
			_, err = os.Create(SriovSystemdResultPath)
			if err != nil {
				glog.Errorf("WriteSriovResult(): failed to create sriov result file on path %s: %v", SriovSystemdResultPath, err)
				return err
			}
		} else {
			glog.Errorf("WriteSriovResult(): failed to check sriov result file on path %s: %v", SriovSystemdResultPath, err)
			return err
		}
	}

	out, err := yaml.Marshal(result)
	if err != nil {
		glog.Errorf("WriteSriovResult(): failed to marshal sriov result file: %v", err)
		return err
	}

	glog.V(2).Infof("WriteSriovResult(): write '%s' to %s", string(out), SriovSystemdResultPath)
	err = os.WriteFile(SriovSystemdResultPath, out, 0644)
	if err != nil {
		glog.Errorf("WriteSriovResult(): failed to write sriov result file on path %s: %v", SriovSystemdResultPath, err)
		return err
	}

	return nil
}

func ReadSriovResult() (*SriovResult, error) {
	_, err := os.Stat(SriovHostSystemdResultPath)
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("ReadSriovResult(): file not existed, return empty result")
			return &SriovResult{}, nil
		} else {
			glog.Errorf("ReadSriovResult(): failed to check sriov result file on path %s: %v", SriovHostSystemdResultPath, err)
			return nil, err
		}
	}

	rawConfig, err := os.ReadFile(SriovHostSystemdResultPath)
	if err != nil {
		glog.Errorf("ReadSriovResult(): failed to read sriov result file on path %s: %v", SriovHostSystemdResultPath, err)
		return nil, err
	}

	result := &SriovResult{}
	err = yaml.Unmarshal(rawConfig, &result)
	if err != nil {
		glog.Errorf("ReadSriovResult(): failed to unmarshal sriov result file on path %s: %v", SriovHostSystemdResultPath, err)
		return nil, err
	}
	return result, err
}

func WriteSriovSupportedNics() error {
	_, err := os.Stat(sriovHostSystemdSupportedNicPath)
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("WriteSriovSupportedNics(): file not existed, create it")
			_, err = os.Create(sriovHostSystemdSupportedNicPath)
			if err != nil {
				glog.Errorf("WriteSriovSupportedNics(): failed to create sriov supporter nics ids file on path %s: %v", sriovHostSystemdSupportedNicPath, err)
				return err
			}
		} else {
			glog.Errorf("WriteSriovSupportedNics(): failed to check sriov supporter nics ids file on path %s: %v", sriovHostSystemdSupportedNicPath, err)
			return err
		}
	}

	rawNicList := []byte{}
	for _, line := range sriovnetworkv1.NicIDMap {
		rawNicList = append(rawNicList, []byte(fmt.Sprintf("%s\n", line))...)
	}

	err = os.WriteFile(sriovHostSystemdSupportedNicPath, rawNicList, 0644)
	if err != nil {
		glog.Errorf("WriteSriovSupportedNics(): failed to write sriov supporter nics ids file on path %s: %v", sriovHostSystemdSupportedNicPath, err)
		return err
	}

	return nil
}

func ReadSriovSupportedNics() ([]string, error) {
	_, err := os.Stat(sriovSystemdSupportedNicPath)
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("ReadSriovSupportedNics(): file not existed, return empty result")
			return nil, err
		} else {
			glog.Errorf("ReadSriovSupportedNics(): failed to check sriov supporter nics file on path %s: %v", sriovSystemdSupportedNicPath, err)
			return nil, err
		}
	}

	rawConfig, err := os.ReadFile(sriovSystemdSupportedNicPath)
	if err != nil {
		glog.Errorf("ReadSriovSupportedNics(): failed to read sriov supporter nics file on path %s: %v", sriovSystemdSupportedNicPath, err)
		return nil, err
	}

	lines := strings.Split(string(rawConfig), "\n")
	return lines, nil
}

func CleanSriovFilesFromHost(isOpenShift bool) error {
	err := os.Remove(SriovHostSystemdConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(SriovHostSystemdResultPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(sriovHostSystemdSupportedNicPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.Remove(sriovHostSystemdServiceBinaryPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// in openshift we should not remove the systemd service it will be done by the machine config operator
	if !isOpenShift {
		err = os.Remove(SriovHostServicePath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
