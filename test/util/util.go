package util

import (
	goctx "context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	dptypes "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/types"

	// "github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	appsv1 "k8s.io/api/apps/v1"
	// corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	// "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apis"
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var (
	RetryInterval        = time.Second * 1
	APITimeout           = time.Second * 20
	Timeout              = time.Second * 60
	CleanupRetryInterval = time.Second * 1
	CleanupTimeout       = time.Second * 5
)

func WaitForSriovNetworkNodeStateReady(nodeState *sriovnetworkv1.SriovNetworkNodeState, client client.Client, namespace, name string, retryInterval, timeout time.Duration) error {
	time.Sleep(30 * time.Second)
	err := wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		ctx, cancel := goctx.WithTimeout(goctx.Background(), APITimeout)
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, nodeState)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if nodeState.Status.SyncStatus != "Succeeded" {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		fmt.Printf("failed to wait for ds %s/%s to be ready: %v", namespace, name, err)
		return err
	}

	return nil
}

func WaitForDaemonSetReady(ds *appsv1.DaemonSet, client client.Client, namespace, name string, retryInterval, timeout time.Duration) error {
	err := wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		ctx, cancel := goctx.WithTimeout(goctx.Background(), APITimeout)
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ds)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if ds.Status.DesiredNumberScheduled == ds.Status.NumberReady {
			return true, nil
		} else {
			return false, nil
		}
	})
	if err != nil {
		fmt.Printf("failed to wait for ds %s/%s to be ready: %v", namespace, name, err)
		return err
	}

	return nil
}

func WaitForNamespacedObject(obj client.Object, client client.Client, namespace, name string, retryInterval, timeout time.Duration) error {
	err := wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		ctx, cancel := goctx.WithTimeout(goctx.Background(), APITimeout)
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
		fmt.Printf("failed to wait for obj %s/%s to exist: %v", namespace, name, err)
		return err
	}

	return nil
}

func WaitForNamespacedObjectDeleted(obj client.Object, client client.Client, namespace, name string, retryInterval, timeout time.Duration) error {
	err := wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		ctx, cancel := goctx.WithTimeout(goctx.Background(), APITimeout)
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	if err != nil {
		fmt.Printf("failed to wait for obj %s/%s to not exist: %v", namespace, name, err)
		return err
	}

	return nil
}

func GenerateSriovNetworkCRs(namespace string, specs map[string]sriovnetworkv1.SriovNetworkSpec) map[string]sriovnetworkv1.SriovNetwork {
	crs := make(map[string]sriovnetworkv1.SriovNetwork)

	for k, v := range specs {
		crs[k] = sriovnetworkv1.SriovNetwork{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetwork",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      k,
				Namespace: namespace,
			},
			Spec: v,
		}
	}
	return crs
}

func GenerateSriovIBNetworkCRs(namespace string, specs map[string]sriovnetworkv1.SriovIBNetworkSpec) map[string]sriovnetworkv1.SriovIBNetwork {
	crs := make(map[string]sriovnetworkv1.SriovIBNetwork)

	for k, v := range specs {
		crs[k] = sriovnetworkv1.SriovIBNetwork{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovIBNetwork",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      k,
				Namespace: namespace,
			},
			Spec: v,
		}
	}
	return crs
}

func ValidateDevicePluginConfig(nps []*sriovnetworkv1.SriovNetworkNodePolicy, rawConfig string) error {
	rcl := dptypes.ResourceConfList{}

	if err := json.Unmarshal([]byte(rawConfig), &rcl); err != nil {
		return err
	}

	if len(rcl.ResourceList) != 1 {
		return fmt.Errorf("number of resources in config is incorrect: %d", len(rcl.ResourceList))
	}

	for _, rc := range rcl.ResourceList {
		for _, np := range nps {
			if rc.ResourceName != np.Spec.ResourceName {
				continue
			}

			netDeviceSelectors := &dptypes.NetDeviceSelectors{}
			raw, err := rc.Selectors.MarshalJSON()
			if err != nil {
				return err
			}

			err = json.Unmarshal(raw, netDeviceSelectors)
			if err != nil {
				return err
			}

			if netDeviceSelectors.IsRdma != np.Spec.IsRdma || rc.ResourceName != np.Spec.ResourceName || !validateSelector(netDeviceSelectors, &np.Spec.NicSelector) {
				return fmt.Errorf("content of config is incorrect")
			}
		}
	}
	return nil
}

// TriggerSriovOperatorConfigReconcile edits a test label of the default SriovOperatorConfig object to
// trigger the reconciliation logic of the controller.
func TriggerSriovOperatorConfigReconcile(client client.Client, operatorNamespace string) error {
	config := &sriovnetworkv1.SriovOperatorConfig{}
	err := client.Get(goctx.Background(), types.NamespacedName{Name: "default", Namespace: operatorNamespace}, config)
	if err != nil {
		return err
	}

	if config.ObjectMeta.Labels == nil {
		config.ObjectMeta.Labels = make(map[string]string)
	}

	config.ObjectMeta.Labels["trigger-test"] = uuid.NewString()
	return client.Update(goctx.Background(), config)
}

func validateSelector(rc *dptypes.NetDeviceSelectors, ns *sriovnetworkv1.SriovNetworkNicSelector) bool {
	if ns.DeviceID != "" {
		if len(rc.Devices) != 1 || ns.DeviceID != rc.Devices[0] {
			return false
		}
	}
	if ns.Vendor != "" {
		if len(rc.Vendors) != 1 || ns.Vendor != rc.Vendors[0] {
			return false
		}
	}
	if len(ns.PfNames) > 0 {
		if !equality.Semantic.DeepEqual(ns.PfNames, rc.PfNames) {
			return false
		}
	}
	return true
}
