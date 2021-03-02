package utils

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

const (
	switchDevConfPath = "/host/etc/switchdev.conf"
)

func IsSwitchdevModeSpec(spec sriovnetworkv1.SriovNetworkNodeStateSpec) bool {
	for _, iface := range spec.Interfaces {
		if iface.EswitchMode == sriovnetworkv1.ESWITCHMODE_SWITCHDEV {
			return true
		}
	}
	return false
}

func WriteSwitchdevConfFile(newState *sriovnetworkv1.SriovNetworkNodeState) (update, remove bool, err error) {
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
	newContent := ""
	for _, iface := range newState.Spec.Interfaces {
		if iface.EswitchMode == sriovnetworkv1.ESWITCHMODE_SWITCHDEV {
			newContent = newContent + fmt.Sprintln(iface.PciAddress, iface.NumVfs)
		}
	}
	oldContent, err := ioutil.ReadFile(switchDevConfPath)
	if err != nil {
		glog.Errorf("WriteSwitchdevConfFile(): fail to read file: %v", err)
		return
	}
	if newContent == string(oldContent) {
		glog.V(2).Info("WriteSwitchdevConfFile(): no update")
		return
	}
	if newContent == "" {
		remove = true
		glog.V(2).Info("WriteSwitchdevConfFile(): remove content in switchdev.conf")
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
