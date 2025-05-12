package tests

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/nodes"
)

var _ = Describe("[sriov] operator", Ordered, ContinueOnFailure, func() {
	Describe("No SriovNetworkNodePolicy", func() {
		Context("SR-IOV network config daemon can be set by nodeselector", func() {
			// 26186
			It("Should schedule the config daemon on selected nodes", func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}

				By("Checking that a daemon is scheduled on each worker node")
				Eventually(func() bool {
					return daemonsScheduledOnNodes("node-role.kubernetes.io/worker=")
				}, 3*time.Minute, 1*time.Second).Should(Equal(true))

				By("Labeling one worker node with the label needed for the daemon")
				allNodes, err := clients.CoreV1Interface.Nodes().List(context.Background(), metav1.ListOptions{
					LabelSelector: "node-role.kubernetes.io/worker",
				})
				Expect(err).ToNot(HaveOccurred())

				selectedNodes, err := nodes.MatchingOptionalSelector(clients, allNodes.Items)
				Expect(err).ToNot(HaveOccurred())

				Expect(len(selectedNodes)).To(BeNumerically(">", 0), "There must be at least one worker")
				patch := []byte(`{"metadata":{"labels":{"sriovenabled":"true"}}}`)
				candidate, err := clients.CoreV1Interface.Nodes().Patch(context.Background(), selectedNodes[0].Name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
				Expect(err).ToNot(HaveOccurred())
				selectedNodes[0] = *candidate

				By("Setting the node selector for each daemon")
				cfg := sriovv1.SriovOperatorConfig{}
				err = clients.Get(context.TODO(), runtimeclient.ObjectKey{
					Name:      "default",
					Namespace: operatorNamespace,
				}, &cfg)
				Expect(err).ToNot(HaveOccurred())
				cfg.Spec.ConfigDaemonNodeSelector = map[string]string{
					"sriovenabled": "true",
				}
				Eventually(func() error {
					return clients.Update(context.TODO(), &cfg)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Checking that a daemon is scheduled only on selected node")
				Eventually(func() bool {
					return !daemonsScheduledOnNodes("sriovenabled!=true") &&
						daemonsScheduledOnNodes("sriovenabled=true")
				}, 1*time.Minute, 1*time.Second).Should(Equal(true))

				By("Restoring the node selector for daemons")
				err = clients.Get(context.TODO(), runtimeclient.ObjectKey{
					Name:      "default",
					Namespace: operatorNamespace,
				}, &cfg)
				Expect(err).ToNot(HaveOccurred())
				cfg.Spec.ConfigDaemonNodeSelector = map[string]string{}
				Eventually(func() error {
					return clients.Update(context.TODO(), &cfg)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Checking that a daemon is scheduled on each worker node")
				Eventually(func() bool {
					return daemonsScheduledOnNodes("node-role.kubernetes.io/worker")
				}, 1*time.Minute, 1*time.Second).Should(Equal(true))
			})
		})

		Context("LogLevel affects operator's logs", func() {
			It("when set to 0 no lifecycle logs are present", func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}

				initialLogLevelValue := getOperatorConfigLogLevel()
				DeferCleanup(func() {
					By("Restore LogLevel to its initial value")
					setOperatorConfigLogLevel(initialLogLevelValue)
				})

				initialDisableDrain, err := cluster.GetNodeDrainState(clients, operatorNamespace)
				Expect(err).ToNot(HaveOccurred())

				DeferCleanup(func() {
					By("Restore DisableDrain to its initial value")
					Eventually(func() error {
						return cluster.SetDisableNodeDrainState(clients, operatorNamespace, initialDisableDrain)
					}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())
				})

				By("Set operator LogLevel to 2")
				setOperatorConfigLogLevel(2)

				By("Flip DisableDrain to trigger operator activity")
				since := time.Now().Add(-10 * time.Second)
				Eventually(func() error {
					return cluster.SetDisableNodeDrainState(clients, operatorNamespace, !initialDisableDrain)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Assert logs contains verbose output")
				Eventually(func(g Gomega) {
					logs := getOperatorLogs(since)
					g.Expect(logs).To(
						ContainElement(And(
							ContainSubstring("Reconciling SriovOperatorConfig"),
						)),
					)

					// Should contain verbose logging
					g.Expect(logs).To(
						ContainElement(
							ContainSubstring("Start to sync webhook objects"),
						),
					)
				}, 1*time.Minute, 5*time.Second).Should(Succeed())

				By("Reduce operator LogLevel to 0")
				setOperatorConfigLogLevel(0)

				By("Flip DisableDrain again to trigger operator activity")
				since = time.Now().Add(-10 * time.Second)
				Eventually(func() error {
					return cluster.SetDisableNodeDrainState(clients, operatorNamespace, initialDisableDrain)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Assert logs contains less operator activity")
				Eventually(func(g Gomega) {
					logs := getOperatorLogs(since)

					// time only contains sec, but we can have race here that in the same sec there was a sync
					afterLogs := []string{}
					found := false
					for _, log := range logs {
						if found {
							afterLogs = append(afterLogs, log)
						}
						if strings.Contains(log, "{\"new-level\": 0, \"current-level\": 2}") {
							found = true
						}
					}
					g.Expect(found).To(BeTrue())
					g.Expect(afterLogs).To(
						ContainElement(And(
							ContainSubstring("Reconciling SriovOperatorConfig"),
						)),
					)

					// Should not contain verbose logging
					g.Expect(afterLogs).ToNot(
						ContainElement(
							ContainSubstring("Start to sync webhook objects"),
						),
					)
				}, 3*time.Minute, 5*time.Second).Should(Succeed())
			})
		})

		Context("SriovNetworkMetricsExporter", func() {
			BeforeEach(func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}

				initialValue := isFeatureFlagEnabled("metricsExporter")
				DeferCleanup(func() {
					By("Restoring initial feature flag value")
					setFeatureFlag("metricsExporter", initialValue)
				})

				By("Enabling `metricsExporter` feature flag")
				setFeatureFlag("metricsExporter", true)
			})

			It("should be deployed if the feature gate is enabled", func() {
				By("Checking that a daemon is scheduled on selected node")
				Eventually(func() bool {
					return isDaemonsetScheduledOnNodes("node-role.kubernetes.io/worker", "app=sriov-network-metrics-exporter")
				}).WithTimeout(time.Minute).WithPolling(time.Second).Should(Equal(true))
			})

			It("should deploy ServiceMonitor if the Prometheus operator is installed", func() {
				_, err := clients.ServiceMonitors(operatorNamespace).List(context.Background(), metav1.ListOptions{})
				if k8serrors.IsNotFound(err) {
					Skip("Prometheus operator not available in the cluster")
				}

				By("Checking ServiceMonitor is deployed if needed")
				Eventually(func(g Gomega) {
					_, err := clients.ServiceMonitors(operatorNamespace).Get(context.Background(), "sriov-network-metrics-exporter", metav1.GetOptions{})
					g.Expect(err).ToNot(HaveOccurred())
				}).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
			})

			It("should remove ServiceMonitor when the feature is turned off", func() {
				setFeatureFlag("metricsExporter", false)
				Eventually(func(g Gomega) {
					_, err := clients.ServiceMonitors(operatorNamespace).Get(context.Background(), "sriov-network-metrics-exporter", metav1.GetOptions{})
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
			})
		})

		Context("Namespaced network objects", func() {
			DescribeTable("can be create in every namespaces", func(object runtimeclient.Object) {
				err := clients.Create(context.Background(), object)
				Expect(err).ToNot(HaveOccurred())

				waitForNetAttachDef(object.GetName(), object.GetNamespace())

				err = clients.Delete(context.Background(), object)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() bool {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: object.GetName(), Namespace: object.GetNamespace()}, netAttDef)
					return err != nil && k8serrors.IsNotFound(err)
				}, 2*time.Minute, 10*time.Second).Should(BeTrue())
			},
				Entry("SriovNetwork", &sriovv1.SriovNetwork{ObjectMeta: metav1.ObjectMeta{Name: "sriovnet1", Namespace: namespaces.Test}}),
				Entry("SriovIBNetwork", &sriovv1.SriovIBNetwork{ObjectMeta: metav1.ObjectMeta{Name: "sriovibnet1", Namespace: namespaces.Test}}),
				Entry("OVSNetwork", &sriovv1.OVSNetwork{ObjectMeta: metav1.ObjectMeta{Name: "ovsnet1", Namespace: namespaces.Test}}),
			)

			DescribeTable("can NOT be in application namespace and have .Spec.NetworkNamespace != ''", func(object runtimeclient.Object) {
				err := clients.Create(context.Background(), object)
				Expect(err).To(HaveOccurred())
				Expect(string(k8serrors.ReasonForError(err))).
					To(ContainSubstring(".Spec.NetworkNamespace field can't be specified if the resource is not in the "))
			},
				Entry("SriovNetwork", &sriovv1.SriovNetwork{ObjectMeta: metav1.ObjectMeta{Name: "sriovnet1", Namespace: namespaces.Test}, Spec: sriovv1.SriovNetworkSpec{NetworkNamespace: "default"}}),
				Entry("SriovIBNetwork", &sriovv1.SriovIBNetwork{ObjectMeta: metav1.ObjectMeta{Name: "sriovibnet1", Namespace: namespaces.Test}, Spec: sriovv1.SriovIBNetworkSpec{NetworkNamespace: "default"}}),
				Entry("OVSNetwork", &sriovv1.OVSNetwork{ObjectMeta: metav1.ObjectMeta{Name: "ovsnet1", Namespace: namespaces.Test}, Spec: sriovv1.OVSNetworkSpec{NetworkNamespace: "default"}}),
			)
		})
	})
})
