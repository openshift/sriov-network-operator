package orchestrator_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mcv1 "github.com/openshift/api/machineconfiguration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	cancel context.CancelFunc
	ctx    context.Context
	err    error
	node   *corev1.Node
	o      orchestrator.Interface
)

var _ = Describe("Openshift Package", Ordered, func() {

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		ctx = context.WithValue(ctx, "logger", log.FromContext(ctx))

		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		o, err = orchestrator.New(vars.ClusterType)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Pod{}, client.InNamespace(testNamespace), &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)}})).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Node{}, &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)}})).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &mcv1.MachineConfigPool{}, &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)}})).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &mcv1.MachineConfig{}, &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)}})).ToNot(HaveOccurred())

		By("Shutdown controller manager")
		cancel()
	})

	AfterAll(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
	})

	Context("Unknow cluster type", func() {
		BeforeEach(func() {
			originalClusterType := vars.ClusterType
			vars.ClusterType = ""
			DeferCleanup(func() {
				vars.ClusterType = originalClusterType
			})
		})
		It("should return error with unknow cluster type", func() {
			_, err := orchestrator.New("unknown")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown cluster type: "))
		})
	})

	Context("On Kubernetes cluster", func() {
		BeforeEach(func() {
			if vars.ClusterType != constants.ClusterTypeKubernetes {
				Skip("Cluster type is not Kubernetes")
			}
		})

		Context("ClusterType", func() {
			It("should return cluster type", func() {
				Expect(o.ClusterType()).To(Equal(constants.ClusterTypeKubernetes))
			})
		})

		Context("Flavor", func() {
			It("should return flavor", func() {
				Expect(o.Flavor()).To(Equal(constants.ClusterFlavorDefault))
			})
		})

		Context("BeforeDrainNode", func() {
			It("should return true with no error", func() {
				done, err := o.BeforeDrainNode(ctx, node)
				Expect(err).ToNot(HaveOccurred())
				Expect(done).To(BeTrue())
			})
		})

		Context("AfterCompleteDrainNode", func() {
			It("should return true with no error", func() {
				done, err := o.AfterCompleteDrainNode(ctx, node)
				Expect(err).ToNot(HaveOccurred())
				Expect(done).To(BeTrue())
			})
		})
	})

	Context("On Openshift cluster", func() {
		BeforeEach(func() {
			if vars.ClusterType != constants.ClusterTypeOpenshift {
				Skip("Cluster type is not openshift")
			}
		})

		Context("ClusterType", func() {
			It("should return cluster type", func() {
				Expect(o.ClusterType()).To(Equal(constants.ClusterTypeOpenshift))
			})
		})

		Context("Flavor", func() {
			It("should return flavor", func() {
				Expect(o.Flavor()).To(Equal(constants.ClusterFlavorDefault))
			})
		})
	})
})
