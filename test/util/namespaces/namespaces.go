package namespaces

import (
	"context"
	"fmt"
	"strings"
	"time"

	k8sv1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/utils/pointer"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
)

// Test is the namespace to be use for testing
const Test = "sriov-conformance-testing"

var inhibitSecurityAdmissionLabels = map[string]string{
	"pod-security.kubernetes.io/audit":               "privileged",
	"pod-security.kubernetes.io/enforce":             "privileged",
	"pod-security.kubernetes.io/warn":                "privileged",
	"security.openshift.io/scc.podSecurityLabelSync": "false",
}

// WaitForDeletion waits until the namespace will be removed from the cluster
func WaitForDeletion(cs *testclient.ClientSet, nsName string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		_, err := cs.Namespaces().Get(context.Background(), nsName, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

// Create creates a new namespace with the given name.
// If the namespace exists, it returns.
func Create(namespace string, cs *testclient.ClientSet) error {
	_, err := cs.Namespaces().Create(context.Background(), &k8sv1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namespace,
			Labels: inhibitSecurityAdmissionLabels,
		}}, metav1.CreateOptions{})

	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// DeleteAndWait deletes a namespace and waits until it is deleted
func DeleteAndWait(cs *testclient.ClientSet, namespace string, timeout time.Duration) error {
	err := cs.Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
	if k8serrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to delete namespace [%s]: %w", namespace, err)
	}

	return WaitForDeletion(cs, namespace, timeout)
}

// Exists tells whether the given namespace exists
func Exists(namespace string, cs *testclient.ClientSet) bool {
	_, err := cs.Namespaces().Get(context.Background(), namespace, metav1.GetOptions{})
	return err == nil || !k8serrors.IsNotFound(err)
}

// CleanPods deletes all pods in namespace
func CleanPods(namespace string, cs *testclient.ClientSet) error {
	if !Exists(namespace, cs) {
		return nil
	}
	err := cs.Pods(namespace).DeleteCollection(context.Background(), metav1.DeleteOptions{
		GracePeriodSeconds: pointer.Int64Ptr(0),
	}, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pods %v", err)
	}
	return err
}

// CleanPolicies deletes all SriovNetworkNodePolicies in operatorNamespace
func CleanPolicies(operatorNamespace string, cs *testclient.ClientSet) error {
	policies := sriovv1.SriovNetworkNodePolicyList{}
	err := cs.List(context.Background(),
		&policies,
		runtimeclient.InNamespace(operatorNamespace),
	)
	if err != nil {
		return err
	}
	for _, p := range policies.Items {
		if p.Name != "default" && strings.HasPrefix(p.Name, "test-") {
			err := cs.Delete(context.Background(), &p)
			if err != nil {
				return fmt.Errorf("failed to delete policy %v", err)
			}
		}
	}
	return err
}

// CleanNetworks deletes all network in operatorNamespace
func CleanNetworks(operatorNamespace string, cs *testclient.ClientSet) error {
	networks := sriovv1.SriovNetworkList{}
	err := cs.List(context.Background(),
		&networks,
		runtimeclient.InNamespace(operatorNamespace))
	if err != nil {
		return err
	}
	for _, n := range networks.Items {
		if strings.HasPrefix(n.Name, "test-") {
			err := cs.Delete(context.Background(), &n)
			if err != nil {
				return fmt.Errorf("failed to delete network %v", err)
			}
		}
	}
	return waitForSriovNetworkDeletion(operatorNamespace, cs, 15*time.Second)
}

func waitForSriovNetworkDeletion(operatorNamespace string, cs *testclient.ClientSet, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		networks := sriovv1.SriovNetworkList{}
		err := cs.List(context.Background(),
			&networks,
			runtimeclient.InNamespace(operatorNamespace))
		if err != nil {
			return false, err
		}
		for _, network := range networks.Items {
			if strings.HasPrefix(network.Name, "test-") {
				return false, nil
			}
		}
		return true, nil
	})
}

// Clean cleans all dangling objects from the given namespace.
func Clean(operatorNamespace, namespace string, cs *testclient.ClientSet, discoveryEnabled bool) error {
	err := CleanPods(namespace, cs)
	if err != nil {
		return err
	}
	err = CleanNetworks(operatorNamespace, cs)
	if err != nil {
		return err
	}
	if discoveryEnabled {
		return nil
	}
	err = CleanPolicies(operatorNamespace, cs)
	if err != nil {
		return err
	}
	return nil
}

func AddLabel(cs corev1client.NamespacesGetter, ctx context.Context, namespaceName, key, value string) error {
	ns, err := cs.Namespaces().Get(context.Background(), namespaceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get namespace [%s]: %v", namespaceName, err)
	}

	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}

	if ns.Labels[key] == value {
		return nil
	}

	ns.Labels[key] = value

	_, err = cs.Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update namespace [%s] with label [%s: %s]: %v", namespaceName, key, value, err)
	}

	return nil
}
