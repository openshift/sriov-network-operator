package e2e

import (
	goctx "context"
	"strings"
	"testing"
	"time"

	"github.com/pliurh/sriov-network-operator/pkg/apis"
	netattdefv1 "github.com/pliurh/sriov-network-operator/pkg/apis/k8s/v1"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	appsv1 "k8s.io/api/apps/v1"
	// dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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
	t.Run("Operator", func(t *testing.T) {
		t.Run("Create-SriovDaemonset", func(t *testing.T) {
			CreateSriovNetworkConfigDaemonset(t, ctx)
		})
		t.Run("With-SriovNetworkCR-Create-NetAttDefCR", func(t *testing.T) {
			WithSriovNetworkCRCreateNetAttDefCR(t, ctx)
		})
	})
}

func CreateSriovNetworkConfigDaemonset(t *testing.T, ctx *framework.TestCtx) {
	t.Parallel()
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("failed to get namesapces: %v", err)
	}
	f := framework.Global
	_, err = waitForDaemonset(t, f.Client, namespace, "sriov-network-config-daemon", retryInterval, timeout)
	if err != nil {
		t.Fatalf("failed to get daemonset: %v", err)
	}
}

func WithSriovNetworkCRCreateNetAttDefCR(t *testing.T, ctx *framework.TestCtx) {
	t.Parallel()
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("failed to get namesapces: %v", err)
	}
	// create memcached custom resource
	exampleCR := &sriovnetworkv1.SriovNetwork{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SriovNetwork",
			APIVersion: "sriovnetwork.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example-sriovnetwork",
			Namespace: namespace,
		},
		Spec: sriovnetworkv1.SriovNetworkSpec{
			ResourceName: "resource-1",
			IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			Vlan:         100,
		},
	}

	expect := `{"cniVersion":"0.3.1","name":"sriov-net","type":"sriov","vlan":100,"ipam":{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}}`

	// get global framework variables
	f := framework.Global
	err = f.Client.Create(goctx.TODO(), exampleCR, &framework.CleanupOptions{TestContext: ctx, Timeout: apiTimeout, RetryInterval: retryInterval})
	if err != nil {
		t.Fatalf("fail to create SriovNetwork CR: %v", err)
	}
	netAttDefCR, err := WaitForNetworkAttachmentDefinition(t, f.Client, exampleCR.GetName(), exampleCR.GetNamespace(), retryInterval, timeout)
	if err != nil {
		t.Fatalf("fail to get NetworkAttachmentDefinition: %v", err)
	}
	anno := netAttDefCR.GetAnnotations()

	if anno["k8s.v1.cni.cncf.io/resourceName"] != exampleCR.Spec.ResourceName {
		t.Fatal("CNI resourcName not match")
	}

	if strings.TrimSpace(netAttDefCR.Spec.Config) != expect {
		t.Fatal("CNI config not match")
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

func waitForDaemonset(t *testing.T, client framework.FrameworkClient, namespace, name string, retryInterval, timeout time.Duration) (*appsv1.DaemonSet, error) {
	found := &appsv1.DaemonSet{}

	err := wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		ctx, cancel := goctx.WithTimeout(goctx.Background(), apiTimeout)
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	if err != nil {
		t.Logf("failed to wait for Daemonset %s/%s to exist: %v", namespace, name, err)
		return nil, err
	}

	return found, nil
}
