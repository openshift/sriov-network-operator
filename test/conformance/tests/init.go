package tests

import (
	"os"
	"strconv"
	"time"

	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
)

var (
	waitingTime       = 20 * time.Minute
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

	waitingEnv := os.Getenv("SRIOV_WAITING_TIME")
	newTime, err := strconv.Atoi(waitingEnv)
	if err == nil && newTime != 0 {
		waitingTime = time.Duration(newTime) * time.Minute
	}
}
