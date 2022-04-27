package tests

import (
	"context"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	goclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
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
}

const (
	sriovOperatorDeploymentName = "sriov-network-operator"
	// SriovNetworkNodePolicies contains the name of the sriov network node policies CRD
	sriovNetworkNodePolicies = "sriovnetworknodepolicies.sriovnetwork.openshift.io"
	// sriovNetworkNodeStates contains the name of the sriov network node state CRD
	sriovNetworkNodeStates = "sriovnetworknodestates.sriovnetwork.openshift.io"
	// sriovNetworks contains the name of the sriov network CRD
	sriovNetworks = "sriovnetworks.sriovnetwork.openshift.io"
	// sriovOperatorConfigs contains the name of the sriov Operator config CRD
	sriovOperatorConfigs = "sriovoperatorconfigs.sriovnetwork.openshift.io"
)

var _ = Describe("validation", func() {

	Context("sriov", func() {
		It("should have the sriov namespace", func() {
			_, err := clients.Namespaces().Get(context.Background(), operatorNamespace, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should have the sriov operator deployment in running state", func() {
			deploy, err := clients.Deployments(operatorNamespace).Get(context.Background(), sriovOperatorDeploymentName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(deploy.Status.Replicas).To(Equal(deploy.Status.ReadyReplicas))

			pods, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("name=%s", sriovOperatorDeploymentName)})
			Expect(err).ToNot(HaveOccurred())

			Expect(len(pods.Items)).To(Equal(1))
			Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodRunning))
		})

		It("Should have the sriov CRDs available in the cluster", func() {
			crd := &apiext.CustomResourceDefinition{}
			err := clients.Client.Get(context.TODO(), goclient.ObjectKey{Name: sriovNetworkNodePolicies}, crd)
			Expect(err).ToNot(HaveOccurred())

			err = clients.Client.Get(context.TODO(), goclient.ObjectKey{Name: sriovNetworkNodeStates}, crd)
			Expect(err).ToNot(HaveOccurred())

			err = clients.Client.Get(context.TODO(), goclient.ObjectKey{Name: sriovNetworks}, crd)
			Expect(err).ToNot(HaveOccurred())

			err = clients.Client.Get(context.TODO(), goclient.ObjectKey{Name: sriovOperatorConfigs}, crd)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should deploy the injector pod if requested", func() {
			operatorConfig := &sriovv1.SriovOperatorConfig{}
			err := clients.Client.Get(context.TODO(), goclient.ObjectKey{Name: "default", Namespace: operatorNamespace}, operatorConfig)
			Expect(err).ToNot(HaveOccurred())

			if *operatorConfig.Spec.EnableInjector {
				daemonset, err := clients.DaemonSets(operatorNamespace).Get(context.Background(), "network-resources-injector", metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(daemonset.Status.DesiredNumberScheduled).To(Equal(daemonset.Status.NumberReady))
			} else {
				_, err := clients.DaemonSets(operatorNamespace).Get(context.Background(), "network-resources-injector", metav1.GetOptions{})
				Expect(err).To(HaveOccurred())
				Expect(errors.IsNotFound(err)).To(BeTrue())
			}
		})

		It("should deploy the operator webhook if requested", func() {
			operatorConfig := &sriovv1.SriovOperatorConfig{}
			err := clients.Get(context.TODO(), goclient.ObjectKey{Name: "default", Namespace: operatorNamespace}, operatorConfig)
			Expect(err).ToNot(HaveOccurred())

			if *operatorConfig.Spec.EnableOperatorWebhook {
				daemonset, err := clients.DaemonSets(operatorNamespace).Get(context.Background(), "operator-webhook", metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(daemonset.Status.DesiredNumberScheduled).To(Equal(daemonset.Status.NumberReady))
			} else {
				_, err := clients.DaemonSets(operatorNamespace).Get(context.Background(), "operator-webhook", metav1.GetOptions{})
				Expect(err).To(HaveOccurred())
				Expect(errors.IsNotFound(err)).To(BeTrue())
			}
		})

		It("should have SR-IOV node statuses not in progress", func() {
			CheckStable()
		})
	})
})

func CheckStable() {
	res, err := cluster.SriovStable(operatorNamespace, clients)
	Expect(err).ToNot(HaveOccurred())
	Expect(res).To(BeTrue(), "SR-IOV status is not stable")

	isClusterReady, err := cluster.IsClusterStable(clients)
	Expect(err).ToNot(HaveOccurred())
	Expect(isClusterReady).To(BeTrue(), "Cluster is not stable")
}
