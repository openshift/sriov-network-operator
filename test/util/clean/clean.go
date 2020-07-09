package clean

import (
	"fmt"
	"os"
	"time"

	"github.com/openshift/sriov-network-operator/test/util/client"
	"github.com/openshift/sriov-network-operator/test/util/namespaces"
)

// All cleans all the dangling resources created by conformance tests.
// This includes pods, networks, policies and namespaces.
func All() error {
	operatorNamespace, found := os.LookupEnv("OPERATOR_NAMESPACE")
	if !found {
		operatorNamespace = "openshift-sriov-network-operator"
	}

	clients := client.New("")
	if !namespaces.Exists(namespaces.Test, clients) {
		return nil
	}
	err := namespaces.Clean(operatorNamespace, namespaces.Test, clients, false)
	if err != nil {
		return fmt.Errorf("Failed to clean sriov resources %v", err)
	}

	err = namespaces.DeleteAndWait(clients, namespaces.Test, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("Failed to delete sriov tests namespace %v", err)
	}
	return nil
}
