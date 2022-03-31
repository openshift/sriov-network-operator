package daemon

import (
	"context"
	"flag"
	"testing"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	fakemcclientset "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	fakesnclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned/fake"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var FakeSupportedNicIDs corev1.ConfigMap = corev1.ConfigMap{
	ObjectMeta: metav1.ObjectMeta{
		Name:      sriovnetworkv1.SUPPORTED_NIC_ID_CONFIGMAP,
		Namespace: namespace,
	},
	Data: map[string]string{},
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
		mcClient := fakemcclientset.NewSimpleClientset()

		sut = New("test-node",
			client,
			kubeClient,
			mcClient,
			exitCh,
			stopCh,
			syncCh,
			refreshCh,
			utils.Baremetal,
		)

		sut.LoadedPlugins = map[string]VendorPlugin{GenericPlugin: &fakePlugin{}}
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
			go func() {
				Expect(sut.Run(stopCh, exitCh)).To(BeNil())
			}()

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
	})
})

func createSriovNetworkNodeState(c snclientset.Interface, nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	_, err := c.SriovnetworkV1().
		SriovNetworkNodeStates(namespace).
		Create(context.Background(), nodeState, metav1.CreateOptions{})
	return err
}

type fakePlugin struct{}

func (f *fakePlugin) Name() string {
	return "fake_plugin"
}

func (f *fakePlugin) Spec() string {
	return "1.0"
}

func (f *fakePlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error) {
	return false, false, nil
}

func (f *fakePlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error) {
	return false, false, nil
}

func (f *fakePlugin) Apply() error {
	return nil
}
