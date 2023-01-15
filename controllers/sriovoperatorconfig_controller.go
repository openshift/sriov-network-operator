/*
Copyright 2021.

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
	"context"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	apply "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apply"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	render "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	utils "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

// SriovOperatorConfigReconciler reconciles a SriovOperatorConfig object
type SriovOperatorConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovoperatorconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovoperatorconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovoperatorconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SriovOperatorConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *SriovOperatorConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sriovoperatorconfig", req.NamespacedName)

	logger.Info("Reconciling SriovOperatorConfig")

	enableAdmissionController := os.Getenv("ENABLE_ADMISSION_CONTROLLER") == "true"
	if !enableAdmissionController {
		logger.Info("SR-IOV Network Resource Injector and Operator Webhook are disabled.")
	}
	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err := r.Get(ctx, types.NamespacedName{
		Name: constants.DefaultConfigName, Namespace: namespace}, defaultConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			singleNode, err := utils.IsSingleNodeCluster(r.Client)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("couldn't get cluster single node status: %s", err)
			}

			// Default Config object not found, create it.
			defaultConfig.SetNamespace(namespace)
			defaultConfig.SetName(constants.DefaultConfigName)
			defaultConfig.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
				EnableInjector:           func() *bool { b := enableAdmissionController; return &b }(),
				EnableOperatorWebhook:    func() *bool { b := enableAdmissionController; return &b }(),
				ConfigDaemonNodeSelector: map[string]string{},
				LogLevel:                 2,
				DisableDrain:             singleNode,
			}

			err = r.Create(ctx, defaultConfig)
			if err != nil {
				logger.Error(err, "Failed to create default Operator Config", "Namespace",
					namespace, "Name", constants.DefaultConfigName)
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
	if err = r.syncWebhookObjs(ctx, defaultConfig); err != nil {
		return reconcile.Result{}, err
	}

	// Sync SriovNetworkConfigDaemon objects
	if err = r.syncConfigDaemonSet(ctx, defaultConfig); err != nil {
		return reconcile.Result{}, err
	}

	if err = r.syncPluginDaemonSet(ctx, defaultConfig); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: constants.ResyncPeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SriovOperatorConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sriovnetworkv1.SriovOperatorConfig{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func (r *SriovOperatorConfigReconciler) syncPluginDaemonSet(ctx context.Context, dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.Log.WithName("syncConfigDaemonset")
	logger.Info("Start to sync SRIOV plugin daemonsets nodeSelector")
	ds := &appsv1.DaemonSet{}

	names := []string{"sriov-cni", "sriov-device-plugin"}

	if len(dc.Spec.ConfigDaemonNodeSelector) == 0 {
		return nil
	}
	for _, name := range names {
		err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ds)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			logger.Error(err, "Couldn't get daemonset", "name", name)
			return err
		}
		ds.Spec.Template.Spec.NodeSelector = dc.Spec.ConfigDaemonNodeSelector
		err = r.Client.Update(ctx, ds)
		if err != nil {
			logger.Error(err, "Couldn't update daemonset", "name", name)
			return err
		}
	}

	return nil
}

func (r *SriovOperatorConfigReconciler) syncConfigDaemonSet(ctx context.Context, dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.Log.WithName("syncConfigDaemonset")
	logger.Info("Start to sync config daemonset")

	data := render.MakeRenderData()
	data.Data["Image"] = os.Getenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE")
	data.Data["Namespace"] = namespace
	data.Data["SRIOVCNIImage"] = os.Getenv("SRIOV_CNI_IMAGE")
	data.Data["SRIOVInfiniBandCNIImage"] = os.Getenv("SRIOV_INFINIBAND_CNI_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ClusterType"] = utils.ClusterType
	data.Data["DevMode"] = os.Getenv("DEV_MODE")
	data.Data["ImagePullSecrets"] = GetImagePullSecrets()
	envCniBinPath := os.Getenv("SRIOV_CNI_BIN_PATH")
	if envCniBinPath == "" {
		data.Data["CNIBinPath"] = "/var/lib/cni/bin"
	} else {
		logger.Info("New cni bin found", "CNIBinPath", envCniBinPath)
		data.Data["CNIBinPath"] = envCniBinPath
	}
	objs, err := render.RenderDir(constants.ConfigDaemonPath, &data)
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
		err = r.syncK8sResource(ctx, dc, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}
	return nil
}

func (r *SriovOperatorConfigReconciler) syncWebhookObjs(ctx context.Context, dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.Log.WithName("syncWebhookObjs")
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
		data.Data["DevMode"] = os.Getenv("DEV_MODE")
		data.Data["ImagePullSecrets"] = GetImagePullSecrets()
		external, err := utils.IsExternalControlPlaneCluster(r.Client)
		if err != nil {
			logger.Error(err, "Fail to get control plane topology")
			return err
		}
		data.Data["ExternalControlPlane"] = external
		objs, err := render.RenderDir(path, &data)
		if err != nil {
			logger.Error(err, "Fail to render webhook manifests")
			return err
		}

		// Delete injector webhook
		if !*dc.Spec.EnableInjector && path == constants.InjectorWebHookPath {
			for _, obj := range objs {
				err = r.deleteWebhookObject(ctx, obj)
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
		if !*dc.Spec.EnableOperatorWebhook && path == constants.OperatorWebHookPath {
			for _, obj := range objs {
				err = r.deleteWebhookObject(ctx, obj)
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
			err = r.syncK8sResource(ctx, dc, obj)
			if err != nil {
				logger.Error(err, "Couldn't sync webhook objects")
				return err
			}
		}
	}

	return nil
}

func (r *SriovOperatorConfigReconciler) deleteWebhookObject(ctx context.Context, obj *uns.Unstructured) error {
	if err := r.deleteK8sResource(ctx, obj); err != nil {
		return err
	}
	return nil
}

func (r *SriovOperatorConfigReconciler) deleteK8sResource(ctx context.Context, in *uns.Unstructured) error {
	if err := apply.DeleteObject(ctx, r.Client, in); err != nil {
		return fmt.Errorf("failed to delete object %v with err: %v", in, err)
	}
	return nil
}

func (r *SriovOperatorConfigReconciler) syncK8sResource(ctx context.Context, cr *sriovnetworkv1.SriovOperatorConfig, in *uns.Unstructured) error {
	switch in.GetKind() {
	case "ClusterRole", "ClusterRoleBinding", "MutatingWebhookConfiguration", "ValidatingWebhookConfiguration":
	default:
		// set owner-reference only for namespaced objects
		if err := controllerutil.SetControllerReference(cr, in, r.Scheme); err != nil {
			return err
		}
	}
	if err := apply.ApplyObject(ctx, r.Client, in); err != nil {
		return fmt.Errorf("failed to apply object %v with err: %v", in, err)
	}
	return nil
}
