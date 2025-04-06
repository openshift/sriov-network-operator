package openshift_test

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mcv1 "github.com/openshift/api/machineconfiguration/v1"
	mcoconsts "github.com/openshift/machine-config-operator/pkg/daemon/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openshift"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	cancel context.CancelFunc
	ctx    context.Context
	err    error
	op     openshift.OpenshiftContextInterface
)

var _ = Describe("Openshift Package", Ordered, func() {
	BeforeAll(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
		soc := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
			Name:      constants.DefaultConfigName,
			Namespace: testNamespace,
		},
			Spec: sriovnetworkv1.SriovOperatorConfigSpec{
				LogLevel: 2,
			},
		}
		err := k8sClient.Create(context.Background(), soc)
		Expect(err).ToNot(HaveOccurred())

		snolog.SetLogLevel(2)
		// Check if the environment variable CLUSTER_TYPE is set
		if clusterType, ok := os.LookupEnv("CLUSTER_TYPE"); ok && clusterType == constants.ClusterTypeOpenshift {
			vars.ClusterType = constants.ClusterTypeOpenshift
		} else {
			vars.ClusterType = constants.ClusterTypeKubernetes
		}
	})

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		op, err = openshift.New()
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

	Context("On Kubernetes cluster", func() {
		BeforeEach(func() {
			if vars.ClusterType != constants.ClusterTypeKubernetes {
				Skip("Cluster type is not Kubernetes")
			}
		})

		It("should return an empty struct", func() {
			temp, err := openshift.New()
			Expect(err).ToNot(HaveOccurred())
			Expect(temp.IsOpenshiftCluster()).To(BeFalse())
		})

		It("should return false for IsOpenshiftCluster function", func() {
			Expect(op.IsOpenshiftCluster()).To(BeFalse())
		})

		It("should return empty string for GetFlavor function", func() {
			Expect(op.GetFlavor()).To(Equal(openshift.OpenshiftFlavor("")))
		})

		It("should return false for IsHypershift function", func() {
			Expect(op.IsHypershift()).To(BeFalse())
		})

		It("should always return true for OpenshiftBeforeDrainNode", func() {
			n := createNode("worker0")
			complete, err := op.OpenshiftBeforeDrainNode(ctx, n)
			Expect(err).ToNot(HaveOccurred())
			Expect(complete).To(BeTrue())
		})

		It("should always return true for OpenshiftBeforeDrainNode even for invalid call like non existing node", func() {
			n := createNode("worker0")
			n.Name = "test"
			complete, err := op.OpenshiftBeforeDrainNode(ctx, n)
			Expect(err).ToNot(HaveOccurred())
			Expect(complete).To(BeTrue())
		})

		It("should always return true for OpenshiftAfterCompleteDrainNode", func() {
			n := createNode("worker0")
			complete, err := op.OpenshiftAfterCompleteDrainNode(ctx, n)
			Expect(err).ToNot(HaveOccurred())
			Expect(complete).To(BeTrue())
		})

		It("should always return true for OpenshiftAfterCompleteDrainNode even for invalid call like non existing node", func() {
			n := createNode("worker0")
			n.Name = "test"
			complete, err := op.OpenshiftAfterCompleteDrainNode(ctx, n)
			Expect(err).ToNot(HaveOccurred())
			Expect(complete).To(BeTrue())
		})

		It("should always return error for GetNodeMachinePoolName on a non openshift cluster", func() {
			n := createNode("worker0")
			_, err = op.GetNodeMachinePoolName(ctx, n)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("On Openshift cluster", func() {
		BeforeEach(func() {
			if vars.ClusterType != constants.ClusterTypeOpenshift {
				Skip("Cluster type is not openshift")
			}
		})

		Context("OpenshiftBeforeDrainNode", func() {
			It("should return true if the cluster is not openshift", func() {
				vars.ClusterType = constants.ClusterTypeKubernetes
				defer func() {
					vars.ClusterType = constants.ClusterTypeOpenshift
				}()

				op, err = openshift.New()
				Expect(err).ToNot(HaveOccurred())

				n := createNode("worker-0")
				completed, err := op.OpenshiftBeforeDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())
			})

			It("should return true if the cluster is hypershift", func() {
				infra := &configv1.Infrastructure{}
				err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, infra)
				Expect(err).ToNot(HaveOccurred())
				infra.Status.ControlPlaneTopology = "External"
				err = k8sClient.Status().Update(ctx, infra)
				Expect(err).ToNot(HaveOccurred())
				defer func() {
					infra.Status.ControlPlaneTopology = "HighlyAvailable"
					err = k8sClient.Status().Update(ctx, infra)
					Expect(err).ToNot(HaveOccurred())
				}()

				// recreate to update the topology
				op, err = openshift.New()
				Expect(err).ToNot(HaveOccurred())

				n := createNode("worker-0")
				completed, err := op.OpenshiftBeforeDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())
			})

			It("should return true if the machine config is paused and it was done by the sriov operator", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				n := createNodeWithMCName("worker-0", "machine-config")
				mcp.Annotations = map[string]string{constants.MachineConfigPoolPausedAnnotation: constants.MachineConfigPoolPausedAnnotationPaused}
				mcp.Spec.Paused = true
				err = k8sClient.Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())

				completed, err := op.OpenshiftBeforeDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())
			})

			It("should return false if machine config is paused but the machine config daemon is configuring the nodes", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				n := createNodeWithMCName("worker-0", "machine-config")
				mcp.Annotations = map[string]string{constants.MachineConfigPoolPausedAnnotation: constants.MachineConfigPoolPausedAnnotationPaused}
				mcp.Spec.Configuration.Name = "test"
				err = k8sClient.Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())
				mcp.Status.Configuration.Name = "test1"
				err = k8sClient.Status().Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())

				completed, err := op.OpenshiftBeforeDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeFalse())
			})

			It("should return true after patching the machineConfigPool with pause true", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				n := createNodeWithMCName("worker-0", "machine-config")
				mcp.Annotations = map[string]string{constants.MachineConfigPoolPausedAnnotation: constants.MachineConfigPoolPausedAnnotationPaused}
				mcp.Spec.Configuration.Name = "test"
				err = k8sClient.Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())
				mcp.Status.Configuration.Name = "test"
				err = k8sClient.Status().Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())

				completed, err := op.OpenshiftBeforeDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())

				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(mcp), mcp)
				Expect(err).ToNot(HaveOccurred())
				Expect(mcp.Spec.Paused).To(BeTrue())
			})

			It("should add the sriov pause label and update the machine config pool spec with pause true", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				n := createNodeWithMCName("worker-0", "machine-config")
				mcp.Spec.Configuration.Name = "test"
				err = k8sClient.Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())
				mcp.Status.Configuration.Name = "test"
				err = k8sClient.Status().Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())

				completed, err := op.OpenshiftBeforeDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())

				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(mcp), mcp)
				Expect(err).ToNot(HaveOccurred())
				Expect(mcp.Spec.Paused).To(BeTrue())
				value, exist := mcp.Annotations[constants.MachineConfigPoolPausedAnnotation]
				Expect(exist).To(BeTrue())
				Expect(value).To(Equal(constants.MachineConfigPoolPausedAnnotationPaused))
			})
		})

		Context("OpenshiftAfterCompleteDrainNode", func() {
			It("should return true if the cluster is not openshift", func() {
				vars.ClusterType = constants.ClusterTypeKubernetes
				defer func() {
					vars.ClusterType = constants.ClusterTypeOpenshift
				}()

				op, err = openshift.New()
				Expect(err).ToNot(HaveOccurred())

				n := createNode("worker-0")
				completed, err := op.OpenshiftAfterCompleteDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())
			})

			It("should return true if the cluster is hypershift", func() {
				infra := &configv1.Infrastructure{}
				err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, infra)
				Expect(err).ToNot(HaveOccurred())
				infra.Status.ControlPlaneTopology = "External"
				err = k8sClient.Status().Update(ctx, infra)
				Expect(err).ToNot(HaveOccurred())
				defer func() {
					infra.Status.ControlPlaneTopology = "HighlyAvailable"
					err = k8sClient.Status().Update(ctx, infra)
					Expect(err).ToNot(HaveOccurred())
				}()

				// recreate to update the topology
				op, err = openshift.New()
				Expect(err).ToNot(HaveOccurred())

				n := createNode("worker-0")
				completed, err := op.OpenshiftAfterCompleteDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())
			})

			It("should return true if the machine config pool doesn't have sriov annotation", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				n := createNodeWithMCName("worker-0", "machine-config")

				completed, err := op.OpenshiftAfterCompleteDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())
			})

			It("should return true if the machine config pool have sriov annotation on idle", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				n := createNodeWithMCName("worker-0", "machine-config")
				mcp.Annotations = map[string]string{constants.MachineConfigPoolPausedAnnotation: constants.MachineConfigPoolPausedAnnotationIdle}
				err = k8sClient.Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())

				completed, err := op.OpenshiftAfterCompleteDrainNode(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())
			})

			It("should return true without changing the machine config pool pause to false when other nodes are still draining", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				mcp.Annotations = map[string]string{constants.MachineConfigPoolPausedAnnotation: constants.MachineConfigPoolPausedAnnotationPaused}
				mcp.Spec.Paused = true
				mcp.Spec.Configuration.Name = "test"
				mcp.Spec.NodeSelector = &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"test": "",
					},
				}
				err = k8sClient.Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())
				mcp.Status.Configuration.Name = "test"
				err = k8sClient.Status().Update(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())

				n1 := createNodeWithMCName("worker-0", "machine-config")
				n1.Annotations[constants.NodeDrainAnnotation] = constants.Draining
				err = k8sClient.Update(ctx, n1)
				Expect(err).ToNot(HaveOccurred())

				n2 := createNodeWithMCName("worker-1", "machine-config")
				n2.Annotations[constants.NodeDrainAnnotation] = constants.Draining
				err = k8sClient.Update(ctx, n2)
				Expect(err).ToNot(HaveOccurred())

				completed, err := op.OpenshiftAfterCompleteDrainNode(ctx, n1)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())

				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(mcp), mcp)
				Expect(err).ToNot(HaveOccurred())
				value, exist := mcp.Annotations[constants.MachineConfigPoolPausedAnnotation]
				Expect(exist).To(BeTrue())
				Expect(value).To(Equal(constants.MachineConfigPoolPausedAnnotationPaused))
				Expect(mcp.Spec.Paused).To(BeTrue())

				n1.Annotations[constants.NodeDrainAnnotation] = constants.DrainIdle
				err = k8sClient.Update(ctx, n1)
				Expect(err).ToNot(HaveOccurred())

				completed, err = op.OpenshiftAfterCompleteDrainNode(ctx, n2)
				Expect(err).ToNot(HaveOccurred())
				Expect(completed).To(BeTrue())

				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(mcp), mcp)
				Expect(err).ToNot(HaveOccurred())
				value, exist = mcp.Annotations[constants.MachineConfigPoolPausedAnnotation]
				Expect(exist).To(BeTrue())
				Expect(value).To(Equal(constants.MachineConfigPoolPausedAnnotationIdle))
				Expect(mcp.Spec.Paused).To(BeFalse())
			})
		})

		Context("GetNodeMachinePoolName", func() {
			It("should return error if the cluster is not openshift", func() {
				vars.ClusterType = constants.ClusterTypeKubernetes
				defer func() {
					vars.ClusterType = constants.ClusterTypeOpenshift
				}()
				n := createNode("worker-0")
				op, err = openshift.New()
				Expect(err).ToNot(HaveOccurred())

				_, err = op.GetNodeMachinePoolName(ctx, n)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("not an openshift cluster"))
			})

			It("should return error if the cluster is hypershift", func() {
				infra := &configv1.Infrastructure{}
				err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, infra)
				Expect(err).ToNot(HaveOccurred())
				infra.Status.ControlPlaneTopology = "External"
				err = k8sClient.Status().Update(ctx, infra)
				Expect(err).ToNot(HaveOccurred())
				defer func() {
					infra.Status.ControlPlaneTopology = "HighlyAvailable"
					err = k8sClient.Status().Update(ctx, infra)
					Expect(err).ToNot(HaveOccurred())
				}()

				// recreate to update the topology
				op, err = openshift.New()
				Expect(err).ToNot(HaveOccurred())

				n := createNode("worker-0")
				_, err = op.GetNodeMachinePoolName(ctx, n)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("hypershift doesn't have machineConfig"))
			})

			It("should return error if the node doesn't have the MCO annotation", func() {
				n := createNode("worker-0")
				delete(n.Annotations, mcoconsts.DesiredMachineConfigAnnotationKey)
				err = k8sClient.Update(ctx, n)
				Expect(err).ToNot(HaveOccurred())

				_, err = op.GetNodeMachinePoolName(ctx, n)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to find the the desiredConfig Annotation"))
			})

			It("should return error if the name of the MC in the annotation doesn't exist", func() {
				n := createNodeWithMCName("worker-0", "worker")
				_, err = op.GetNodeMachinePoolName(ctx, n)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to get the desired Machine Config: machineconfigs.machineconfiguration.openshift.io \"worker\" not found"))
			})

			It("should return error if machine config pool doesn't exist in owner reference of machine config", func() {
				n := createNodeWithMCName("worker-0", "worker")
				createMachineConfig("worker", nil)
				_, err = op.GetNodeMachinePoolName(ctx, n)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to find the MCP of the node"))
			})

			It("should return the machine config pool name for a specific node base on the machine config owner reference", func() {
				mcp := createMachineConfigPool("worker")
				createMachineConfig("machine-config", mcp)
				n := createNodeWithMCName("worker-0", "machine-config")
				mcpName, err := op.GetNodeMachinePoolName(ctx, n)
				Expect(err).ToNot(HaveOccurred())
				Expect(mcpName).To(Equal("worker"))
			})
		})

		Context("ChangeMachineConfigPoolPause", func() {
			It("should failed to change if mcp doesn't exist", func() {
				mcp := createMachineConfigPool("worker")
				mcp.Name = "test"
				err = op.ChangeMachineConfigPoolPause(ctx, mcp, false)
				Expect(err).To(HaveOccurred())
			})

			It("should switch the pause", func() {
				mcp := createMachineConfigPool("worker")
				Expect(mcp.Spec.Paused).To(BeFalse())
				err = op.ChangeMachineConfigPoolPause(ctx, mcp, true)
				Expect(err).ToNot(HaveOccurred())
				Expect(mcp.Spec.Paused).To(BeTrue())
			})
		})
	})
})

func createNode(nodeName string) *corev1.Node {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Annotations: map[string]string{
				constants.NodeDrainAnnotation:               constants.DrainIdle,
				mcoconsts.DesiredMachineConfigAnnotationKey: "worker",
			},
			Labels: map[string]string{
				"test": "",
			},
		},
	}

	Expect(k8sClient.Create(ctx, &node)).ToNot(HaveOccurred())
	vars.NodeName = nodeName

	return &node
}

func createNodeWithMCName(nodeName, mcName string) *corev1.Node {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Annotations: map[string]string{
				constants.NodeDrainAnnotation:               constants.DrainIdle,
				mcoconsts.DesiredMachineConfigAnnotationKey: mcName,
			},
			Labels: map[string]string{
				"test": "",
			},
		},
	}

	Expect(k8sClient.Create(ctx, &node)).ToNot(HaveOccurred())
	vars.NodeName = nodeName

	return &node
}

func createMachineConfigPool(poolName string) *mcv1.MachineConfigPool {
	mcp := &mcv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: poolName,
		},
		Spec: mcv1.MachineConfigPoolSpec{
			Paused: false,
		},
	}

	Expect(k8sClient.Create(ctx, mcp)).ToNot(HaveOccurred())
	return mcp
}

func createMachineConfig(mcName string, mcp *mcv1.MachineConfigPool) *mcv1.MachineConfig {
	mc := &mcv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: mcName,
		},
	}

	if mcp != nil {
		mc.OwnerReferences = []metav1.OwnerReference{
			{
				Name:       mcp.Name,
				APIVersion: "machineconfiguration.openshift.io/v1",
				Kind:       "MachineConfigPool",
				UID:        mcp.UID,
			},
		}
	}

	Expect(k8sClient.Create(ctx, mc)).ToNot(HaveOccurred())
	return mc
}
