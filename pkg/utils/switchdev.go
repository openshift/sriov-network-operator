package utils

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os"

	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

const (
	switchDevConfPath = "/host/etc/sriov_config.json"
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
				i = sriovnetworkv1.Interface{
					// Not passing all the contents, since only NumVfs and EswitchMode can be configured by configure-switchdev.sh currently.
					Name:       iface.Name,
					PciAddress: iface.PciAddress,
					NumVfs:     iface.NumVfs,
				}

				if iface.EswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
					i.EswitchMode = iface.EswitchMode
				}
				cfg.Interfaces = append(cfg.Interfaces, i)
			}
		}
	}
	_, err = os.Stat(switchDevConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			if len(cfg.Interfaces) == 0 {
				err = nil
				return
			}
			glog.V(2).Infof("WriteSwitchdevConfFile(): file not existed, create it")
			_, err = os.Create(switchDevConfPath)
			if err != nil {
				glog.Errorf("WriteSwitchdevConfFile(): fail to create file: %v", err)
				return
			}
		} else {
			return
		}
	}
	oldContent, err := ioutil.ReadFile(switchDevConfPath)
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
	err = ioutil.WriteFile(switchDevConfPath, newContent, 0644)
	if err != nil {
		glog.Errorf("WriteSwitchdevConfFile(): fail to write file: %v", err)
		return
	}
	return
}
