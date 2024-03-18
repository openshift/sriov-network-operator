package webhook

import (
	"encoding/json"
	"os"

	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var namespace = os.Getenv("NAMESPACE")

func RetriveSupportedNics() error {
	if err := sriovnetworkv1.InitNicIDMapFromConfigMap(kubeclient, namespace); err != nil {
		return err
	}
	return nil
}

func MutateCustomResource(ar v1.AdmissionReview) *v1.AdmissionResponse {
	log.Log.V(2).Info("mutating custom resource")

	cr := map[string]interface{}{}

	raw := ar.Request.Object.Raw
	err := json.Unmarshal(raw, &cr)
	if err != nil {
		log.Log.Error(err, "failed to unmarshal object")
		return toV1AdmissionResponse(err)
	}
	var reviewResp *v1.AdmissionResponse
	if reviewResp, err = mutateSriovNetworkNodePolicy(cr); err != nil {
		log.Log.Error(err, "failed to mutate object")
		return toV1AdmissionResponse(err)
	}

	return reviewResp
}

func ValidateCustomResource(ar v1.AdmissionReview) *v1.AdmissionResponse {
	log.Log.V(2).Info("validating custom resource")
	var err error
	var raw []byte

	raw = ar.Request.Object.Raw
	reviewResponse := v1.AdmissionResponse{}
	reviewResponse.Allowed = true

	if ar.Request.Operation == v1.Delete {
		raw = ar.Request.OldObject.Raw
	}

	switch ar.Request.Kind.Kind {
	case "SriovNetworkNodePolicy":
		policy := sriovnetworkv1.SriovNetworkNodePolicy{}

		err = json.Unmarshal(raw, &policy)
		if err != nil {
			log.Log.Error(err, "failed to unmarshal object")
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
			log.Log.Error(err, "failed to unmarshal object")
			return toV1AdmissionResponse(err)
		}

		if reviewResponse.Allowed, reviewResponse.Warnings, err = validateSriovOperatorConfig(&config, ar.Request.Operation); err != nil {
			reviewResponse.Result = &metav1.Status{
				Reason: metav1.StatusReason(err.Error()),
			}
		}

	case "SriovNetworkPoolConfig":
		config := sriovnetworkv1.SriovNetworkPoolConfig{}

		err = json.Unmarshal(raw, &config)
		if err != nil {
			log.Log.Error(err, "failed to unmarshal object")
			return toV1AdmissionResponse(err)
		}

		if reviewResponse.Allowed, reviewResponse.Warnings, err = validateSriovNetworkPoolConfig(&config, ar.Request.Operation); err != nil {
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
