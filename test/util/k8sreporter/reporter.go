package k8sreporter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
)

type KubernetesReporter struct {
	sync.Mutex
	clients    *testclient.ClientSet
	dumpOutput io.Writer
}

func New(clients *testclient.ClientSet, dumpDestination io.Writer) *KubernetesReporter {
	return &KubernetesReporter{clients: clients, dumpOutput: dumpDestination}
}

func (r *KubernetesReporter) Report(sr types.SpecReport) {
	r.Lock()
	defer r.Unlock()
	fmt.Fprintln(r.dumpOutput, "Starting dump for failed spec", sr.ContainerHierarchyTexts)
	r.dump()
	fmt.Fprintln(r.dumpOutput, "Finished dump for failed spec")
}

func (r *KubernetesReporter) dump() {
	r.logNodes()
	r.logPods("openshift-sriov-network-operator")
	r.logPods(namespaces.Test)
	r.logLogs(func(p *corev1.Pod) bool {
		return !strings.HasPrefix(p.Name, "sriov-")
	})
	r.logSriovNodeState()
	r.logNetworkPolicies()
}

func (r *KubernetesReporter) logPods(namespace string) {
	fmt.Fprintf(r.dumpOutput, "Logging pods for %s", namespace)

	pods, err := r.clients.Pods(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch pods: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(pods, "", "    ")
	if err != nil {
		fmt.Println("Failed to marshal pods", err)
		return
	}
	fmt.Fprintln(r.dumpOutput, string(j))
}

func (r *KubernetesReporter) logNodes() {
	fmt.Fprintf(r.dumpOutput, "Logging nodes")

	nodes, err := r.clients.CoreV1Interface.Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch nodes: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(nodes, "", "    ")
	if err != nil {
		fmt.Println("Failed to marshal nodes")
		return
	}
	fmt.Fprintln(r.dumpOutput, string(j))
}

func (r *KubernetesReporter) logLogs(filterPods func(*corev1.Pod) bool) {
	fmt.Fprintf(r.dumpOutput, "Logging pods logs")

	pods, err := r.clients.Pods(corev1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch pods: %v\n", err)
		return
	}

	for _, pod := range pods.Items {
		if filterPods(&pod) {
			continue
		}
		for _, container := range pod.Spec.Containers {
			logs, err := r.clients.Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}).DoRaw(context.Background())
			if err == nil {
				fmt.Fprintf(r.dumpOutput, "Dumping logs for pod %s-%s-%s", pod.Namespace, pod.Name, container.Name)
				fmt.Fprintln(r.dumpOutput, string(logs))
			}
		}
	}
}

func (r *KubernetesReporter) logNetworkPolicies() {
	fmt.Fprintf(r.dumpOutput, "Logging network policies")

	policies := sriovv1.SriovNetworkNodePolicyList{}
	err := r.clients.List(context.Background(),
		&policies,
		runtimeclient.InNamespace("openshift-sriov-network-operator"))

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch network policies: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(policies, "", "    ")
	if err != nil {
		fmt.Println("Failed to marshal policies")
		return
	}
	fmt.Fprintln(r.dumpOutput, string(j))
}

func (r *KubernetesReporter) logSriovNodeState() {
	fmt.Fprintf(r.dumpOutput, "Logging node states")

	nodeStates, err := r.clients.SriovNetworkNodeStates("openshift-sriov-network-operator").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch node states: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(nodeStates, "", "    ")
	if err != nil {
		fmt.Println("Failed to marshal node states")
		return
	}
	fmt.Fprintln(r.dumpOutput, string(j))
}
