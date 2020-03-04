package e2e

import (
	goctx "context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/sriov-network-operator/pkg/apis"
	netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

var (
	retryInterval        = time.Second * 5
	apiTimeout           = time.Second * 10
	timeout              = time.Second * 60
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

func TestOperatorController(t *testing.T) {
	snetList := &sriovnetworkv1.SriovNetworkList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SriovNetwork",
			APIVersion: sriovnetworkv1.SchemeGroupVersion.String(),
		},
	}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, snetList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}

	ctx := framework.NewTestCtx(t)
	defer ctx.Cleanup()
	err = ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		t.Fatalf("failed to initialize cluster resources: %v", err)
	}
	t.Log("Initialized cluster resources")
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatal(err)
	}
	// get global framework variables
	f := framework.Global
	// wait for sriov-network-operator to be ready
	err = e2eutil.WaitForOperatorDeployment(t, f.KubeClient, namespace, "sriov-network-operator", 1, retryInterval, timeout)
	if err != nil {
		t.Fatal(err)
	}

	// run subtests
	t.Run("Operator initialization", func(t *testing.T) {
		t.Run("Test-Sriov-Network-Config-Daemonset-Created", func(t *testing.T) {
			testSriovNetworkConfigDaemonsetCreated(t, ctx)
		})

	})

	t.Run("Operator handle SriovNetwork CR creation", func(t *testing.T) {
		testCases := generateSriovNetworkCRs(namespace)
		for _, cr := range testCases {
			t.Run("Test-With-SriovNetworkCR", func(t *testing.T) {
				testWithSriovNetworkCRCreation(t, ctx, cr)
			})
		}
	})

	t.Run("Operator handle SriovNetwork CR update", func(t *testing.T) {
		testCases := generateSriovNetworkCRs(namespace)
		for _, cr := range testCases {
			t.Run("Test-With-SriovNetworkCR", func(t *testing.T) {
				testWithSriovNetworkCRUpdate(t, ctx, cr)
			})
		}
	})

	t.Run("Operator handle SriovNetwork CR deletion", func(t *testing.T) {
		testCases := generateSriovNetworkCRs(namespace)
		for _, cr := range testCases {
			t.Run("Test-With-SriovNetworkCR", func(t *testing.T) {
				testWithSriovNetworkCRDeletion(t, ctx, cr)
			})
		}
	})

	t.Run("Operator handle SriovNetworkNodePolicy CR Creation", func(t *testing.T) {
		t.Run("Test-With-One-SriovNetworkNodePolicyCR", func(t *testing.T) {
			testWithOneSriovNetworkNodePolicyCR(t, ctx)
		})
	})
}

func generateSriovNetworkCRs(namespace string) []*sriovnetworkv1.SriovNetwork {
	specs := map[string]sriovnetworkv1.SriovNetworkSpec{
		"test-0": {
			ResourceName: "resource_1",
			IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			Vlan:         100,
		},
		"test-1": {
			ResourceName:     "resource_1",
			IPAM:             `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			NetworkNamespace: "default",
		},
		"test-2": {
			ResourceName: "resource_1",
			IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			SpoofChk:     "on",
		},
		"test-3": {
			ResourceName: "resource_1",
			IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			Trust:        "on",
		},
	}
	var crs []*sriovnetworkv1.SriovNetwork

	for k, v := range specs {
		crs = append(crs, &sriovnetworkv1.SriovNetwork{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetwork",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      k,
				Namespace: namespace,
			},
			Spec: v,
		})
	}
	return crs
}

func testWithOneSriovNetworkNodePolicyCR(t *testing.T, ctx *framework.TestCtx) {
	t.Parallel()
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("failed to get namesapces: %v", err)
	}

	// create custom resource
	policy := &sriovnetworkv1.SriovNetworkNodePolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SriovNetworkNodePolicy",
			APIVersion: "sriovnetwork.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-1",
			Namespace: namespace,
		},
		Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			ResourceName: "resource_1",
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			Priority: 99,
			Mtu:      9000,
			NumVfs:   6,
			NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
				Vendor:      "8086",
				RootDevices: []string{"0000:86:00.1"},
			},
			DeviceType: "vfio-pci",
		},
	}
	// get global framework variables
	f := framework.Global
	err = f.Client.Create(goctx.TODO(), policy, &framework.CleanupOptions{TestContext: ctx, Timeout: apiTimeout, RetryInterval: retryInterval})
	if err != nil {
		t.Fatalf("fail to create SriovNetworkNodePolicy CR: %v", err)
	}

	cm := &corev1.ConfigMap{}
	for i := 0; i < 3; i++ {
		err = waitForNamespacedObject(cm, t, f.Client, namespace, "device-plugin-config", retryInterval, timeout)
		if err != nil {
			t.Fatalf("fail to get ConfigMap: %v", err)
		}

		if err = validateDevicePluginConfig(policy, cm.Data["config.json"]); err != nil && i == 2 {
			t.Fatalf("failed to validate ConfigMap : %v", err)
		} else if err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	daemon := &appsv1.DaemonSet{}
	err = waitForNamespacedObject(daemon, t, f.Client, namespace, "sriov-device-plugin", retryInterval, timeout)
	if err != nil {
		t.Fatalf("fail to get DaemonSet sriov-device-plugin: %v", err)
	}

	daemon = &appsv1.DaemonSet{}
	err = waitForNamespacedObject(daemon, t, f.Client, namespace, "sriov-cni", retryInterval, timeout)
	if err != nil {
		t.Fatalf("fail to get DaemonSet sriov-cni: %v", err)
	}
}

func testSriovNetworkConfigDaemonsetCreated(t *testing.T, ctx *framework.TestCtx) {
	t.Parallel()
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("failed to get namespaces: %v", err)
	}
	f := framework.Global
	daemon := &appsv1.DaemonSet{}
	err = waitForNamespacedObject(daemon, t, f.Client, namespace, "sriov-network-config-daemon", retryInterval, timeout)
	if err != nil {
		t.Fatalf("failed to get daemonset: %v", err)
	}
}

func testWithSriovNetworkCRDeletion(t *testing.T, ctx *framework.TestCtx, cr *sriovnetworkv1.SriovNetwork) {
	t.Parallel()

	var err error
	// get global framework variables
	f := framework.Global
	found := &sriovnetworkv1.SriovNetwork{}
	err = f.Client.Get(goctx.TODO(), types.NamespacedName{Namespace: cr.GetNamespace(), Name: cr.GetName()}, found)
	if err != nil {
		t.Fatalf("fail to Get SriovNetwork CR: %v", err)
	}
	err = f.Client.Delete(goctx.TODO(), found, []dynclient.DeleteOption{}...)
	if err != nil {
		t.Fatalf("fail to Delete SriovNetwork CR: %v", err)
	}
	// wait 600ms for object get deleted
	time.Sleep(600 * time.Millisecond)
	nad := &netattdefv1.NetworkAttachmentDefinition{}
	namespace := cr.GetNamespace()
	if cr.Spec.NetworkNamespace != "" {
		namespace = cr.Spec.NetworkNamespace
	}
	err = f.Client.Get(goctx.TODO(), types.NamespacedName{Namespace: namespace, Name: cr.GetName()}, nad)
	if err != nil && errors.IsNotFound(err) {
		return
	}
	t.Fatalf("fail to Delete NetworkAttachmentDefinition CR: %s/%s: %v", namespace, cr.Name, err)
}

func testWithSriovNetworkCRUpdate(t *testing.T, ctx *framework.TestCtx, cr *sriovnetworkv1.SriovNetwork) {
	t.Parallel()
	cr0 := &sriovnetworkv1.SriovNetwork{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SriovNetwork",
			APIVersion: "sriovnetwork.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
		},
		Spec: sriovnetworkv1.SriovNetworkSpec{
			ResourceName: "resource_1",
			IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","gateway":"10.56.217.1"}`,
		},
	}

	var err error
	expect := fmt.Sprintf(`{ "cniVersion":"0.3.1", "name":"%s", "type":"sriov", "vlan":0,"vlanQoS":0,"ipam":%s }`, cr0.Name, cr0.Spec.IPAM)

	// get global framework variables
	f := framework.Global
	found := &sriovnetworkv1.SriovNetwork{}
	err = f.Client.Get(goctx.TODO(), types.NamespacedName{Namespace: cr.GetNamespace(), Name: cr.GetName()}, found)
	if err != nil {
		t.Fatalf("fail to get SriovNetwork CR: %v", err)
	}
	found.Spec = cr0.Spec
	err = f.Client.Update(goctx.TODO(), found)
	if err != nil {
		t.Fatalf("fail to update SriovNetwork CR: %v", err)
	}
	// wait 600ms for object get update
	time.Sleep(600 * time.Millisecond)
	netAttDefCR, err := WaitForNetworkAttachmentDefinition(t, f.Client, cr.GetName(), cr.GetNamespace(), retryInterval, timeout)
	if err != nil {
		t.Fatalf("fail to get NetworkAttachmentDefinition after update: %v", err)
	}
	anno := netAttDefCR.GetAnnotations()

	if anno["k8s.v1.cni.cncf.io/resourceName"] != "openshift.io/"+cr.Spec.ResourceName {
		t.Fatalf("CNI resourceName not match: %v", anno["k8s.v1.cni.cncf.io/resourceName"])
	}

	if strings.TrimSpace(netAttDefCR.Spec.Config) != expect {
		t.Fatalf("CNI config not match: %v\nexpect: %v", strings.TrimSpace(netAttDefCR.Spec.Config), expect)
	}
}

func testWithSriovNetworkCRCreation(t *testing.T, ctx *framework.TestCtx, cr *sriovnetworkv1.SriovNetwork) {
	t.Parallel()
	var err error
	spoofchk := ""
	trust := ""
	state := ""

	if cr.Spec.Trust == "on" {
		trust = `"trust":"on",`
	} else if cr.Spec.Trust == "off" {
		trust = `"trust":"off",`
	}

	if cr.Spec.SpoofChk == "on" {
		spoofchk = `"spoofchk":"on",`
	} else if cr.Spec.SpoofChk == "off" {
		spoofchk = `"spoofchk":"off",`
	}

	if cr.Spec.LinkState == "auto" {
		state = `"link_state":"auto",`
	} else if cr.Spec.LinkState == "enable" {
		state = `"link_state":"enable",`
	} else if cr.Spec.LinkState == "disable" {
		state = `"link_state":"disable",`
	}

	vlanQoS := cr.Spec.VlanQoS

	expect := fmt.Sprintf(`{ "cniVersion":"0.3.1", "name":"%s", "type":"sriov", "vlan":%d,%s%s%s"vlanQoS":%d,"ipam":%s }`, cr.Name, cr.Spec.Vlan, spoofchk, trust, state, vlanQoS, cr.Spec.IPAM)

	// get global framework variables
	f := framework.Global
	err = f.Client.Create(goctx.TODO(), cr, &framework.CleanupOptions{TestContext: ctx, Timeout: apiTimeout, RetryInterval: retryInterval})
	if err != nil {
		t.Fatalf("fail to create SriovNetwork CR: %v", err)
	}
	namespace := cr.GetNamespace()
	if cr.Spec.NetworkNamespace != "" {
		namespace = cr.Spec.NetworkNamespace
	}
	netAttDefCR, err := WaitForNetworkAttachmentDefinition(t, f.Client, cr.GetName(), namespace, retryInterval, timeout)
	if err != nil {
		t.Fatalf("fail to get NetworkAttachmentDefinition: %v", err)
	}
	anno := netAttDefCR.GetAnnotations()

	if anno["k8s.v1.cni.cncf.io/resourceName"] != "openshift.io/"+cr.Spec.ResourceName {
		t.Fatalf("CNI resourceName not match: %v", anno["k8s.v1.cni.cncf.io/resourceName"])
	}

	if strings.TrimSpace(netAttDefCR.Spec.Config) != expect {
		t.Fatalf("CNI config not match: %v: %v", strings.TrimSpace(netAttDefCR.Spec.Config), expect)
	}

}

// WaitForNetworkAttachmentDefinition wait for customer resource to be created
func WaitForNetworkAttachmentDefinition(t *testing.T, client framework.FrameworkClient, name string, namespace string, retryInterval, timeout time.Duration) (*netattdefv1.NetworkAttachmentDefinition, error) {
	cr := &netattdefv1.NetworkAttachmentDefinition{}

	err := wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		ctx, cancel := goctx.WithTimeout(goctx.Background(), apiTimeout)
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cr)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	if err != nil {
		t.Logf("failed to wait for NetworkAttachmentDefinition CR %s/%s to exist: %v", namespace, name, err)
		return nil, err
	}

	return cr, nil
}

func waitForNamespacedObject(obj runtime.Object, t *testing.T, client framework.FrameworkClient, namespace, name string, retryInterval, timeout time.Duration) error {

	err := wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		ctx, cancel := goctx.WithTimeout(goctx.Background(), apiTimeout)
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	if err != nil {
		t.Logf("failed to wait for obj %s/%s to exist: %v", namespace, name, err)
		return err
	}

	return nil
}

func validateDevicePluginConfig(np *sriovnetworkv1.SriovNetworkNodePolicy, rawConfig string) error {
	rcl := dptypes.ResourceConfList{}

	if err := json.Unmarshal([]byte(rawConfig), &rcl); err != nil {
		return err
	}

	if len(rcl.ResourceList) != 1 {
		return fmt.Errorf("number of resources in config is incorrect: %d", len(rcl.ResourceList))
	}

	rc := rcl.ResourceList[0]
	if rc.IsRdma != np.Spec.IsRdma || rc.ResourceName != np.Spec.ResourceName || !validateSelector(&rc, &np.Spec.NicSelector) {
		return fmt.Errorf("content of config is incorrect")
	}

	return nil
}

func validateSelector(rc *dptypes.ResourceConfig, ns *sriovnetworkv1.SriovNetworkNicSelector) bool {
	if ns.DeviceID != "" {
		if len(rc.Selectors.Devices) != 1 || ns.DeviceID != rc.Selectors.Devices[0] {
			return false
		}
	}
	if ns.Vendor != "" {
		if len(rc.Selectors.Vendors) != 1 || ns.Vendor != rc.Selectors.Vendors[0] {
			return false
		}
	}
	if len(ns.PfNames) > 0 {
		if !reflect.DeepEqual(ns.PfNames, rc.Selectors.PfNames) {
			return false
		}
	}
	return true
}
