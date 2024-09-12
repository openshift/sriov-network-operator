package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("[sriov] Metrics Exporter", Ordered, ContinueOnFailure, func() {
	var node string
	var nic *sriovv1.InterfaceExt

	BeforeAll(func() {
		err := namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())

		featureFlagInitialValue := isFeatureFlagEnabled("metricsExporter")
		DeferCleanup(func() {
			By("Restoring initial feature flag value")
			setFeatureFlag("metricsExporter", featureFlagInitialValue)
		})

		By("Enabling `metricsExporter` feature flag")
		setFeatureFlag("metricsExporter", true)

		By("Adding monitoring label to " + operatorNamespace)
		err = namespaces.AddLabel(clients, context.Background(), operatorNamespace, "openshift.io/cluster-monitoring", "true")
		Expect(err).ToNot(HaveOccurred())

		WaitForSRIOVStable()

		sriovInfos, err := cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())

		node, nic, err = sriovInfos.FindOneSriovNodeAndDevice()
		Expect(err).ToNot(HaveOccurred())
		By("Using device " + nic.Name + " on node " + node)

		_, err = network.CreateSriovPolicy(clients, "test-me-policy-", operatorNamespace, nic.Name, node, 2, "metricsResource", "netdevice")
		Expect(err).ToNot(HaveOccurred())

		err = network.CreateSriovNetwork(clients, nic, "test-me-network", namespaces.Test, operatorNamespace, "metricsResource", ipamIpv4)
		Expect(err).ToNot(HaveOccurred())
		waitForNetAttachDef("test-me-network", namespaces.Test)

		DeferCleanup(namespaces.Clean, operatorNamespace, namespaces.Test, clients, discovery.Enabled())
	})

	It("collects metrics regarding receiving traffic via VF", func() {
		if cluster.VirtualCluster() {
			Skip("IGB driver does not support VF statistics")
		}

		pod := createTestPod(node, []string{"test-me-network"})
		DeferCleanup(namespaces.CleanPods, namespaces.Test, clients)

		ips, err := network.GetSriovNicIPs(pod, "net1")
		Expect(err).ToNot(HaveOccurred())
		Expect(ips).NotTo(BeNil(), "No sriov network interface found.")
		Expect(len(ips)).Should(Equal(1))

		initialMetrics := getMetricsForNode(node)
		initialRxBytes := getCounterForPod(initialMetrics, pod, "sriov_vf_rx_bytes")
		initialRxPackets := getCounterForPod(initialMetrics, pod, "sriov_vf_rx_packets")

		for _, ip := range ips {
			pingPod(ip, node, "test-me-network")
		}

		finalMetrics := getMetricsForNode(node)
		finalRxBytes := getCounterForPod(finalMetrics, pod, "sriov_vf_rx_bytes")
		finalRxPackets := getCounterForPod(finalMetrics, pod, "sriov_vf_rx_packets")

		Expect(finalRxBytes).Should(BeNumerically(">", initialRxBytes))
		Expect(finalRxPackets).Should(BeNumerically(">", initialRxPackets))
	})

	Context("When Prometheus operator is available", func() {
		BeforeEach(func() {
			_, err := clients.ServiceMonitors(operatorNamespace).List(context.Background(), metav1.ListOptions{})
			if k8serrors.IsNotFound(err) {
				Skip("Prometheus operator not available in the cluster")
			}
		})

		It("PrometheusRule should provide namespaced metrics", func() {
			pod := createTestPod(node, []string{"test-me-network"})
			DeferCleanup(namespaces.CleanPods, namespaces.Test, clients)

			namespacedMetricNames := []string{
				"network:sriov_vf_rx_bytes",
				"network:sriov_vf_tx_bytes",
				"network:sriov_vf_rx_packets",
				"network:sriov_vf_tx_packets",
				"network:sriov_vf_rx_dropped",
				"network:sriov_vf_tx_dropped",
				"network:sriov_vf_rx_broadcast",
				"network:sriov_vf_rx_multicast",
			}

			Eventually(func(g Gomega) {
				for _, metricName := range namespacedMetricNames {
					values := runPromQLQuery(fmt.Sprintf(`%s{namespace="%s",pod="%s"}`, metricName, pod.Namespace, pod.Name))
					g.Expect(values).ToNot(BeEmpty(), "no value for metric %s", metricName)
				}
			}, "90s", "1s").Should(Succeed())
		})

		It("Metrics should have the correct labels", func() {
			pod := createTestPod(node, []string{"test-me-network"})
			DeferCleanup(namespaces.CleanPods, namespaces.Test, clients)

			metricsName := []string{
				"sriov_vf_rx_bytes",
				"sriov_vf_tx_bytes",
				"sriov_vf_rx_packets",
				"sriov_vf_tx_packets",
				"sriov_vf_rx_dropped",
				"sriov_vf_tx_dropped",
				"sriov_vf_rx_broadcast",
				"sriov_vf_rx_multicast",
			}

			Eventually(func(g Gomega) {
				for _, metricName := range metricsName {
					samples := runPromQLQuery(metricName)
					g.Expect(samples).ToNot(BeEmpty(), "no value for metric %s", metricName)
					g.Expect(samples[0].Metric).To(And(
						HaveKey(model.LabelName("pciAddr")),
						HaveKey(model.LabelName("node")),
						HaveKey(model.LabelName("pf")),
						HaveKey(model.LabelName("vf")),
					))
				}
			}, "90s", "1s").Should(Succeed())

			// sriov_kubepoddevice has a different sets of label than statistics metrics
			samples := runPromQLQuery(fmt.Sprintf(`sriov_kubepoddevice{namespace="%s",pod="%s"}`, pod.Namespace, pod.Name))
			Expect(samples).ToNot(BeEmpty(), "no value for metric sriov_kubepoddevice")
			Expect(samples[0].Metric).To(And(
				HaveKey(model.LabelName("pciAddr")),
				HaveKeyWithValue(model.LabelName("node"), model.LabelValue(pod.Spec.NodeName)),
				HaveKeyWithValue(model.LabelName("dev_type"), model.LabelValue("openshift.io/metricsResource")),
				HaveKeyWithValue(model.LabelName("namespace"), model.LabelValue(pod.Namespace)),
				HaveKeyWithValue(model.LabelName("pod"), model.LabelValue(pod.Name)),
			))
		})
	})
})

func getMetricsForNode(nodeName string) map[string]*dto.MetricFamily {
	metricsExporterPods, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=sriov-network-metrics-exporter",
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, metricsExporterPods.Items).ToNot(HaveLen(0), "At least one operator pod expected")

	metricsExporterPod := metricsExporterPods.Items[0]

	command := []string{"curl", "http://127.0.0.1:9110/metrics"}
	stdout, stderr, err := pod.ExecCommand(clients, &metricsExporterPod, command...)
	Expect(err).ToNot(HaveOccurred(),
		"pod: [%s/%s] command: [%v]\nstdout: %s\nstderr: %s", metricsExporterPod.Namespace, metricsExporterPod.Name, command, stdout, stderr)

	// Clean the scraped output from carriage returns
	stdout = strings.ReplaceAll(stdout, "\r", "")

	var parser expfmt.TextParser
	mf, err := parser.TextToMetricFamilies(strings.NewReader(stdout))
	Expect(err).ToNot(HaveOccurred())

	return mf
}

func getCounterForPod(mf map[string]*dto.MetricFamily, p *corev1.Pod, metricName string) float64 {
	pciAddress := findPciAddressForPod(mf, p)
	return findCounterForPciAddr(mf, pciAddress, metricName)
}

func findPciAddressForPod(mf map[string]*dto.MetricFamily, p *corev1.Pod) string {
	kubePodDeviceMetric := findKubePodDeviceMetric(mf, p)
	for _, labelPair := range kubePodDeviceMetric.Label {
		if labelPair.GetName() == "pciAddr" {
			return *labelPair.Value
		}
	}

	Fail(fmt.Sprintf("Can't find PCI Address for pod  [%s/%s] in metrics %+v", p.Name, p.Namespace, mf))
	return ""
}

func findKubePodDeviceMetric(mf map[string]*dto.MetricFamily, pod *corev1.Pod) *dto.Metric {
	metricFamily, ok := mf["sriov_kubepoddevice"]
	Expect(ok).To(BeTrue(), "sriov_kubepoddevice metric not found: %+v", mf)

	kubePodDeviceMetric := findMetricForPod(metricFamily.Metric, pod)
	Expect(kubePodDeviceMetric).ToNot(BeNil(), "sriov_kubepoddevice metric for pod [%s/%s]  not found: %+v", pod.Name, pod.Namespace, mf)

	return kubePodDeviceMetric
}

func findCounterForPciAddr(mf map[string]*dto.MetricFamily, pciAddress string, metricName string) float64 {
	metricFamily, ok := mf[metricName]
	Expect(ok).To(BeTrue(), "metric %s not found: %+v", metricName, mf)

	metric := findMetricFor(metricFamily.Metric, map[string]string{
		"pciAddr": pciAddress,
	})
	Expect(metric).ToNot(BeNil(), "metric %s for pciAddr %s not found: %+v", metricName, pciAddress, mf)

	return *metric.GetCounter().Value
}

func findMetricForPod(metrics []*dto.Metric, pod *corev1.Pod) *dto.Metric {
	return findMetricFor(metrics, map[string]string{
		"pod":       pod.Name,
		"namespace": pod.Namespace,
	})
}

func findMetricFor(metrics []*dto.Metric, labelsToMatch map[string]string) *dto.Metric {
	for _, metric := range metrics {
		if areLabelsMatching(metric.Label, labelsToMatch) {
			return metric
		}
	}

	return nil
}

func areLabelsMatching(labels []*dto.LabelPair, labelsToMatch map[string]string) bool {
	for _, labelPair := range labels {
		valueToMatch, ok := labelsToMatch[labelPair.GetName()]
		if !ok {
			continue
		}

		if *labelPair.Value != valueToMatch {
			return false
		}
	}

	return true
}

func runPromQLQuery(query string) model.Vector {
	prometheusPods, err := clients.Pods("").List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=prometheus",
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, prometheusPods.Items).ToNot(HaveLen(0), "At least one Prometheus operator pod expected")

	prometheusPod := prometheusPods.Items[0]

	url := fmt.Sprintf("localhost:9090/api/v1/query?%s", (url.Values{"query": []string{query}}).Encode())
	command := []string{"curl", url}
	stdout, stderr, err := pod.ExecCommand(clients, &prometheusPod, command...)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(),
		"promQL query failed: [%s/%s] command: [%v]\nstdout: %s\nstderr: %s", prometheusPod.Namespace, prometheusPod.Name, command, stdout, stderr)

	result := struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string       `json:"resultType"`
			Result     model.Vector `json:"result"`
		} `json:"data"`
	}{}

	json.Unmarshal([]byte(stdout), &result)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, result.Status).To(Equal("success"), "cURL for [%s] failed: %s", url, stdout)

	return result.Data.Result
}
