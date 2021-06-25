/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/openshift/machine-config-operator/lib/resourcemerge"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	apply "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apply"
	render "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

// SriovOperatorConfigReconciler reconciles a SriovOperatorConfig object
type SriovOperatorConfigReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

var injectorServiceCaCmVersion = ""
var webhookServiceCaCmVersion = ""

// +kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovoperatorconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovoperatorconfigs/status,verbs=get;update;patch

func (r *SriovOperatorConfigReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	logger := r.Log.WithValues("sriovoperatorconfig", req.NamespacedName)

	logger.Info("Reconciling SriovOperatorConfig")

	enableAdmissionController := os.Getenv("ENABLE_ADMISSION_CONTROLLER") == "true"
	if !enableAdmissionController {
		logger.Info("SR-IOV Network Resource Injector and Operator Webhook are disabled.")
	}
	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err := r.Get(context.TODO(), types.NamespacedName{
		Name: DEFAULT_CONFIG_NAME, Namespace: namespace}, defaultConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			// Default Config object not found, create it.
			defaultConfig.SetNamespace(namespace)
			defaultConfig.SetName(DEFAULT_CONFIG_NAME)
			defaultConfig.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
				EnableInjector:           func() *bool { b := enableAdmissionController; return &b }(),
				EnableOperatorWebhook:    func() *bool { b := enableAdmissionController; return &b }(),
				ConfigDaemonNodeSelector: map[string]string{},
				LogLevel:                 2,
			}
			err = r.Create(context.TODO(), defaultConfig)
			if err != nil {
				logger.Error(err, "Failed to create default Operator Config", "Namespace",
					namespace, "Name", DEFAULT_CONFIG_NAME)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if req.Namespace != namespace {
		return reconcile.Result{}, nil
	}

	// Render and sync webhook objects
	if err = r.syncWebhookObjs(defaultConfig); err != nil {
		return reconcile.Result{}, err
	}

	// Sync SriovNetworkConfigDaemon objects
	if err = r.syncConfigDaemonSet(defaultConfig); err != nil {
		return reconcile.Result{}, err
	}

	if err = r.syncPluginDaemonSet(defaultConfig); err != nil {
		return reconcile.Result{}, err
	}

	if utils.ClusterType == utils.ClusterTypeOpenshift {
		if err = r.syncOffloadMachineConfig(defaultConfig); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}

func (r *SriovOperatorConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sriovnetworkv1.SriovOperatorConfig{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func (r *SriovOperatorConfigReconciler) syncPluginDaemonSet(dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := r.Log.WithName("syncConfigDaemonset")
	logger.Info("Start to sync SRIOV plugin daemonsets nodeSelector")
	ds := &appsv1.DaemonSet{}

	names := []string{"sriov-cni", "sriov-device-plugin"}

	if len(dc.Spec.ConfigDaemonNodeSelector) == 0 {
		return nil
	}
	for _, name := range names {
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, ds)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			logger.Error(err, "Couldn't get daemonset", "name", name)
			return err
		}
		ds.Spec.Template.Spec.NodeSelector = dc.Spec.ConfigDaemonNodeSelector
		err = r.Client.Update(context.TODO(), ds)
		if err != nil {
			logger.Error(err, "Couldn't update daemonset", "name", name)
			return err
		}
	}

	return nil
}

func (r *SriovOperatorConfigReconciler) syncConfigDaemonSet(dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := r.Log.WithName("syncConfigDaemonset")
	logger.Info("Start to sync config daemonset")
	// var err error
	objs := []*uns.Unstructured{}

	data := render.MakeRenderData()
	data.Data["Image"] = os.Getenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE")
	data.Data["Namespace"] = namespace
	data.Data["SRIOVCNIImage"] = os.Getenv("SRIOV_CNI_IMAGE")
	data.Data["SRIOVInfiniBandCNIImage"] = os.Getenv("SRIOV_INFINIBAND_CNI_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ClusterType"] = utils.ClusterType
	envCniBinPath := os.Getenv("SRIOV_CNI_BIN_PATH")
	if envCniBinPath == "" {
		data.Data["CNIBinPath"] = "/var/lib/cni/bin"
	} else {
		logger.Info("New cni bin found", "CNIBinPath", envCniBinPath)
		data.Data["CNIBinPath"] = envCniBinPath
	}
	objs, err := render.RenderDir(CONFIG_DAEMON_PATH, &data)
	if err != nil {
		logger.Error(err, "Fail to render config daemon manifests")
		return err
	}
	// Sync DaemonSets
	for _, obj := range objs {
		if obj.GetKind() == "DaemonSet" && len(dc.Spec.ConfigDaemonNodeSelector) > 0 {
			scheme := kscheme.Scheme
			ds := &appsv1.DaemonSet{}
			err = scheme.Convert(obj, ds, nil)
			if err != nil {
				logger.Error(err, "Fail to convert to DaemonSet")
				return err
			}
			ds.Spec.Template.Spec.NodeSelector = dc.Spec.ConfigDaemonNodeSelector
			err = scheme.Convert(ds, obj, nil)
			if err != nil {
				logger.Error(err, "Fail to convert to Unstructured")
				return err
			}
		}
		err = r.syncK8sResource(dc, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}
	return nil
}

func (r *SriovOperatorConfigReconciler) syncWebhookObjs(dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := r.Log.WithName("syncWebhookObjs")
	logger.Info("Start to sync webhook objects")

	for name, path := range webhooks {
		// Render Webhook manifests
		data := render.MakeRenderData()
		data.Data["Namespace"] = namespace
		data.Data["SRIOVMutatingWebhookName"] = name
		data.Data["NetworkResourcesInjectorImage"] = os.Getenv("NETWORK_RESOURCES_INJECTOR_IMAGE")
		data.Data["SriovNetworkWebhookImage"] = os.Getenv("SRIOV_NETWORK_WEBHOOK_IMAGE")
		data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
		data.Data["ClusterType"] = utils.ClusterType
		data.Data["CaBundle"] = os.Getenv("WEBHOOK_CA_BUNDLE")
		objs, err := render.RenderDir(path, &data)
		if err != nil {
			logger.Error(err, "Fail to render webhook manifests")
			return err
		}

		// Delete injector webhook
		if *dc.Spec.EnableInjector != true && path == INJECTOR_WEBHOOK_PATH {
			for _, obj := range objs {
				err = r.deleteWebhookObject(obj)
				if err != nil {
					return err
				}
			}
			logger.Info("SR-IOV Admission Controller is disabled.")
			logger.Info("To enable SR-IOV Admission Controller,")
			logger.Info("Set 'SriovOperatorConfig.Spec.EnableInjector' to true(bool).")
			continue
		}
		// Delete operator webhook
		if *dc.Spec.EnableOperatorWebhook != true && path == OPERATOR_WEBHOOK_PATH {
			for _, obj := range objs {
				err = r.deleteWebhookObject(obj)
				if err != nil {
					return err
				}
			}
			logger.Info("Operator Admission Controller is disabled.")
			logger.Info("To enable Operator Admission Controller,")
			logger.Info("Set 'SriovOperatorConfig.Spec.EnableOperatorWebhook' to true(bool).")
			continue
		}

		// Sync Webhook
		for _, obj := range objs {
			err = r.syncK8sResource(dc, obj)
			if err != nil {
				logger.Error(err, "Couldn't sync webhook objects")
				return err
			}
		}
	}

	return nil
}

func (r *SriovOperatorConfigReconciler) deleteWebhookObject(obj *uns.Unstructured) error {
	if err := r.deleteK8sResource(obj); err != nil {
		return err
	}
	return nil
}

func (r *SriovOperatorConfigReconciler) deleteK8sResource(in *uns.Unstructured) error {
	if err := apply.DeleteObject(context.TODO(), r, in); err != nil {
		return fmt.Errorf("failed to delete object %v with err: %v", in, err)
	}
	return nil
}

func (r *SriovOperatorConfigReconciler) syncK8sResource(cr *sriovnetworkv1.SriovOperatorConfig, in *uns.Unstructured) error {
	switch in.GetKind() {
	case "ClusterRole", "ClusterRoleBinding", "MutatingWebhookConfiguration", "ValidatingWebhookConfiguration":
	default:
		// set owner-reference only for namespaced objects
		if err := controllerutil.SetControllerReference(cr, in, r.Scheme); err != nil {
			return err
		}
	}
	if err := apply.ApplyObject(context.TODO(), r, in); err != nil {
		return fmt.Errorf("failed to apply object %v with err: %v", in, err)
	}
	return nil
}

func (r *SriovOperatorConfigReconciler) syncOffloadMachineConfig(dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := r.Log.WithName("syncOffloadMachineConfig")
	var err error

	logger.Info("Start to render MachineConfig and MachineConfigPool for OVS HW offloading")
	data := render.MakeRenderData()
	data.Data["HwOffloadNodeLabel"] = HwOffloadNodeLabel
	mcName := "00-" + HwOffloadNodeLabel
	mcpName := HwOffloadNodeLabel
	mc, err := render.GenerateMachineConfig("bindata/manifests/switchdev-config", mcName, HwOffloadNodeLabel, dc.Spec.EnableOvsOffload, &data)
	if err != nil {
		return err
	}
	mcpRaw, err := render.RenderTemplate("bindata/manifests/switchdev-config/machineconfigpool.yaml", &data)
	if err != nil {
		return err
	}
	mcp := &mcfgv1.MachineConfigPool{}
	if len(mcpRaw) != 1 {
		return fmt.Errorf("Invalid MachineConfigPool CR template")
	}
	err = r.Scheme.Convert(mcpRaw[0], mcp, context.TODO())
	if err != nil {
		return err
	}

	foundMC := &mcfgv1.MachineConfig{}
	foundMCP := &mcfgv1.MachineConfigPool{}

	err = r.Get(context.TODO(), types.NamespacedName{Name: mcName}, foundMC)
	if err != nil {
		if errors.IsNotFound(err) {
			if dc.Spec.EnableOvsOffload {
				err = r.Create(context.TODO(), mc)
				if err != nil {
					return fmt.Errorf("Couldn't create MachineConfig: %v", err)
				}
				logger.Info("Created MachineConfig CR")
			}
		} else {
			return fmt.Errorf("Failed to get MachineConfig: %v", err)
		}
	} else {
		if dc.Spec.EnableOvsOffload {
			if bytes.Compare(foundMC.Spec.Config.Raw, mc.Spec.Config.Raw) == 0 {
				logger.Info("MachineConfig already exists, updating")
				err = r.Update(context.TODO(), foundMC)
				if err != nil {
					return fmt.Errorf("Couldn't update MachineConfig: %v", err)
				}
			} else {
				logger.Info("No content change, skip updating MC")
			}
		} else {
			logger.Info("offload disabled, delete MachineConfig")
			err = r.Delete(context.TODO(), foundMC)
			if err != nil {
				return fmt.Errorf("Couldn't delete MachineConfig: %v", err)
			}
		}
	}

	err = r.Get(context.TODO(), types.NamespacedName{Name: mcpName}, foundMCP)
	if err != nil {
		if errors.IsNotFound(err) {
			if dc.Spec.EnableOvsOffload {
				err = r.Create(context.TODO(), mcp)
				if err != nil {
					return fmt.Errorf("Couldn't create MachineConfigPool: %v", err)
				}
				logger.Info("Created MachineConfigPool CR")
			}
		} else {
			return fmt.Errorf("Failed to get MachineConfigPool: %v", err)
		}
	} else {
		if dc.Spec.EnableOvsOffload {
			modified := resourcemerge.BoolPtr(false)
			resourcemerge.EnsureMachineConfigPool(modified, foundMCP, *mcp)
			if *modified {
				logger.Info("MachineConfig already exists, updating")
				err = r.Update(context.TODO(), foundMCP)
				if err != nil {
					return fmt.Errorf("Couldn't update MachineConfig: %v", err)
				}
			} else {
				logger.Info("No content change, skip updating MCP")
			}
		} else {
			logger.Info("offload disabled, delete MachineConfigPool")
			err = r.Delete(context.TODO(), foundMCP)
			if err != nil {
				return fmt.Errorf("Couldn't delete MachineConfigPool: %v", err)
			}
		}
	}
	return nil
}
