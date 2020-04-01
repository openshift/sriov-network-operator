package tests

import (
	"os"

	sriovv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	testclient "github.com/openshift/sriov-network-operator/test/util/client"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	clients           *testclient.ClientSet
	operatorNamespace string
)

func init() {
	operatorNamespace = os.Getenv("OPERATOR_NAMESPACE")
	if operatorNamespace == "" {
		operatorNamespace = "openshift-sriov-network-operator"
	}

	clients = testclient.New("", func(scheme *runtime.Scheme) {
		sriovv1.AddToScheme(scheme)
	})

}
