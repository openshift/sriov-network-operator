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
		if iface.EswitchMode == sriovnetworkv1.ESWITCHMODE_SWITCHDEV {
			return true
		}
	}
	return false
}

func WriteSwitchdevConfFile(newState *sriovnetworkv1.SriovNetworkNodeState) (update bool, err error) {
	_, err = os.Stat(switchDevConfPath)
	if err != nil {
		if os.IsNotExist(err) {
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
	cfg := config{}
	for _, iface := range newState.Spec.Interfaces {
		if iface.EswitchMode == sriovnetworkv1.ESWITCHMODE_SWITCHDEV {
			// Not passing all the contents, since only NumVfs and EswitchMode can be configured by configure-switchdev.sh currently.
			i := sriovnetworkv1.Interface{
				Name:        iface.Name,
				PciAddress:  iface.PciAddress,
				EswitchMode: iface.EswitchMode,
				NumVfs:      iface.NumVfs,
			}
			cfg.Interfaces = append(cfg.Interfaces, i)
		}
	}
	oldContent, err := ioutil.ReadFile(switchDevConfPath)
	if err != nil {
		glog.Errorf("WriteSwitchdevConfFile(): fail to read file: %v", err)
		return
	}
	newContent, err := json.Marshal(cfg)
	if err != nil {
		glog.Errorf("WriteSwitchdevConfFile(): fail to marshal config: %v", err)
		return
	}
	if bytes.Equal(newContent, oldContent) {
		glog.V(2).Info("WriteSwitchdevConfFile(): no update")
		return
	}
	update = true
	glog.V(2).Infof("WriteSwitchdevConfFile(): write %s to switchdev.conf", newContent)
	err = ioutil.WriteFile(switchDevConfPath, []byte(newContent), 0644)
	if err != nil {
		glog.Errorf("WriteSwitchdevConfFile(): fail to write file: %v", err)
		return
	}
	return
}
