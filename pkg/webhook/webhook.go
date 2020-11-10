package webhook

import (
	"encoding/json"
	"os"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/api/v1"
)

var namespace = os.Getenv("NAMESPACE")

func MutateCustomResource(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	glog.V(2).Info("mutating custom resource")

	cr := map[string]interface{}{}

	raw := ar.Request.Object.Raw
	err := json.Unmarshal(raw, &cr)
	if err != nil {
		glog.Error(err)
		return toV1beta1AdmissionResponse(err)
	}
	var reviewResp *v1beta1.AdmissionResponse
	if reviewResp, err = mutateSriovNetworkNodePolicy(cr); err != nil {
		glog.Error(err)
		return toV1beta1AdmissionResponse(err)
	}

	return reviewResp
}

func ValidateCustomResource(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	glog.V(2).Info("validating custom resource")
	var err error
	var raw []byte

	raw = ar.Request.Object.Raw
	reviewResponse := v1beta1.AdmissionResponse{}
	reviewResponse.Allowed = true

	if ar.Request.Operation == "DELETE" {
		raw = ar.Request.OldObject.Raw
	} else {
		raw = ar.Request.Object.Raw
	}

	switch ar.Request.Kind.Kind {
	case "SriovNetworkNodePolicy":
		policy := sriovnetworkv1.SriovNetworkNodePolicy{}

		err = json.Unmarshal(raw, &policy)
		if err != nil {
			glog.Error(err)
			return toV1beta1AdmissionResponse(err)
		}

		if reviewResponse.Allowed, err = validateSriovNetworkNodePolicy(&policy, ar.Request.Operation); err != nil {
			reviewResponse.Result = &metav1.Status{
				Reason: metav1.StatusReason(err.Error()),
			}
		}
	case "SriovOperatorConfig":
		config := sriovnetworkv1.SriovOperatorConfig{}

		err = json.Unmarshal(raw, &config)
		if err != nil {
			glog.Error(err)
			return toV1beta1AdmissionResponse(err)
		}

		if reviewResponse.Allowed, err = validateSriovOperatorConfig(&config, ar.Request.Operation); err != nil {
			reviewResponse.Result = &metav1.Status{
				Reason: metav1.StatusReason(err.Error()),
			}
		}
	}

	return &reviewResponse
}

func toV1beta1AdmissionResponse(err error) *v1beta1.AdmissionResponse {
	return &v1beta1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}
