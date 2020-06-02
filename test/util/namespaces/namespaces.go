package namespaces

import (
	"context"
	"fmt"
	"strings"
	"time"

	sriovv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	testclient "github.com/openshift/sriov-network-operator/test/util/client"
)

// Test is the namespace to be use for testing
const Test = "sriov-conformance-testing"

// WaitForDeletion waits until the namespace will be removed from the cluster
func WaitForDeletion(cs *testclient.ClientSet, nsName string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		_, err := cs.Namespaces().Get(nsName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

// Create creates a new namespace with the given name.
// If the namespace exists, it returns.
func Create(namespace string, cs *testclient.ClientSet) error {
	_, err := cs.Namespaces().Create(&k8sv1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		}})

	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// Clean cleans all dangling objects from the given namespace.
func Clean(operatorNamespace, namespace string, cs *testclient.ClientSet) error {
	_, err := cs.Namespaces().Get(namespace, metav1.GetOptions{})
	if err != nil && k8serrors.IsNotFound(err) {
		return nil
	}

	err = cs.Pods(namespace).DeleteCollection(&metav1.DeleteOptions{
		GracePeriodSeconds: pointer.Int64Ptr(0),
	}, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("Failed to delete pods %v", err)
	}

	policies := sriovv1.SriovNetworkNodePolicyList{}
	err = cs.List(context.Background(),
		&policies,
		runtimeclient.InNamespace(operatorNamespace))

	if err != nil {
		return err
	}

	for _, p := range policies.Items {
		if p.Name != "default" && strings.HasPrefix(p.Name, "test-") {
			err := cs.Delete(context.Background(), &p)
			if err != nil {
				return fmt.Errorf("Failed to delete policy %v", err)
			}
		}
	}

	networks := sriovv1.SriovNetworkList{}
	err = cs.List(context.Background(),
		&networks,
		runtimeclient.InNamespace(operatorNamespace))

	if err != nil {
		return err
	}

	for _, n := range networks.Items {
		if strings.HasPrefix(n.Name, "test-") {
			err := cs.Delete(context.Background(), &n)
			if err != nil {
				return fmt.Errorf("Failed to delete network %v", err)
			}
		}
	}
	return nil
}
