package webhook

import (
	"encoding/json"
	"os"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
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
	cr := sriovnetworkv1.SriovNetworkNodePolicy{}
	var err error
	var raw []byte
	raw = ar.Request.Object.Raw

	err = json.Unmarshal(raw, &cr)
	if err != nil {
		glog.Error(err)
		return toV1beta1AdmissionResponse(err)
	}

	reviewResponse := v1beta1.AdmissionResponse{}
	reviewResponse.Allowed = true

	if reviewResponse.Allowed, err = validateSriovNetworkNodePolicy(&cr, ar.Request.Operation); err != nil {
		reviewResponse.Result = &metav1.Status{
			Reason: metav1.StatusReason(err.Error()),
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
