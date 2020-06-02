package webhook

import (
	"encoding/json"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
)

var (
	defaultPriorityPatch   = map[string]interface{}{"op": "add", "path": "/spec/priority", "value": 99}
	defaultDeviceTypePatch = map[string]interface{}{"op": "add", "path": "/spec/deviceType", "value": "netdevice"}
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
	var err error
	reviewResponse.Patch, err = json.Marshal(patchs)
	if err != nil {
		return nil, err
	}

	pt := v1beta1.PatchTypeJSONPatch
	reviewResponse.PatchType = &pt
	return &reviewResponse, nil
}
