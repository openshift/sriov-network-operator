package apis

import (
	"k8s.io/apimachinery/pkg/runtime"
	netattdefv1 "github.com/pliurh/sriov-network-operator/pkg/apis/k8s/v1"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes runtime.SchemeBuilder

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	netattdefv1.SchemeBuilder.AddToScheme(s)
	return AddToSchemes.AddToScheme(s)
}
