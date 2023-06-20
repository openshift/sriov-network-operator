package daemon

import (
	"context"
	"flag"
	"io/ioutil"
	"path"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"

	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	fakesnclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned/fake"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/fake"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var FakeSupportedNicIDs corev1.ConfigMap = corev1.ConfigMap{
	ObjectMeta: metav1.ObjectMeta{
		Name:      sriovnetworkv1.SupportedNicIDConfigmap,
		Namespace: namespace,
	},
	Data: map[string]string{
		"Intel_i40e_XXV710":      "8086 158a 154c",
		"Nvidia_mlx5_ConnectX-4": "15b3 1013 1014",
	},
}

var SriovDevicePluginPod corev1.Pod = corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "sriov-device-plugin-xxxx",
		Namespace: namespace,
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
				"host",
			},
			Symlinks: map[string]string{},
			Files: map[string][]byte{
				"/bindata/scripts/enable-rdma.sh": []byte(""),
				"/bindata/scripts/load-kmod.sh":   []byte(""),
			},
		}

		var err error
		filesystemRoot, cleanFakeFs, err = fakeFs.Use()
		Expect(err).ToNot(HaveOccurred())

		kubeClient := fakek8s.NewSimpleClientset(&FakeSupportedNicIDs, &SriovDevicePluginPod)
		client := fakesnclientset.NewSimpleClientset()

		err = sriovnetworkv1.InitNicIDMapFromConfigMap(kubeClient, namespace)
		Expect(err).ToNot(HaveOccurred())

		sut = New("test-node",
			client,
			kubeClient,
			&utils.OpenshiftContext{IsOpenShiftCluster: false, OpenshiftFlavor: ""},
			exitCh,
			stopCh,
			syncCh,
			refreshCh,
			utils.Baremetal,
			false,
			false,
		)

		sut.enabledPlugins = map[string]plugin.VendorPlugin{generic.PluginName: &fake.FakePlugin{}}

		go func() {
			sut.Run(stopCh, exitCh)
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
				podList, err := sut.kubeClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
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

		It("configure udev rules on host", func() {

			networkManagerUdevRulePath := path.Join(filesystemRoot, "host/etc/udev/rules.d/10-nm-unmanaged.rules")

			expectedContents := `ACTION=="add|change|move", ATTRS{device}=="0x1014|0x154c", ENV{NM_UNMANAGED}="1"
SUBSYSTEM=="net", ACTION=="add|move", ATTRS{phys_switch_id}!="", ATTR{phys_port_name}=="pf*vf*", ENV{NM_UNMANAGED}="1"
`
			// No need to trigger any action on config-daemon, as it checks the file in the main loop
			assertFileContents(networkManagerUdevRulePath, expectedContents)
		})
	})

	Context("isNodeDraining", func() {

		It("for a non-Openshift cluster", func() {
			sut.openshiftContext = &utils.OpenshiftContext{IsOpenShiftCluster: false}

			sut.node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: map[string]string{}}}

			Expect(sut.isNodeDraining()).To(BeFalse())

			sut.node.Annotations["sriovnetwork.openshift.io/state"] = "Draining"
			Expect(sut.isNodeDraining()).To(BeTrue())

			sut.node.Annotations["sriovnetwork.openshift.io/state"] = "Draining_MCP_Paused"
			Expect(sut.isNodeDraining()).To(BeTrue())
		})

		It("for an Openshift cluster", func() {
			sut.openshiftContext = &utils.OpenshiftContext{
				IsOpenShiftCluster: true,
				OpenshiftFlavor:    utils.OpenshiftFlavorDefault,
			}

			sut.node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: map[string]string{}}}

			Expect(sut.isNodeDraining()).To(BeFalse())

			sut.node.Annotations["sriovnetwork.openshift.io/state"] = "Draining"
			Expect(sut.isNodeDraining()).To(BeFalse())

			sut.node.Annotations["sriovnetwork.openshift.io/state"] = "Draining_MCP_Paused"
			Expect(sut.isNodeDraining()).To(BeTrue())
		})

		It("for an Openshift Hypershift cluster", func() {
			sut.openshiftContext = &utils.OpenshiftContext{
				IsOpenShiftCluster: true,
				OpenshiftFlavor:    utils.OpenshiftFlavorHypershift,
			}

			sut.node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: map[string]string{}}}

			Expect(sut.isNodeDraining()).To(BeFalse())

			sut.node.Annotations["sriovnetwork.openshift.io/state"] = "Draining"
			Expect(sut.isNodeDraining()).To(BeTrue())

			sut.node.Annotations["sriovnetwork.openshift.io/state"] = "Draining_MCP_Paused"
			Expect(sut.isNodeDraining()).To(BeTrue())
		})
	})
})

func createSriovNetworkNodeState(c snclientset.Interface, nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	_, err := c.SriovnetworkV1().
		SriovNetworkNodeStates(namespace).
		Create(context.Background(), nodeState, metav1.CreateOptions{})
	return err
}

func updateSriovNetworkNodeState(c snclientset.Interface, nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	_, err := c.SriovnetworkV1().
		SriovNetworkNodeStates(namespace).
		Update(context.Background(), nodeState, metav1.UpdateOptions{})
	return err
}

func assertFileContents(path, contents string) {
	Eventually(func() (string, error) {
		ret, err := ioutil.ReadFile(path)
		return string(ret), err
	}, "10s").WithOffset(1).Should(Equal(contents))
}
