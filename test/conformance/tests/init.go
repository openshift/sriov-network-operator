package tests

import (
	"os"

	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
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

	clients = testclient.New("")
	if clients == nil {
		panic("failed package init, failed to create ClientSet")
	}
}
