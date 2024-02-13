package daemon

import (
	"context"
	"flag"
	"testing"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	mock_platforms "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"

	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	fakesnclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned/fake"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openshift"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/fake"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var FakeSupportedNicIDs corev1.ConfigMap = corev1.ConfigMap{
	ObjectMeta: metav1.ObjectMeta{
		Name:      sriovnetworkv1.SupportedNicIDConfigmap,
		Namespace: vars.Namespace,
	},
	Data: map[string]string{
		"Intel_i40e_XXV710":      "8086 158a 154c",
		"Nvidia_mlx5_ConnectX-4": "15b3 1013 1014",
	},
}

var SriovDevicePluginPod corev1.Pod = corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "sriov-device-plugin-xxxx",
		Namespace: vars.Namespace,
		Labels: map[string]string{
			"app": "sriov-device-plugin",
		},
	},
	Spec: corev1.PodSpec{
		NodeName: "test-node",
	},
}

func TestConfigDaemon(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Daemon Suite")
}

var _ = BeforeSuite(func() {
	// Increase verbosity to help debugging failures
	flag.Set("logtostderr", "true")
	flag.Set("stderrthreshold", "WARNING")
	flag.Set("v", "2")
})

var _ = Describe("Config Daemon", func() {
	var stopCh chan struct{}
	var syncCh chan struct{}
	var exitCh chan error
	var refreshCh chan Message

	var cleanFakeFs func()

	var sut *Daemon

	BeforeEach(func() {
		stopCh = make(chan struct{})
		refreshCh = make(chan Message)
		exitCh = make(chan error)
		syncCh = make(chan struct{}, 64)

		// Fill syncCh with values so daemon doesn't wait for a writer
		for i := 0; i < 64; i++ {
			syncCh <- struct{}{}
		}

		// Create virtual filesystem for Daemon
		fakeFs := &fakefilesystem.FS{
			Dirs: []string{
				"bindata/scripts",
				"host/etc/sriov-operator",
				"host/etc/sriov-operator/pci",
				"host/etc/udev/rules.d",
			},
			Symlinks: map[string]string{},
			Files: map[string][]byte{
				"/bindata/scripts/enable-rdma.sh": []byte(""),
				"/bindata/scripts/load-kmod.sh":   []byte(""),
			},
		}

		var err error
		vars.FilesystemRoot, cleanFakeFs, err = fakeFs.Use()
		Expect(err).ToNot(HaveOccurred())

		vars.UsingSystemdMode = false
		vars.NodeName = "test-node"
		vars.PlatformType = consts.Baremetal

		kubeClient := fakek8s.NewSimpleClientset(&FakeSupportedNicIDs, &SriovDevicePluginPod)
		client := fakesnclientset.NewSimpleClientset()

		err = sriovnetworkv1.InitNicIDMapFromConfigMap(kubeClient, vars.Namespace)
		Expect(err).ToNot(HaveOccurred())

		er := NewEventRecorder(client, kubeClient)

		t := GinkgoT()
		mockCtrl := gomock.NewController(t)
		platformHelper := mock_platforms.NewMockInterface(mockCtrl)
		platformHelper.EXPECT().GetFlavor().Return(openshift.OpenshiftFlavorDefault).AnyTimes()
		platformHelper.EXPECT().IsOpenshiftCluster().Return(false).AnyTimes()
		platformHelper.EXPECT().IsHypershift().Return(false).AnyTimes()

		vendorHelper := mock_helper.NewMockHostHelpersInterface(mockCtrl)
		vendorHelper.EXPECT().TryEnableRdma().Return(true, nil).AnyTimes()
		vendorHelper.EXPECT().TryEnableVhostNet().AnyTimes()
		vendorHelper.EXPECT().TryEnableTun().AnyTimes()
		vendorHelper.EXPECT().PrepareNMUdevRule([]string{"0x1014", "0x154c"}).Return(nil).AnyTimes()

		sut = New(
			client,
			kubeClient,
			vendorHelper,
			platformHelper,
			exitCh,
			stopCh,
			syncCh,
			refreshCh,
			er,
			nil,
		)

		sut.loadedPlugins = map[string]plugin.VendorPlugin{generic.PluginName: &fake.FakePlugin{PluginName: "fake"}}

		go func() {
			defer GinkgoRecover()
			err := sut.Run(stopCh, exitCh)
			Expect(err).ToNot(HaveOccurred())
		}()
	})

	AfterEach(func() {
		close(stopCh)
		close(syncCh)
		close(exitCh)
		close(refreshCh)

		cleanFakeFs()
	})

	Context("Should", func() {
		It("restart sriov-device-plugin pod", func() {

			_, err := sut.kubeClient.CoreV1().Nodes().
				Create(context.Background(), &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				}, metav1.CreateOptions{})
			Expect(err).To(BeNil())

			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-node",
					Generation: 123,
				},
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{
							VFs: []sriovnetworkv1.VirtualFunction{
								{},
							},
							DeviceID:   "158b",
							Driver:     "i40e",
							Mtu:        1500,
							Name:       "ens803f0",
							PciAddress: "0000:86:00.0",
							Vendor:     "8086",
							NumVfs:     4,
							TotalVfs:   64,
						},
					},
				},
			}
			Expect(
				createSriovNetworkNodeState(sut.client, nodeState)).
				To(BeNil())

			var msg Message
			Eventually(refreshCh, "10s").Should(Receive(&msg))
			Expect(msg.syncStatus).To(Equal("InProgress"))

			Eventually(refreshCh, "10s").Should(Receive(&msg))
			Expect(msg.syncStatus).To(Equal("Succeeded"))

			Eventually(func() (int, error) {
				podList, err := sut.kubeClient.CoreV1().Pods(vars.Namespace).List(context.Background(), metav1.ListOptions{
					LabelSelector: "app=sriov-device-plugin",
					FieldSelector: "spec.nodeName=test-node",
				})

				if err != nil {
					return 0, err
				}

				return len(podList.Items), nil
			}, "10s").Should(BeZero())

		})

		It("ignore non latest SriovNetworkNodeState generations", func() {

			_, err := sut.kubeClient.CoreV1().Nodes().Create(context.Background(), &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
			}, metav1.CreateOptions{})
			Expect(err).To(BeNil())

			nodeState1 := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-node",
					Generation: 123,
				},
			}
			Expect(
				createSriovNetworkNodeState(sut.client, nodeState1)).
				To(BeNil())

			nodeState2 := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-node",
					Generation: 777,
				},
			}
			Expect(
				updateSriovNetworkNodeState(sut.client, nodeState2)).
				To(BeNil())

			var msg Message
			Eventually(refreshCh, "10s").Should(Receive(&msg))
			Expect(msg.syncStatus).To(Equal("InProgress"))

			Eventually(refreshCh, "10s").Should(Receive(&msg))
			Expect(msg.syncStatus).To(Equal("Succeeded"))

			Expect(sut.nodeState.GetGeneration()).To(BeNumerically("==", 777))
		})
	})
})

func createSriovNetworkNodeState(c snclientset.Interface, nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	_, err := c.SriovnetworkV1().
		SriovNetworkNodeStates(vars.Namespace).
		Create(context.Background(), nodeState, metav1.CreateOptions{})
	return err
}

func updateSriovNetworkNodeState(c snclientset.Interface, nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	_, err := c.SriovnetworkV1().
		SriovNetworkNodeStates(vars.Namespace).
		Update(context.Background(), nodeState, metav1.UpdateOptions{})
	return err
}
