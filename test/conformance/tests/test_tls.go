package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
)

var _ = Describe("[sriov] TLS Configuration", Ordered, ContinueOnFailure, func() {
	var node string

	BeforeAll(func() {
		if discovery.Enabled() {
			Skip("Test unsuitable to be run in discovery mode")
		}

		if platformType != consts.Baremetal {
			Skip("TLS tests are not supported on non-baremetal platforms")
		}

		err := namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())

		featureFlagInitialValue := isFeatureFlagEnabled(consts.MetricsExporterFeatureGate)
		DeferCleanup(func() {
			By("Restoring metricsExporter feature flag")
			setFeatureFlag(consts.MetricsExporterFeatureGate, featureFlagInitialValue)
		})

		By("Enabling metricsExporter feature flag")
		setFeatureFlag(consts.MetricsExporterFeatureGate, true)

		WaitForSRIOVStable()

		node = sriovInfos.Nodes[0]
		By("Using node " + node)
	})

	AfterAll(func() {
		err := namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())
		WaitForSRIOVStable()
	})

	Context("Kubernetes cluster", func() {
		BeforeAll(func() {
			if isOpenShiftCluster() {
				Skip("Kubernetes TLS tests are skipped on OpenShift clusters")
			}
		})

		It("should propagate TLS env vars to operand DaemonSets", func() {
			By("Getting the operator deployment")
			deploy := getOperatorDeployment()

			originalCiphers := getDeploymentEnvVar(deploy, "TLS_CIPHER_SUITES")
			originalMinVersion := getDeploymentEnvVar(deploy, "TLS_MIN_VERSION")
			DeferCleanup(func() {
				By("Restoring original TLS environment variables")
				restoreOperatorTLSEnv(originalCiphers, originalMinVersion)
			})

			ciphers := "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"
			minVersion := "VersionTLS12"

			By("Setting TLS environment variables on the operator deployment")
			setOperatorTLSEnv(ciphers, minVersion)

			By("Waiting for operator pod to be running with new env")
			waitForOperatorPodReady()

			By("Validating operator-webhook DaemonSet pods have TLS args")
			assertOperandHasTLSArgs("app=operator-webhook", "webhook-server",
				"--tls-cipher-suites="+ciphers, "--tls-min-version="+minVersion)

			By("Validating network-resources-injector DaemonSet pods have TLS args")
			assertOperandHasTLSArgs("app=network-resources-injector", "webhook-server",
				"-tls-cipher-suites="+ciphers, "-tls-min-version="+minVersion)

			By("Validating metrics-exporter kube-rbac-proxy has TLS args")
			assertOperandHasTLSArgs("app=sriov-network-metrics-exporter", "kube-rbac-proxy",
				"--tls-cipher-suites="+ciphers, "--tls-min-version="+minVersion)

			By("Creating a policy, network and pod to validate operands still work")
			validateOperandsWorkWithPolicy(node)
		})

		It("should update operands when TLS configuration changes", func() {
			By("Getting the operator deployment")
			deploy := getOperatorDeployment()

			originalCiphers := getDeploymentEnvVar(deploy, "TLS_CIPHER_SUITES")
			originalMinVersion := getDeploymentEnvVar(deploy, "TLS_MIN_VERSION")
			DeferCleanup(func() {
				By("Restoring original TLS environment variables")
				restoreOperatorTLSEnv(originalCiphers, originalMinVersion)
			})

			firstCiphers := "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
			By("Setting initial TLS cipher suite")
			setOperatorTLSEnv(firstCiphers, "VersionTLS12")
			waitForOperatorPodReady()

			assertOperandHasTLSArgs("app=operator-webhook", "webhook-server",
				"--tls-cipher-suites="+firstCiphers, "--tls-min-version=VersionTLS12")

			updatedCiphers := "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256"
			By("Updating TLS cipher suites to a new value")
			setOperatorTLSEnv(updatedCiphers, "VersionTLS13")
			waitForOperatorPodReady()

			By("Validating operator-webhook has updated TLS args")
			assertOperandHasTLSArgs("app=operator-webhook", "webhook-server",
				"--tls-cipher-suites="+updatedCiphers, "--tls-min-version=VersionTLS13")

			By("Validating network-resources-injector has updated TLS args")
			assertOperandHasTLSArgs("app=network-resources-injector", "webhook-server",
				"-tls-cipher-suites="+updatedCiphers, "-tls-min-version=VersionTLS13")

			By("Validating metrics-exporter kube-rbac-proxy has updated TLS args")
			assertOperandHasTLSArgs("app=sriov-network-metrics-exporter", "kube-rbac-proxy",
				"--tls-cipher-suites="+updatedCiphers, "--tls-min-version=VersionTLS13")
		})
	})

	Context("OpenShift cluster", func() {
		BeforeAll(func() {
			if !isOpenShiftCluster() {
				Skip("OpenShift TLS tests are skipped on Kubernetes clusters")
			}
		})

		It("should NOT propagate TLS profile when tlsAdherence is unset or LegacyAdheringComponentsOnly", func() {
			By("Saving the current APIServer state")
			apiServer := getAPIServer()
			originalProfile := apiServer.Spec.TLSSecurityProfile
			originalAdherence := apiServer.Spec.TLSAdherence
			DeferCleanup(func() {
				By("Restoring original APIServer configuration")
				restoreAPIServerConfig(originalProfile, originalAdherence)
			})

			By("Setting Intermediate TLS profile with LegacyAdheringComponentsOnly adherence")
			setAPIServerConfig(&configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			}, configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly)

			By("Validating operands do NOT have TLS args from the cluster profile")
			assertOperandDoesNotHaveTLSArgs("app=operator-webhook", "webhook-server",
				"--tls-cipher-suites=")

			assertOperandDoesNotHaveTLSArgs("app=network-resources-injector", "webhook-server",
				"-tls-cipher-suites=")
		})

		It("should propagate TLS profile when tlsAdherence is StrictAllComponents", func() {
			if !isTLSAdherenceSupported() {
				Skip("TLSAdherence feature gate is not enabled on this cluster")
			}

			By("Saving the current APIServer state")
			apiServer := getAPIServer()
			originalProfile := apiServer.Spec.TLSSecurityProfile
			originalAdherence := apiServer.Spec.TLSAdherence
			DeferCleanup(func() {
				By("Restoring original APIServer configuration")
				restoreAPIServerConfig(originalProfile, originalAdherence)
			})

			By("Setting Intermediate TLS profile with StrictAllComponents adherence")
			setAPIServerConfig(&configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			}, configv1.TLSAdherencePolicyStrictAllComponents)

			intermediateSpec := configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
			expectedMinVersion := string(intermediateSpec.MinTLSVersion)

			By("Validating operator-webhook has TLS args from the cluster profile")
			assertOperandHasTLSArgs("app=operator-webhook", "webhook-server",
				"--tls-min-version="+expectedMinVersion)

			By("Validating network-resources-injector has TLS args")
			assertOperandHasTLSArgs("app=network-resources-injector", "webhook-server",
				"-tls-min-version="+expectedMinVersion)

			By("Validating metrics-exporter kube-rbac-proxy has TLS args")
			assertOperandHasTLSArgs("app=sriov-network-metrics-exporter", "kube-rbac-proxy",
				"--tls-min-version="+expectedMinVersion)

			By("Creating a policy, network and pod to validate operands still work")
			validateOperandsWorkWithPolicy(node)
		})

		It("should update operands when tlsAdherence changes from Legacy to Strict", func() {
			if !isTLSAdherenceSupported() {
				Skip("TLSAdherence feature gate is not enabled on this cluster")
			}

			By("Saving the current APIServer state")
			apiServer := getAPIServer()
			originalProfile := apiServer.Spec.TLSSecurityProfile
			originalAdherence := apiServer.Spec.TLSAdherence
			DeferCleanup(func() {
				By("Restoring original APIServer configuration")
				restoreAPIServerConfig(originalProfile, originalAdherence)
			})

			By("Setting Intermediate TLS profile with LegacyAdheringComponentsOnly (operator should NOT honor)")
			setAPIServerConfig(&configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			}, configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly)

			By("Validating operands do NOT have TLS args from the cluster profile")
			assertOperandDoesNotHaveTLSArgs("app=operator-webhook", "webhook-server",
				"--tls-cipher-suites=")

			By("Updating tlsAdherence to StrictAllComponents (operator should now honor)")
			setAPIServerConfig(&configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			}, configv1.TLSAdherencePolicyStrictAllComponents)

			intermediateSpec := configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
			expectedMinVersion := string(intermediateSpec.MinTLSVersion)

			By("Validating operator-webhook now has TLS args")
			assertOperandHasTLSArgs("app=operator-webhook", "webhook-server",
				"--tls-min-version="+expectedMinVersion)

			By("Validating network-resources-injector now has TLS args")
			assertOperandHasTLSArgs("app=network-resources-injector", "webhook-server",
				"-tls-min-version="+expectedMinVersion)

			By("Validating metrics-exporter kube-rbac-proxy now has TLS args")
			assertOperandHasTLSArgs("app=sriov-network-metrics-exporter", "kube-rbac-proxy",
				"--tls-min-version="+expectedMinVersion)
		})
	})
})

// isOpenShiftCluster detects if the cluster is OpenShift by checking
// for the availability of the config.openshift.io API group.
func isOpenShiftCluster() bool {
	_, err := clients.DiscoveryInterface.ServerResourcesForGroupVersion("config.openshift.io/v1")
	return err == nil
}

// isTLSAdherenceSupported checks if the TLSAdherence feature gate is enabled on this cluster
// by writing the field and reading it back. If the field is stripped (not preserved), the
// feature gate is not enabled.
func isTLSAdherenceSupported() bool {
	apiServer := getAPIServer()

	apiServer.Spec.TLSAdherence = configv1.TLSAdherencePolicyStrictAllComponents
	err := clients.Update(context.Background(), apiServer)
	if err != nil {
		return false
	}

	updated := getAPIServer()
	supported := updated.Spec.TLSAdherence == configv1.TLSAdherencePolicyStrictAllComponents

	// Restore to LegacyAdheringComponentsOnly (the field cannot be removed once set)
	updated.Spec.TLSAdherence = configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly
	clients.Update(context.Background(), updated) //nolint:errcheck

	return supported
}

// getOperatorDeployment retrieves the sriov-network-operator Deployment.
func getOperatorDeployment() *appsv1.Deployment {
	deploy, err := clients.AppsV1Interface.Deployments(operatorNamespace).Get(
		context.Background(), "sriov-network-operator", metav1.GetOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return deploy
}

// getDeploymentEnvVar returns the value of an environment variable in the operator container.
// Returns empty string if the env var is not set.
func getDeploymentEnvVar(deploy *appsv1.Deployment, envName string) string {
	for i := range deploy.Spec.Template.Spec.Containers {
		if deploy.Spec.Template.Spec.Containers[i].Name == "sriov-network-operator" {
			for _, env := range deploy.Spec.Template.Spec.Containers[i].Env {
				if env.Name == envName {
					return env.Value
				}
			}
		}
	}
	return ""
}

// setOperatorTLSEnv updates the TLS_CIPHER_SUITES and TLS_MIN_VERSION env vars
// on the operator deployment and waits for the new pod to be running.
func setOperatorTLSEnv(ciphers, minVersion string) {
	Eventually(func(g Gomega) {
		deploy := getOperatorDeployment()
		updated := false
		for i := range deploy.Spec.Template.Spec.Containers {
			if deploy.Spec.Template.Spec.Containers[i].Name == "sriov-network-operator" {
				deploy.Spec.Template.Spec.Containers[i].Env = setEnvVar(
					deploy.Spec.Template.Spec.Containers[i].Env, "TLS_CIPHER_SUITES", ciphers)
				deploy.Spec.Template.Spec.Containers[i].Env = setEnvVar(
					deploy.Spec.Template.Spec.Containers[i].Env, "TLS_MIN_VERSION", minVersion)
				updated = true
				break
			}
		}
		g.Expect(updated).To(BeTrue(), "sriov-network-operator container not found in deployment")

		_, err := clients.AppsV1Interface.Deployments(operatorNamespace).Update(
			context.Background(), deploy, metav1.UpdateOptions{})
		g.Expect(err).ToNot(HaveOccurred())
	}, 1*time.Minute, 5*time.Second).Should(Succeed())
}

// restoreOperatorTLSEnv restores TLS env vars on the operator deployment.
func restoreOperatorTLSEnv(ciphers, minVersion string) {
	setOperatorTLSEnv(ciphers, minVersion)
	waitForOperatorPodReady()
	WaitForSRIOVStable()
}

// setEnvVar sets or updates an environment variable in a list of env vars.
func setEnvVar(envs []corev1.EnvVar, name, value string) []corev1.EnvVar {
	for i := range envs {
		if envs[i].Name == name {
			envs[i].Value = value
			return envs
		}
	}
	return append(envs, corev1.EnvVar{Name: name, Value: value})
}

// waitForOperatorPodReady waits for the operator pod to be running after a deployment update.
func waitForOperatorPodReady() {
	Eventually(func(g Gomega) {
		podList, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "name=sriov-network-operator",
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(podList.Items).ToNot(BeEmpty(), "No operator pod found")

		readyPods := 0
		for _, p := range podList.Items {
			if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
				for _, c := range p.Status.ContainerStatuses {
					if c.Ready {
						readyPods++
					}
				}
			}
		}
		g.Expect(readyPods).To(BeNumerically(">=", 1))
	}, 3*time.Minute, 5*time.Second).Should(Succeed())

	WaitForSRIOVStable()
}

// assertOperandHasTLSArgs validates that the specified operand pods have the expected TLS arguments.
func assertOperandHasTLSArgs(labelSelector, containerName string, expectedArgs ...string) {
	Eventually(func(g Gomega) {
		pods, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pods.Items).ToNot(BeEmpty(),
			fmt.Sprintf("No pods found with label selector %s", labelSelector))

		for _, p := range pods.Items {
			if p.DeletionTimestamp != nil || p.Status.Phase != corev1.PodRunning {
				continue
			}
			args := getContainerArgs(p, containerName)
			for _, expected := range expectedArgs {
				g.Expect(args).To(ContainElement(ContainSubstring(expected)),
					fmt.Sprintf("Pod %s/%s container %s missing arg %q in args: %v",
						p.Namespace, p.Name, containerName, expected, args))
			}
		}
	}, 5*time.Minute, 10*time.Second).Should(Succeed())
}

// getContainerArgs returns the args for a specific container in a pod.
func getContainerArgs(p corev1.Pod, containerName string) []string {
	for _, c := range p.Spec.Containers {
		if c.Name == containerName {
			return c.Args
		}
	}
	return nil
}

// getAPIServer retrieves the OpenShift APIServer cluster resource.
func getAPIServer() *configv1.APIServer {
	apiServer := &configv1.APIServer{}
	err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "cluster"}, apiServer)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return apiServer
}

// setAPIServerConfig updates both the TLS profile and tlsAdherence on the APIServer CR.
func setAPIServerConfig(profile *configv1.TLSSecurityProfile, adherence configv1.TLSAdherencePolicy) {
	Eventually(func(g Gomega) {
		apiServer := &configv1.APIServer{}
		err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "cluster"}, apiServer)
		g.Expect(err).ToNot(HaveOccurred())

		apiServer.Spec.TLSSecurityProfile = profile
		apiServer.Spec.TLSAdherence = adherence
		err = clients.Update(context.Background(), apiServer)
		g.Expect(err).ToNot(HaveOccurred())
	}, 1*time.Minute, 5*time.Second).Should(Succeed())

	WaitForSRIOVStable()
}

// restoreAPIServerConfig restores the original TLS profile and tlsAdherence.
// Note: once tlsAdherence is set on the cluster it cannot be removed, so if the
// original was empty we fall back to LegacyAdheringComponentsOnly (the default).
func restoreAPIServerConfig(profile *configv1.TLSSecurityProfile, adherence configv1.TLSAdherencePolicy) {
	if adherence == "" {
		adherence = configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly
	}
	setAPIServerConfig(profile, adherence)
}

// assertOperandDoesNotHaveTLSArgs validates that operand pods do NOT have the specified TLS arg prefix.
func assertOperandDoesNotHaveTLSArgs(labelSelector, containerName, argPrefix string) {
	Eventually(func(g Gomega) {
		pods, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pods.Items).ToNot(BeEmpty(),
			fmt.Sprintf("No pods found with label selector %s", labelSelector))

		for _, p := range pods.Items {
			if p.DeletionTimestamp != nil || p.Status.Phase != corev1.PodRunning {
				continue
			}
			args := getContainerArgs(p, containerName)
			for _, arg := range args {
				g.Expect(arg).ToNot(HavePrefix(argPrefix),
					fmt.Sprintf("Pod %s/%s container %s should NOT have arg with prefix %q, but found: %s",
						p.Namespace, p.Name, containerName, argPrefix, arg))
			}
		}
	}, 3*time.Minute, 10*time.Second).Should(Succeed())
}

// validateOperandsWorkWithPolicy creates a SRIOV policy, network, and pod to confirm
// that the operands (webhook, injector) are functioning correctly after a TLS config change.
// It validates:
//   - Policy creation goes through the operator-webhook (TLS-enabled)
//   - Network reconciles into a NAD (controller working)
//   - Pod with network annotation is admitted through the injector webhook (TLS-enabled)
func validateOperandsWorkWithPolicy(testNode string) {
	intf := findInterface(sriovInfos, testNode)

	resourceName := "tlstestresource"

	By("Creating a SriovNetworkNodePolicy (validates operator-webhook with TLS)")
	_, err := network.CreateSriovPolicy(clients, "test-tls-policy-", operatorNamespace,
		intf.Name, testNode, 2, resourceName, "netdevice")
	Expect(err).ToNot(HaveOccurred())

	WaitForSRIOVStable()

	By("Waiting for VFs to be allocatable on the node")
	Eventually(func() int64 {
		testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), testNode, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
		capacity, _ := resNum.AsInt64()
		return capacity
	}, 10*time.Minute, time.Second).Should(Equal(int64(2)))

	By("Waiting for device-plugin pods to be ready")
	Eventually(func(g Gomega) {
		pods, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "app=sriov-device-plugin",
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pods.Items).ToNot(BeEmpty())
		for _, p := range pods.Items {
			g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning))
			for _, cs := range p.Status.ContainerStatuses {
				g.Expect(cs.Ready).To(BeTrue())
			}
		}
	}, 3*time.Minute, 5*time.Second).Should(Succeed())

	By("Creating a SriovNetwork and waiting for NAD (validates controller reconciliation)")
	err = network.CreateSriovNetwork(clients, intf, "test-tls-network", namespaces.Test,
		operatorNamespace, resourceName, ipamIpv4)
	Expect(err).ToNot(HaveOccurred())
	waitForNetAttachDef("test-tls-network", namespaces.Test)

	By("Creating a pod with network annotation (validates injector webhook with TLS)")
	testPod := createTestPod(testNode, []string{"test-tls-network"})
	Expect(testPod.Status.Phase).To(Equal(corev1.PodRunning))

	By("Cleaning up test resources")
	err = namespaces.CleanPods(namespaces.Test, clients)
	Expect(err).ToNot(HaveOccurred())

	err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
	Expect(err).ToNot(HaveOccurred())

	WaitForSRIOVStable()
}
