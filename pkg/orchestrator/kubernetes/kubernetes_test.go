package kubernetes_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/kubernetes"
)

func TestBaremetal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubernetes Suite")
}

var _ = Describe("Kubernetes Platform", func() {
	var (
		k8s  *kubernetes.Kubernetes
		node *corev1.Node
		err  error
	)

	BeforeEach(func() {
		k8s, err = kubernetes.New()
		Expect(err).NotTo(HaveOccurred())
		Expect(k8s).NotTo(BeNil())

		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
			},
		}
	})

	Context("Platform Identification", func() {
		It("should correctly identify the cluster type as Kubernetes", func() {
			Expect(k8s.ClusterType()).To(Equal(consts.ClusterTypeKubernetes))
		})

		It("should correctly identify the cluster flavor as Vanilla Kubernetes", func() {
			Expect(k8s.Flavor()).To(Equal(consts.ClusterFlavorDefault))
		})
	})

	Context("Node Drain Hooks", func() {
		It("should return true for the BeforeDrainNode hook", func() {
			shouldDrain, err := k8s.BeforeDrainNode(context.Background(), node)
			Expect(err).NotTo(HaveOccurred())
			Expect(shouldDrain).To(BeTrue())
		})

		It("should return true for the AfterCompleteDrainNode hook", func() {
			shouldContinue, err := k8s.AfterCompleteDrainNode(context.Background(), node)
			Expect(err).NotTo(HaveOccurred())
			Expect(shouldContinue).To(BeTrue())
		})
	})
})
