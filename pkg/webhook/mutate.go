package webhook

import (
	"encoding/json"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
)

var (
	defaultPriorityPatch   = map[string]interface{}{"op": "add", "path": "/spec/priority", "value": 99}
	defaultEthMtuPatch     = map[string]interface{}{"op": "add", "path": "/spec/mtu", "value": 1500}
	defaultIBMtuPatch      = map[string]interface{}{"op": "add", "path": "/spec/mtu", "value": 2048}
	defaultDeviceTypePatch = map[string]interface{}{"op": "add", "path": "/spec/deviceType", "value": "netdevice"}
	defaultIsRdmaPatch     = map[string]interface{}{"op": "add", "path": "/spec/isRdma", "value": false}
	defaultLinkTypePatch   = map[string]interface{}{"op": "add", "path": "/spec/linkType", "value": "eth"}
)

func defaultSriovNetworkNodePolicy(cr map[string]interface{}) (*v1beta1.AdmissionResponse, error) {
	glog.V(2).Infof("defaultSriovNetworkNodePolicy(): set default value")
	reviewResponse := v1beta1.AdmissionResponse{}
	reviewResponse.Allowed = true

	name := cr["metadata"].(map[string]interface{})["name"]
	if name == "default" {
		// skip the default policy
		return &reviewResponse, nil
	}

	patchs := []map[string]interface{}{}
	spec := cr["spec"]
	if _, ok := spec.(map[string]interface{})["priority"]; !ok {
		glog.V(2).Infof("defaultSriovNetworkNodePolicy(): set default priority to lowest for %v", name)
		patchs = append(patchs, defaultPriorityPatch)
	}
	if _, ok := spec.(map[string]interface{})["deviceType"]; !ok {
		glog.V(2).Infof("defaultSriovNetworkNodePolicy(): set default deviceType to netdevice for %v", name)
		patchs = append(patchs, defaultDeviceTypePatch)
	}
	if _, ok := spec.(map[string]interface{})["isRdma"]; !ok {
		glog.V(2).Infof("defaultSriovNetworkNodePolicy(): set default isRdma to false for %v", name)
		patchs = append(patchs, defaultDeviceTypePatch)
	}
	if _, ok := spec.(map[string]interface{})["linkType"]; !ok {
		glog.V(2).Infof("defaultSriovNetworkNodePolicy(): set default linkType to eth for %v", name)
		patchs = append(patchs, defaultLinkTypePatch)
	}
	// MTU is evaluated lastly since default MTU value varies per link type
	if _, ok := spec.(map[string]interface{})["mtu"]; !ok {
		if str, ok := spec.(map[string]interface{})["linkType"].(string); ok && strings.ToLower(str) == "eth" {
			glog.V(2).Infof("defaultSriovNetworkNodePolicy(): set default mtu to 1500 for %v", name)
			patchs = append(patchs, defaultEthMtuPatch)
		} else if str, ok := spec.(map[string]interface{})["linkType"].(string); ok && strings.ToLower(str) == "ib" {
			glog.V(2).Infof("defaultSriovNetworkNodePolicy(): set default mtu to 2048 for %v", name)
			patchs = append(patchs, defaultIBMtuPatch)
		}
	}
	var err error
	reviewResponse.Patch, err = json.Marshal(patchs)
	if err != nil {
		return nil, err
	}

	pt := v1beta1.PatchTypeJSONPatch
	reviewResponse.PatchType = &pt
	return &reviewResponse, nil
}
