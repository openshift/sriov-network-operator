/*
Copyright 2021.

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
package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

const (
	SriovConfBasePath          = "/etc/sriov-operator"
	HostSriovConfBasePath      = "/host" + SriovConfBasePath
	SriovSwitchDevConfPath     = SriovConfBasePath + "/sriov_config.json"
	SriovHostSwitchDevConfPath = "/host" + SriovSwitchDevConfPath
)

type config struct {
	Interfaces []sriovnetworkv1.Interface `json:"interfaces"`
}

func IsSwitchdevModeSpec(spec sriovnetworkv1.SriovNetworkNodeStateSpec) bool {
	for _, iface := range spec.Interfaces {
		if iface.EswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
			return true
		}
	}
	return false
}

func findInterface(interfaces sriovnetworkv1.Interfaces, name string) (iface sriovnetworkv1.Interface, err error) {
	for _, i := range interfaces {
		if i.Name == name {
			return i, nil
		}
	}
	return sriovnetworkv1.Interface{}, fmt.Errorf("unable to find interface: %v", name)
}

func WriteSwitchdevConfFile(newState *sriovnetworkv1.SriovNetworkNodeState) (update bool, err error) {
	// Create a map with all the PFs we will need to SKIP for systemd configuration
	pfsToSkip, err := GetPfsToSkip(newState)
	if err != nil {
		return false, err
	}
	cfg := config{}
	for _, iface := range newState.Spec.Interfaces {
		for _, ifaceStatus := range newState.Status.Interfaces {
			if iface.PciAddress != ifaceStatus.PciAddress {
				continue
			}

			if skip := pfsToSkip[iface.PciAddress]; !skip {
				continue
			}

			i := sriovnetworkv1.Interface{}
			if iface.NumVfs > 0 {
				var vfGroups []sriovnetworkv1.VfGroup = nil
				ifc, err := findInterface(newState.Spec.Interfaces, iface.Name)
				if err != nil {
					glog.Errorf("WriteSwitchdevConfFile(): fail find interface: %v", err)
				} else {
					vfGroups = ifc.VfGroups
				}
				i = sriovnetworkv1.Interface{
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
	_, err = os.Stat(SriovHostSwitchDevConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			if len(cfg.Interfaces) == 0 {
				err = nil
				return
			}

			// Create the sriov-operator folder on the host if it doesn't exist
			if _, err := os.Stat("/host" + SriovConfBasePath); os.IsNotExist(err) {
				err = os.Mkdir("/host"+SriovConfBasePath, os.ModeDir)
				if err != nil {
					glog.Errorf("WriteConfFile(): fail to create sriov-operator folder: %v", err)
					return false, err
				}
			}

			glog.V(2).Infof("WriteSwitchdevConfFile(): file not existed, create it")
			_, err = os.Create(SriovHostSwitchDevConfPath)
			if err != nil {
				glog.Errorf("WriteSwitchdevConfFile(): fail to create file: %v", err)
				return
			}
		} else {
			return
		}
	}
	oldContent, err := ioutil.ReadFile(SriovHostSwitchDevConfPath)
	if err != nil {
		glog.Errorf("WriteSwitchdevConfFile(): fail to read file: %v", err)
		return
	}
	var newContent []byte
	if len(cfg.Interfaces) != 0 {
		newContent, err = json.Marshal(cfg)
		if err != nil {
			glog.Errorf("WriteSwitchdevConfFile(): fail to marshal config: %v", err)
			return
		}
	}

	if bytes.Equal(newContent, oldContent) {
		glog.V(2).Info("WriteSwitchdevConfFile(): no update")
		return
	}
	update = true
	glog.V(2).Infof("WriteSwitchdevConfFile(): write '%s' to switchdev.conf", newContent)
	err = ioutil.WriteFile(SriovHostSwitchDevConfPath, newContent, 0644)
	if err != nil {
		glog.Errorf("WriteSwitchdevConfFile(): fail to write file: %v", err)
		return
	}
	return
}
