package webhook

import (
	"encoding/json"
	"os"

	"github.com/golang/glog"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var namespace = os.Getenv("NAMESPACE")

func RetriveSupportedNics() error {
	if err := sriovnetworkv1.InitNicIDMap(kubeclient, namespace); err != nil {
		return err
	}
	return nil
}

func MutateCustomResource(ar v1.AdmissionReview) *v1.AdmissionResponse {
	glog.V(2).Info("mutating custom resource")

	cr := map[string]interface{}{}

	raw := ar.Request.Object.Raw
	err := json.Unmarshal(raw, &cr)
	if err != nil {
		glog.Error(err)
		return toV1AdmissionResponse(err)
	}
	var reviewResp *v1.AdmissionResponse
	if reviewResp, err = mutateSriovNetworkNodePolicy(cr); err != nil {
		glog.Error(err)
		return toV1AdmissionResponse(err)
	}

	return reviewResp
}

func ValidateCustomResource(ar v1.AdmissionReview) *v1.AdmissionResponse {
	glog.V(2).Info("validating custom resource")
	var err error
	var raw []byte

	raw = ar.Request.Object.Raw
	reviewResponse := v1.AdmissionResponse{}
	reviewResponse.Allowed = true

	if ar.Request.Operation == "DELETE" {
		raw = ar.Request.OldObject.Raw
	}

	switch ar.Request.Kind.Kind {
	case "SriovNetworkNodePolicy":
		policy := sriovnetworkv1.SriovNetworkNodePolicy{}

		err = json.Unmarshal(raw, &policy)
		if err != nil {
			glog.Error(err)
			return toV1AdmissionResponse(err)
		}

		if reviewResponse.Allowed, reviewResponse.Warnings, err = validateSriovNetworkNodePolicy(&policy, ar.Request.Operation); err != nil {
			reviewResponse.Result = &metav1.Status{
				Reason: metav1.StatusReason(err.Error()),
			}
		}
	case "SriovOperatorConfig":
		config := sriovnetworkv1.SriovOperatorConfig{}

		err = json.Unmarshal(raw, &config)
		if err != nil {
			glog.Error(err)
			return toV1AdmissionResponse(err)
		}

		if reviewResponse.Allowed, reviewResponse.Warnings, err = validateSriovOperatorConfig(&config, ar.Request.Operation); err != nil {
			reviewResponse.Result = &metav1.Status{
				Reason: metav1.StatusReason(err.Error()),
			}
		}
	}

	return &reviewResponse
}

func toV1AdmissionResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}
