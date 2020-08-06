package webhook

import (
	"encoding/json"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
)

var (
	defaultPriorityPatch   = map[string]interface{}{"op": "add", "path": "/spec/priority", "value": 99}
	defaultDeviceTypePatch = map[string]interface{}{"op": "add", "path": "/spec/deviceType", "value": "netdevice"}
	defaultIsRdmaPatch     = map[string]interface{}{"op": "add", "path": "/spec/isRdma", "value": false}
	defaultLinkTypePatch   = map[string]interface{}{"op": "add", "path": "/spec/linkType", "value": "eth"}
	InfiniBandIsRdmaPatch  = map[string]interface{}{"op": "add", "path": "/spec/isRdma", "value": true}
)

func mutateSriovNetworkNodePolicy(cr map[string]interface{}) (*v1beta1.AdmissionResponse, error) {
	glog.V(2).Infof("mutateSriovNetworkNodePolicy(): set default value")
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
		glog.V(2).Infof("mutateSriovNetworkNodePolicy(): set default priority to lowest for %v", name)
		patchs = append(patchs, defaultPriorityPatch)
	}
	if _, ok := spec.(map[string]interface{})["deviceType"]; !ok {
		glog.V(2).Infof("mutateSriovNetworkNodePolicy(): set default deviceType to netdevice for %v", name)
		patchs = append(patchs, defaultDeviceTypePatch)
	}
	if _, ok := spec.(map[string]interface{})["isRdma"]; !ok {
		glog.V(2).Infof("mutateSriovNetworkNodePolicy(): set default isRdma to false for %v", name)
		patchs = append(patchs, defaultIsRdmaPatch)
	}
	if _, ok := spec.(map[string]interface{})["linkType"]; !ok {
		glog.V(2).Infof("mutateSriovNetworkNodePolicy(): set default linkType to eth for %v", name)
		patchs = append(patchs, defaultLinkTypePatch)
	}
	// Device with InfiniBand link type requires isRdma to be true
	if str, ok := spec.(map[string]interface{})["linkType"].(string); ok && strings.ToLower(str) == "ib" {
		glog.V(2).Infof("mutateSriovNetworkNodePolicy(): set isRdma to true for %v since ib link type is detected", name)
		patchs = append(patchs, InfiniBandIsRdmaPatch)
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
