package tests

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	sriovdrav1alpha1 "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/api/sriovdra/v1alpha1"
	configapi "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/api/virtualfunction/v1alpha1"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"
)

const (
	draResourceName      = "draresource"
	initialVFs           = 2
	updatedVFs           = 4
	devicePluginDSName   = "sriov-device-plugin"
	draDriverAppLabel    = "app=sriov-dra-driver"
	devicePluginAppLabel = "app=sriov-device-plugin"
)

// isDeviceClassAPIAvailable reports whether resource.k8s.io/v1 is discoverable.
// It returns (false, nil) only when the API is conclusively unavailable (NotFound /
// NoMatch). Other discovery failures (auth, outage, etc.) are returned as errors.
func isDeviceClassAPIAvailable() (bool, error) {
	_, err := clients.ServerResourcesForGroupVersion(schema.GroupVersion{
		Group:   "resource.k8s.io",
		Version: "v1",
	}.String())
	if err == nil {
		return true, nil
	}
	if errors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return false, nil
	}
	return false, fmt.Errorf("discovering resource.k8s.io/v1 DeviceClass API: %w", err)
}

// draDriverInitContainersTerminated reports whether the pod has a wait-for-config
// init container and every init container has terminated successfully (exit code 0).
func draDriverInitContainersTerminated(pod *corev1.Pod) bool {
	statuses := make(map[string]corev1.ContainerStatus, len(pod.Status.InitContainerStatuses))
	for _, status := range pod.Status.InitContainerStatuses {
		statuses[status.Name] = status
	}

	hasWaitForConfig := false
	for _, initContainer := range pod.Spec.InitContainers {
		if slices.Contains(initContainer.Command, "wait-for-config") {
			hasWaitForConfig = true
		}

		status, found := statuses[initContainer.Name]
		if !found || status.State.Terminated == nil || status.State.Terminated.ExitCode != 0 {
			return false
		}
	}
	return hasWaitForConfig
}

func listDRADriverPods(ctx context.Context) []corev1.Pod {
	pods, err := clients.Pods(operatorNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: draDriverAppLabel,
	})
	Expect(err).ToNot(HaveOccurred())
	return pods.Items
}

func countAdvertisedDRADevices(ctx context.Context, nodeName, resourceAttribute string) (int, error) {
	list := &resourceapi.ResourceSliceList{}
	if err := clients.List(ctx, list); err != nil {
		return 0, err
	}

	// During a pool update the driver may publish the old and new generations together.
	// Count only the highest generation for each pool on this node.
	generation := map[string]int64{}
	for i := range list.Items {
		slice := &list.Items[i]
		if !draSliceForNode(slice, nodeName) {
			continue
		}
		if slice.Spec.Pool.Generation > generation[slice.Spec.Pool.Name] {
			generation[slice.Spec.Pool.Name] = slice.Spec.Pool.Generation
		}
	}

	count := 0
	attrKey := resourceapi.QualifiedName(consts.DRAResourceNameAttributeKey)
	for i := range list.Items {
		slice := &list.Items[i]
		if !draSliceForNode(slice, nodeName) || slice.Spec.Pool.Generation != generation[slice.Spec.Pool.Name] {
			continue
		}
		for _, device := range slice.Spec.Devices {
			attr, ok := device.Attributes[attrKey]
			if !ok || attr.StringValue == nil || *attr.StringValue != resourceAttribute {
				continue
			}
			count++
		}
	}
	return count, nil
}

func draSliceForNode(slice *resourceapi.ResourceSlice, nodeName string) bool {
	return slice.Spec.Driver == consts.DRADriverBaseDeviceClassName &&
		slice.Spec.NodeName != nil &&
		*slice.Spec.NodeName == nodeName
}

// waitForDRAProviderSwitch performs the user-managed checks required before
// creating policies after a feature-gate change, including during suite cleanup.
func waitForDRAProviderSwitch(ctx context.Context, draEnabled bool) {
	selected, previous := devicePluginDSName, consts.DRADriverDaemonSetName
	if draEnabled {
		selected, previous = previous, selected
	}
	By(fmt.Sprintf("Waiting for %s removal and %s rollout before creating policies", previous, selected))
	Eventually(ctx, func(g Gomega) {
		err := clients.Get(ctx, runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: previous}, &appsv1.DaemonSet{})
		g.Expect(errors.IsNotFound(err)).To(BeTrue(), "previous provider DaemonSet still exists: %v", err)
		oldPods, err := clients.Pods(operatorNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + previous})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(oldPods.Items).To(BeEmpty())

		ds := &appsv1.DaemonSet{}
		g.Expect(clients.Get(ctx, runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: selected}, ds)).To(Succeed())
		g.Expect(ds.DeletionTimestamp.IsZero()).To(BeTrue())
		g.Expect(ds.Status.ObservedGeneration).To(BeNumerically(">=", ds.Generation))
		g.Expect(ds.Status.UpdatedNumberScheduled).To(Equal(ds.Status.DesiredNumberScheduled))
		g.Expect(ds.Status.NumberAvailable).To(Equal(ds.Status.DesiredNumberScheduled))
		g.Expect(ds.Status.NumberReady).To(Equal(ds.Status.DesiredNumberScheduled))
		// The device plugin may have no eligible nodes until policies exist.
		if draEnabled {
			g.Expect(ds.Status.DesiredNumberScheduled).To(BeNumerically(">", 0))
		}
		pods, err := clients.Pods(operatorNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + selected})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(pods.Items).To(HaveLen(int(ds.Status.DesiredNumberScheduled)))
		for _, p := range pods.Items {
			g.Expect(p.DeletionTimestamp.IsZero()).To(BeTrue())
			g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning))
			g.Expect(pod.IsReady(&p)).To(BeTrue(), "provider pod %s is not Ready", p.Name)
			if draEnabled {
				g.Expect(draDriverInitContainersTerminated(&p)).To(BeTrue(), "DRA pod %s is still in Init", p.Name)
			}
		}
	}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())
}

// testDRAVFIOExtendedResource runs after the advertisement tests, before the
// ordered suite removes the policy and switches back to the device plugin.
func testDRAVFIOExtendedResource(ctx SpecContext, policyName string) {
	class := &resourceapi.DeviceClass{}
	Expect(clients.Get(ctx, runtimeclient.ObjectKey{Name: sriovv1.ResourceNameToDeviceClassName(draResourceName)}, class)).To(Succeed())
	if class.Spec.ExtendedResourceName == nil {
		Skip("DRAExtendedResource is not enabled in the API server")
	}
	extendedResource := *class.Spec.ExtendedResourceName
	infos, err := cluster.DiscoverSriov(clients, operatorNamespace)
	Expect(err).NotTo(HaveOccurred())
	worker, nic := infos.FindOneVfioSriovDevice()
	if worker == "" {
		Skip("no VFIO-capable SR-IOV device is available")
	}
	node, err := clients.Nodes().Get(ctx, worker, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	hostname := node.Labels[corev1.LabelHostname]
	Expect(hostname).NotTo(BeEmpty())

	By("Configuring a VFIO resource through the existing DRA policy")
	Eventually(ctx, func(g Gomega) {
		policy := &sriovv1.SriovNetworkNodePolicy{}
		g.Expect(clients.Get(ctx, runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: policyName}, policy)).To(Succeed())
		policy.Spec.DeviceType = consts.DeviceTypeVfioPci
		policy.Spec.NumVfs = 1
		policy.Spec.NodeSelector = map[string]string{corev1.LabelHostname: hostname}
		policy.Spec.NicSelector = sriovv1.SriovNetworkNicSelector{PfNames: []string{nic.Name}}
		g.Expect(clients.Update(ctx, policy)).To(Succeed())
	}).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
	WaitForSRIOVStable()
	Eventually(ctx, func(g Gomega) {
		g.Expect(clients.Get(ctx, runtimeclient.ObjectKey{Name: class.Name}, class)).To(Succeed())
		g.Expect(class.Spec.Config).To(HaveLen(1))
		g.Expect(class.Spec.Config[0].Opaque).NotTo(BeNil())
		decoded, err := runtime.Decode(configapi.Decoder, class.Spec.Config[0].Opaque.Parameters.Raw)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(decoded.(*configapi.VfConfig).Driver).To(Equal(consts.DeviceTypeVfioPci))
		count, err := countAdvertisedDRADevices(ctx, worker, extendedResource)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(count).To(Equal(1))
	}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())

	By("Requesting a VF through limits without an explicit claim or privileged access")
	consumer := pod.GetDefinition()
	consumer.Spec.NodeSelector = map[string]string{corev1.LabelHostname: hostname}
	consumer.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: ptr.To(false)}
	consumer.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceName(extendedResource): resource.MustParse("1")},
		Limits:   corev1.ResourceList{corev1.ResourceName(extendedResource): resource.MustParse("1")},
	}
	Expect(clients.Create(ctx, consumer)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		Expect(clients.Delete(ctx, consumer)).To(Succeed())
		Eventually(ctx, func() bool {
			return errors.IsNotFound(clients.Get(ctx, runtimeclient.ObjectKeyFromObject(consumer), &corev1.Pod{}))
		}).WithTimeout(waitingTime).WithPolling(time.Second).Should(BeTrue())
	})
	Eventually(ctx, func(g Gomega) {
		g.Expect(clients.Get(ctx, runtimeclient.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		g.Expect(pod.AssertContainerReady(*consumer, consumer.Spec.Containers[0].Name)).To(Succeed())
		g.Expect(consumer.Status.ExtendedResourceClaimStatus).NotTo(BeNil())
	}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())
	claim := &resourceapi.ResourceClaim{}
	Expect(clients.Get(ctx, runtimeclient.ObjectKey{Namespace: namespaces.Test,
		Name: consumer.Status.ExtendedResourceClaimStatus.ResourceClaimName}, claim)).To(Succeed())
	Expect(claim.Status.Allocation).NotTo(BeNil())
	Expect(claim.Status.Allocation.Devices.Results).To(HaveLen(1))
	Expect(claim.Status.Allocation.Devices.Config).To(ContainElement(SatisfyAll(
		HaveField("Source", resourceapi.AllocationConfigSourceClass),
		HaveField("DeviceConfiguration.Opaque.Driver", consts.DRADriverBaseDeviceClassName),
	)))

	By("Checking the claim CDI specification includes both VFIO character devices")
	cdiRoot := ""
	for _, driverPod := range listDRADriverPods(ctx) {
		if driverPod.Spec.NodeName != worker {
			continue
		}
		for _, volume := range driverPod.Spec.Volumes {
			if volume.Name == "cdi-root" && volume.HostPath != nil {
				cdiRoot = volume.HostPath.Path
			}
		}
	}
	Expect(cdiRoot).NotTo(BeEmpty())
	execCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// Read from the config daemon's host mount, without exposing host /dev to
	// the consumer. Pass paths as shell arguments rather than interpolating them.
	contents, stderr, err := runCommandOnConfigDaemonWithContext(execCtx, worker, "sh", "-c",
		`for f in "$1"/*"$2"*; do if [ -f "$f" ]; then cat "$f"; exit; fi; done; exit 1`,
		"read-cdi", filepath.Join("/host", cdiRoot), string(claim.UID))
	Expect(err).NotTo(HaveOccurred(), stderr)
	var spec struct {
		Devices []struct {
			ContainerEdits struct {
				DeviceNodes []struct {
					Path string `json:"path"`
					Type string `json:"type"`
				} `json:"deviceNodes"`
			} `json:"containerEdits"`
		} `json:"devices"`
	}
	Expect(yaml.Unmarshal([]byte(contents), &spec)).To(Succeed())
	Expect(spec.Devices).To(HaveLen(1))
	paths := []string{}
	groupPath := ""
	for _, device := range spec.Devices[0].ContainerEdits.DeviceNodes {
		if strings.HasPrefix(device.Path, "/dev/vfio/") {
			Expect(device.Type).To(Equal("c"))
			paths = append(paths, device.Path)
			if device.Path != "/dev/vfio/vfio" && !strings.Contains(strings.TrimPrefix(device.Path, "/dev/vfio/"), "/") {
				groupPath = device.Path
			}
		}
	}
	Expect(paths).To(ContainElement("/dev/vfio/vfio"))
	Expect(groupPath).NotTo(BeEmpty())
	_, stderr, err = pod.ExecCommandWithContext(execCtx, clients, consumer, "sh", "-c",
		`test -c /dev/vfio/vfio && test -c "$1" && exec 3<>/dev/vfio/vfio && exec 4<>"$1"`, "check-vfio", groupPath)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("VFIO device access failed: %s", stderr))
}

// Ordered is required: later specs depend on earlier ones (enable DRA => create
// SriovNetworkNodePolicy / unblock init => ResourceSlices publish initial VFs =>
// NumVfs change updates ResourceSlices => drain on policy deletion => disable and clean up).
var _ = Describe("[sriov] DRA", Ordered, func() {
	var (
		node             string
		nic              *sriovv1.InterfaceExt
		policyName       string
		deviceClassName  string
		deviceAttrsName  string
		extendedResource string
	)

	BeforeAll(func() {
		if discovery.Enabled() {
			Skip("DRA conformance suite changes the resource provider, which is unsuitable for discovery mode")
		}
		if platformType != consts.Baremetal {
			Skip("DRA operator tests require a baremetal (or emulated-PF) platform")
		}
		available, err := isDeviceClassAPIAvailable()
		Expect(err).ToNot(HaveOccurred())
		if !available {
			Skip("resource.k8s.io/v1 DeviceClass API is not available on this cluster")
		}

		err = namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())
		WaitForSRIOVStable()

		featureFlagInitialValue := isFeatureFlagEnabled(consts.DynamicResourceAllocationFeatureGate)
		DeferCleanup(func(ctx SpecContext) {
			By("Restoring dynamicResourceAllocation feature flag and cleaning test state")
			// Switching providers in either direction requires removing policies first.
			if err := namespaces.CleanPods(namespaces.Test, clients); err != nil {
				AddReportEntry("DRA teardown pod cleanup failed", err.Error())
			}
			if err := namespaces.CleanNetworks(operatorNamespace, clients); err != nil {
				AddReportEntry("DRA teardown network cleanup failed", err.Error())
			}
			if err := namespaces.CleanPolicies(operatorNamespace, clients); err != nil {
				AddReportEntry("DRA teardown policy cleanup failed", err.Error())
			}
			if err := namespaces.CleanPools(operatorNamespace, clients); err != nil {
				AddReportEntry("DRA teardown pool cleanup failed", err.Error())
			}
			Eventually(func(g Gomega) {
				policies := &sriovv1.SriovNetworkNodePolicyList{}
				g.Expect(clients.List(context.Background(), policies,
					runtimeclient.InNamespace(operatorNamespace))).To(Succeed())
				for _, policy := range policies.Items {
					g.Expect(policy.Name == "default" || !strings.HasPrefix(policy.Name, "test-")).To(BeTrue(),
						"test policy %q has not been removed", policy.Name)
				}
			}).WithTimeout(waitingTime).WithPolling(time.Second).Should(Succeed())
			WaitForSRIOVStable()
			setFeatureFlag(consts.DynamicResourceAllocationFeatureGate, featureFlagInitialValue)
			WaitForSRIOVStable()
			waitForDRAProviderSwitch(ctx, featureFlagInitialValue)
		})

		sriovInfos, err := cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		node, nic, err = sriovInfos.FindOneSriovNodeAndDevice()
		if err == nil && (nic.TotalVfs < initialVFs || nic.TotalVfs < updatedVFs) {
			for _, candidateNode := range sriovInfos.Nodes {
				devices, err := sriovInfos.FindSriovDevices(candidateNode)
				Expect(err).ToNot(HaveOccurred())
				for _, candidateNIC := range devices {
					if candidateNIC.TotalVfs >= initialVFs && candidateNIC.TotalVfs >= updatedVFs {
						node, nic = candidateNode, candidateNIC
						break
					}
				}
				if nic.TotalVfs >= initialVFs && nic.TotalVfs >= updatedVFs {
					break
				}
			}
		}
		if err != nil || nic.TotalVfs < initialVFs || nic.TotalVfs < updatedVFs {
			Skip(fmt.Sprintf("DRA conformance suite requires a PF with at least %d total VFs", updatedVFs))
		}
		By(fmt.Sprintf("Using device %s on node %s", nic.Name, node))

		deviceClassName = sriovv1.ResourceNameToDeviceClassName(draResourceName)
		deviceAttrsName = deviceClassName + "-attrs"

		// Enable DRA before creating a SriovNetworkNodePolicy. Nodes with no
		// interfaces are unblocked, so the DRA driver leaves Init without a policy.
		By("Enabling dynamicResourceAllocation feature flag")
		setFeatureFlag(consts.DynamicResourceAllocationFeatureGate, true)
	})

	It("should deploy the DRA driver DaemonSet and remove the device plugin", func(ctx SpecContext) {
		waitForDRAProviderSwitch(ctx, true)
	})

	It("should create the base DeviceClass managed by the operator", func(ctx SpecContext) {
		Eventually(ctx, func(g Gomega) {
			dc := &resourceapi.DeviceClass{}
			err := clients.Get(ctx, runtimeclient.ObjectKey{
				Name: consts.DRADriverBaseDeviceClassName,
			}, dc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(dc.Labels).To(HaveKeyWithValue(consts.DRAGeneratedByLabel, consts.SriovNetworkOperatorIdentifier))
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})

	It("should create SriovNetworkNodePolicy and unblock DRA driver init with DRA CRs", func(ctx SpecContext) {
		By("Creating SriovNetworkNodePolicy for resource " + draResourceName)
		created, err := network.CreateSriovPolicy(clients, "test-dra-policy-", operatorNamespace, nic.Name, node, initialVFs, draResourceName, "netdevice")
		Expect(err).ToNot(HaveOccurred())
		policyName = created.Name
		WaitForSRIOVStable()

		By("Waiting for DRA driver pods to leave Init and become Running")
		Eventually(ctx, func(g Gomega) {
			pods, err := clients.Pods(operatorNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: draDriverAppLabel,
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(pods.Items).ToNot(BeEmpty())

			readyPods := 0
			for _, p := range pods.Items {
				g.Expect(draDriverInitContainersTerminated(&p)).To(BeTrue(),
					"DRA driver pod %s still has incomplete init containers: %v", p.Name, p.Status.InitContainerStatuses)
				g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning),
					"DRA driver pod %s phase=%s", p.Name, p.Status.Phase)
				if pod.IsReady(&p) {
					readyPods++
				}
			}
			g.Expect(readyPods).To(BeNumerically(">", 0))
		}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for sriov-dra-driver DaemonSet to finish rollout")
		Eventually(ctx, func(g Gomega) {
			ds := &appsv1.DaemonSet{}
			err := clients.Get(ctx, runtimeclient.ObjectKey{
				Namespace: operatorNamespace,
				Name:      consts.DRADriverDaemonSetName,
			}, ds)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ds.Status.ObservedGeneration).To(BeNumerically(">=", ds.Generation))
			g.Expect(ds.Status.NumberReady).To(Equal(ds.Status.DesiredNumberScheduled))
			g.Expect(ds.Status.UpdatedNumberScheduled).To(Equal(ds.Status.DesiredNumberScheduled))
		}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for DeviceAttributes " + deviceAttrsName)
		attrs := &sriovdrav1alpha1.DeviceAttributes{}
		Eventually(ctx, func(g Gomega) {
			err := clients.Get(ctx, runtimeclient.ObjectKey{
				Namespace: operatorNamespace,
				Name:      deviceAttrsName,
			}, attrs)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(attrs.Labels).To(HaveKeyWithValue(consts.DRAGeneratedByLabel, consts.SriovNetworkOperatorIdentifier))
			g.Expect(attrs.Labels).To(HaveKeyWithValue(consts.DRAResourcePoolLabel, deviceClassName))
			attribute, ok := attrs.Spec.Attributes[resourceapi.QualifiedName(consts.DRAResourceNameAttributeKey)]
			g.Expect(ok).To(BeTrue())
			g.Expect(attribute.StringValue).NotTo(BeNil())
			if ok && attribute.StringValue != nil {
				extendedResource = *attribute.StringValue
			}
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for SriovResourcePolicy for node " + node)
		Eventually(ctx, func(g Gomega) {
			policy := &sriovdrav1alpha1.SriovResourcePolicy{}
			err := clients.Get(ctx, runtimeclient.ObjectKey{
				Namespace: operatorNamespace,
				Name:      node,
			}, policy)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(policy.Labels).To(HaveKeyWithValue(consts.DRAGeneratedByLabel, consts.SriovNetworkOperatorIdentifier))
			g.Expect(policy.Labels).To(HaveKeyWithValue(consts.DRASriovResourcePolicyNodeLabel, node))
			g.Expect(policy.Spec.Configs).ToNot(BeEmpty())
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for per-resource DeviceClass " + deviceClassName)
		Eventually(ctx, func(g Gomega) {
			dc := &resourceapi.DeviceClass{}
			err := clients.Get(ctx, runtimeclient.ObjectKey{Name: deviceClassName}, dc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(dc.Labels).To(HaveKeyWithValue(consts.DRAGeneratedByLabel, consts.SriovNetworkOperatorIdentifier))
			g.Expect(dc.Labels).To(HaveKeyWithValue(consts.DRADeviceClassResourceNameLabel, draResourceName))
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})

	It("should publish the initial VFs on ResourceSlices for the selected node", func(ctx SpecContext) {
		By("Waiting for ResourceSlices to publish the initial VFs on " + node)
		Eventually(ctx, func(g Gomega) {
			count, err := countAdvertisedDRADevices(ctx, node, extendedResource)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(count).To(Equal(initialVFs))
		}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())

		By("Checking other nodes do not advertise this resource")
		for _, p := range listDRADriverPods(ctx) {
			if p.Spec.NodeName == "" || p.Spec.NodeName == node {
				continue
			}
			count, err := countAdvertisedDRADevices(ctx, p.Spec.NodeName, extendedResource)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(0),
				"node %s advertised %s before a policy selected it", p.Spec.NodeName, extendedResource)
		}
	})

	It("should publish the additional VFs on ResourceSlices when policy interfaces change", func(ctx SpecContext) {
		otherCounts := map[string]int{}
		for _, p := range listDRADriverPods(ctx) {
			if p.Spec.NodeName != "" && p.Spec.NodeName != node {
				count, err := countAdvertisedDRADevices(ctx, p.Spec.NodeName, extendedResource)
				Expect(err).ToNot(HaveOccurred())
				otherCounts[p.Spec.NodeName] = count
			}
		}

		By("Changing NumVfs so SriovNetworkNodeState interfaces are updated")
		Eventually(ctx, func(g Gomega) {
			policy := &sriovv1.SriovNetworkNodePolicy{}
			err := clients.Get(ctx, runtimeclient.ObjectKey{
				Namespace: operatorNamespace,
				Name:      policyName,
			}, policy)
			g.Expect(err).ToNot(HaveOccurred())
			policy.Spec.NumVfs = updatedVFs
			g.Expect(clients.Update(ctx, policy)).To(Succeed())
		}).WithTimeout(1 * time.Minute).WithPolling(2 * time.Second).Should(Succeed())
		WaitForSRIOVStable()

		By(fmt.Sprintf("Waiting for ResourceSlice devices on %s to increase from %d to %d", node, initialVFs, updatedVFs))
		Eventually(ctx, func(g Gomega) {
			count, err := countAdvertisedDRADevices(ctx, node, extendedResource)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(count).To(Equal(updatedVFs))
		}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())

		By("Checking ResourceSlice device counts on other nodes did not change")
		for nodeName, before := range otherCounts {
			count, err := countAdvertisedDRADevices(ctx, nodeName, extendedResource)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(before),
				"ResourceSlice devices changed on %s", nodeName)
		}
	})

	It("should expose VFIO device nodes to an unprivileged extended-resource consumer", func(ctx SpecContext) {
		testDRAVFIOExtendedResource(ctx, policyName)
	})

	It("should drain a ResourceClaim consumer when its SriovNetworkNodePolicy is deleted", func(ctx SpecContext) {
		disableDrain, err := cluster.GetNodeDrainState(clients, operatorNamespace)
		Expect(err).NotTo(HaveOccurred())
		if disableDrain {
			Skip("Drain is disabled in SriovOperatorConfig")
		}

		// The preceding VFIO spec may have selected a different node and device.
		// Use the current policy so this also works when that spec was skipped.
		policy := &sriovv1.SriovNetworkNodePolicy{}
		Expect(clients.Get(ctx, runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: policyName}, policy)).To(Succeed())

		By("Creating an explicit ResourceClaim for the policy's DeviceClass")
		claim := &resourceapi.ResourceClaim{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "dra-drain-", Namespace: namespaces.Test},
			Spec: resourceapi.ResourceClaimSpec{
				Devices: resourceapi.DeviceClaim{
					Requests: []resourceapi.DeviceRequest{{
						Name: "vf",
						Exactly: &resourceapi.ExactDeviceRequest{
							DeviceClassName: deviceClassName,
							AllocationMode:  resourceapi.DeviceAllocationModeExactCount,
							Count:           1,
						},
					}},
				},
			},
		}
		Expect(clients.Create(ctx, claim)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			Expect(runtimeclient.IgnoreNotFound(clients.Delete(ctx, claim))).To(Succeed())
			Eventually(ctx, func() bool {
				return errors.IsNotFound(clients.Get(ctx, runtimeclient.ObjectKeyFromObject(claim), &resourceapi.ResourceClaim{}))
			}).WithTimeout(waitingTime).WithPolling(time.Second).Should(BeTrue())
		})

		By("Starting a pod that consumes the claim without extended-resource requests")
		consumer := pod.GetDefinition()
		consumer.Spec.NodeSelector = policy.Spec.NodeSelector
		consumer.Spec.ResourceClaims = []corev1.PodResourceClaim{{
			Name: "vf", ResourceClaimName: ptr.To(claim.Name),
		}}
		consumer.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "vf"}}
		Expect(clients.Create(ctx, consumer)).To(Succeed())
		// Cleanup runs after the eviction assertion, and also handles a failed drain.
		// Register it after claim cleanup so the pod is removed first.
		DeferCleanup(func(ctx SpecContext) {
			Expect(runtimeclient.IgnoreNotFound(clients.Delete(ctx, consumer))).To(Succeed())
			Eventually(ctx, func() bool {
				return errors.IsNotFound(clients.Get(ctx, runtimeclient.ObjectKeyFromObject(consumer), &corev1.Pod{}))
			}).WithTimeout(waitingTime).WithPolling(time.Second).Should(BeTrue())
		})
		Eventually(ctx, func(g Gomega) {
			g.Expect(clients.Get(ctx, runtimeclient.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
			g.Expect(pod.AssertContainerReady(*consumer, consumer.Spec.Containers[0].Name)).To(Succeed())
			g.Expect(consumer.Spec.NodeName).NotTo(BeEmpty())
			g.Expect(clients.Get(ctx, runtimeclient.ObjectKeyFromObject(claim), claim)).To(Succeed())
			g.Expect(claim.Status.Allocation).NotTo(BeNil())
			g.Expect(claim.Status.Allocation.Devices.Results).To(HaveLen(1))
			g.Expect(claim.Status.Allocation.Devices.Results[0].Driver).To(Equal(consts.DRADriverBaseDeviceClassName))
			g.Expect(claim.Status.ReservedFor).To(ContainElement(HaveField("UID", consumer.UID)))
		}).WithTimeout(waitingTime).WithPolling(time.Second).Should(Succeed())
		for _, container := range consumer.Spec.Containers {
			Expect(container.Resources.Requests).NotTo(HaveKey(corev1.ResourceName(extendedResource)))
			Expect(container.Resources.Limits).NotTo(HaveKey(corev1.ResourceName(extendedResource)))
		}

		By("Deleting the policy while the claim-backed pod is running")
		Expect(clients.Delete(ctx, policy)).To(Succeed())

		By("Waiting for the operator to drain the consumer before test cleanup")
		Eventually(ctx, func(g Gomega) {
			err := clients.Get(ctx, runtimeclient.ObjectKeyFromObject(consumer), &corev1.Pod{})
			g.Expect(errors.IsNotFound(err)).To(BeTrue(),
				"pod %s/%s on node %s was not drained after policy deletion (get error: %v)",
				consumer.Namespace, consumer.Name, consumer.Spec.NodeName, err)
		}).WithTimeout(waitingTime).WithPolling(time.Second).Should(Succeed())

		By("Waiting for node reconfiguration and withdrawal of the deleted policy's devices")
		WaitForSRIOVStable()
		Eventually(ctx, func(g Gomega) {
			count, err := countAdvertisedDRADevices(ctx, consumer.Spec.NodeName, extendedResource)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(count).To(BeZero())
		}).WithTimeout(waitingTime).WithPolling(time.Second).Should(Succeed())
	})

	It("should clean up DRA objects and restore the device plugin when DRA is disabled", func(ctx SpecContext) {
		By("Removing the DRA policy and waiting for node reconfiguration before switching providers")
		policy := &sriovv1.SriovNetworkNodePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: operatorNamespace},
		}
		Expect(runtimeclient.IgnoreNotFound(clients.Delete(ctx, policy))).To(Succeed())
		WaitForSRIOVStable()

		By("Disabling dynamicResourceAllocation feature flag")
		setFeatureFlag(consts.DynamicResourceAllocationFeatureGate, false)
		WaitForSRIOVStable()

		By("Waiting for sriov-dra-driver DaemonSet to be removed")
		assertObjectIsNotFound(consts.DRADriverDaemonSetName, &appsv1.DaemonSet{})

		By("Waiting for operator-generated DeviceAttributes to be removed")
		Eventually(ctx, func(g Gomega) {
			list := &sriovdrav1alpha1.DeviceAttributesList{}
			err := clients.List(ctx, list,
				runtimeclient.InNamespace(operatorNamespace),
				runtimeclient.MatchingLabels(consts.DRAOperatorLabels))
			if meta.IsNoMatchError(err) {
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(list.Items).To(BeEmpty())
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for operator-generated SriovResourcePolicy to be removed")
		Eventually(ctx, func(g Gomega) {
			list := &sriovdrav1alpha1.SriovResourcePolicyList{}
			err := clients.List(ctx, list,
				runtimeclient.InNamespace(operatorNamespace),
				runtimeclient.MatchingLabels(consts.DRAOperatorLabels))
			if meta.IsNoMatchError(err) {
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(list.Items).To(BeEmpty())
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for operator-generated DeviceClasses to be removed")
		Eventually(ctx, func(g Gomega) {
			list := &resourceapi.DeviceClassList{}
			err := clients.List(ctx, list,
				runtimeclient.MatchingLabels(consts.DRAOperatorLabels))
			if meta.IsNoMatchError(err) {
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(list.Items).To(BeEmpty())
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for base DeviceClass to be removed")
		Eventually(ctx, func(g Gomega) {
			dc := &resourceapi.DeviceClass{}
			err := clients.Get(ctx, runtimeclient.ObjectKey{
				Name: consts.DRADriverBaseDeviceClassName,
			}, dc)
			g.Expect(errors.IsNotFound(err) || meta.IsNoMatchError(err)).To(BeTrue())
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		waitForDRAProviderSwitch(ctx, false)

		By("Recreating the policy in device-plugin mode")
		_, err := network.CreateSriovPolicy(clients, "test-device-plugin-policy-", operatorNamespace, nic.Name, node, initialVFs, draResourceName, "netdevice")
		Expect(err).ToNot(HaveOccurred())
		WaitForSRIOVStable()

		By("Waiting for sriov-device-plugin DaemonSet to be restored")
		Eventually(ctx, func(g Gomega) {
			ds := &appsv1.DaemonSet{}
			err := clients.Get(ctx, runtimeclient.ObjectKey{
				Namespace: operatorNamespace,
				Name:      devicePluginDSName,
			}, ds)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ds.Status.NumberReady).To(Equal(ds.Status.DesiredNumberScheduled))
			g.Expect(ds.Status.DesiredNumberScheduled).To(BeNumerically(">", 0))
		}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for at least one Running device-plugin pod")
		Eventually(ctx, func(g Gomega) {
			pods, err := clients.Pods(operatorNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: devicePluginAppLabel,
			})
			g.Expect(err).ToNot(HaveOccurred())
			running := 0
			for _, p := range pods.Items {
				if p.Status.Phase == corev1.PodRunning {
					running++
				}
			}
			g.Expect(running).To(BeNumerically(">", 0))
		}).WithTimeout(waitingTime).WithPolling(5 * time.Second).Should(Succeed())
	})
})
