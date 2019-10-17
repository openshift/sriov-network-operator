package sriovoperatorconfig

import (
	"context"
	"fmt"
	"os"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	apply "github.com/openshift/sriov-network-operator/pkg/apply"
	render "github.com/openshift/sriov-network-operator/pkg/render"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_sriovoperatorconfig")

const (
	DEFAULT_CONFIG_NAME	    = "default"
	WEBHOOK_PATH                = "./bindata/manifests/webhook"
	SERVICE_CA_CONFIGMAP        = "openshift-service-ca"
	SRIOV_MUTATING_WEBHOOK_NAME = "network-resources-injector-config"
)

var Namespace = os.Getenv("NAMESPACE")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new SriovOperatorConfig Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileSriovOperatorConfig{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("sriovoperatorconfig-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource SriovOperatorConfig
	err = c.Watch(&source.Kind{Type: &sriovnetworkv1.SriovOperatorConfig{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner SriovOperatorConfig
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &sriovnetworkv1.SriovOperatorConfig{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileSriovOperatorConfig implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileSriovOperatorConfig{}

// ReconcileSriovOperatorConfig reconciles a SriovOperatorConfig object
type ReconcileSriovOperatorConfig struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a SriovOperatorConfig object and makes changes based on the state read
// and what is in the SriovOperatorConfig.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileSriovOperatorConfig) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling SriovOperatorConfig")

	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: DEFAULT_CONFIG_NAME, Namespace: Namespace}, defaultConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			// Default Config object not found, create it.
                        defaultConfig.SetNamespace(Namespace)
                        defaultConfig.SetName(DEFAULT_CONFIG_NAME)
                        defaultConfig.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
				EnableInjector: func() *bool { b := true; return &b }(),
                        }
                        err = r.client.Create(context.TODO(), defaultConfig)
                        if err != nil {
                                reqLogger.Error(err, "Failed to create default Operator Config", "Namespace", Namespace, "Name", DEFAULT_CONFIG_NAME)
                                return reconcile.Result{},err
                        }
                        return reconcile.Result{}, nil
                }
                // Error reading the object - requeue the request.
                return reconcile.Result{}, err
	}

	if request.Namespace != Namespace {
		reqLogger.Info("Creating SriovOperatorConfig in", request.Namespace, "namespace is not supported, use namespace", Namespace)
		return reconcile.Result{}, nil
	}

	// Fetch the SriovOperatorConfig instance
	instance := &sriovnetworkv1.SriovOperatorConfig{}
	err = r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Render and sync webhook objects
	if err = r.syncWebhookObjs(instance); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileSriovOperatorConfig) syncWebhookObjs(dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.WithName("syncWebhookObjs")
	logger.Info("Start to sync webhook objects")

	// Render Webhook manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = os.Getenv("NAMESPACE")
	data.Data["ServiceCAConfigMap"] = SERVICE_CA_CONFIGMAP
	data.Data["SRIOVMutatingWebhookName"] = SRIOV_MUTATING_WEBHOOK_NAME
	data.Data["NetworkResourcesInjectorImage"] = os.Getenv("NETWORK_RESOURCES_INJECTOR_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	objs, err := render.RenderDir(WEBHOOK_PATH, &data)
	if err != nil {
		logger.Error(err, "Fail to render webhook manifests")
		return err
	}

	// Delete webhook
	if *dc.Spec.EnableInjector != true {
		for _, obj := range objs {
			err = r.deleteWebhookObject(obj)
			if err != nil {
				return err
			}
		}
		logger.Info("SR-IOV Admission Controller is disabled.")
		logger.Info("To enable SR-IOV Admission Controller, set 'SriovOperatorConfig.Spec.EnableInjector' to true(bool).")
		return nil
	}

	// Sync Webhook
	for _, obj := range objs {
		err = r.syncWebhookObject(dc, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync webhook objects")
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovOperatorConfig) deleteWebhookObject(obj *uns.Unstructured) error {
	if err := r.deleteK8sResource(obj); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileSriovOperatorConfig) syncWebhookObject(dc *sriovnetworkv1.SriovOperatorConfig, obj *uns.Unstructured) error {
	var err error
	logger := log.WithName("syncWebhookObject")
	logger.Info("Start to sync Objects")
	scheme := kscheme.Scheme
	switch kind := obj.GetKind(); kind {
	case "MutatingWebhookConfiguration":
		whs := &admissionregistrationv1beta1.MutatingWebhookConfiguration{}
		err = scheme.Convert(obj, whs, nil)
		r.syncWebhook(dc, whs)
		if err != nil {
			logger.Error(err, "Fail to sync mutate webhook")
			return err
		}
	case "ConfigMap":
		cm := &corev1.ConfigMap{}
		err = scheme.Convert(obj, cm, nil)
		r.syncWebhookConfigMap(dc, cm)
		if err != nil {
			logger.Error(err, "Fail to sync webhook config map")
			return err
		}
	case "ServiceAccount", "DaemonSet", "Service", "ClusterRole", "ClusterRoleBinding":
		r.syncK8sResource(dc, obj)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovOperatorConfig) syncWebhook(cr *sriovnetworkv1.SriovOperatorConfig, in *admissionregistrationv1beta1.MutatingWebhookConfiguration) error {
	logger := log.WithName("syncWebhook")
	logger.Info("Start to sync webhook", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	whs := &admissionregistrationv1beta1.MutatingWebhookConfiguration{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: in.Name}, whs)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create webhook: %v", err)
			}
			logger.Info("Create webhook for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get webhook: %v", err)
		}
	} else {
		if *cr.Spec.EnableInjector != true {
			logger.Info("Deleting webhook Config")
			err = r.client.Delete(context.TODO(), whs)
			if err != nil {
				return fmt.Errorf("Couldn't delete webhook config: %v", err)
			}
		} else {
			logger.Info("Webhook already exists, updating")
			for idx, wh := range whs.Webhooks {
				if wh.ClientConfig.CABundle != nil {
					in.Webhooks[idx].ClientConfig.CABundle = wh.ClientConfig.CABundle
				}
			}
			err = r.client.Update(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't update webhook: %v", err)
			}
		}
	}
	return nil
}

func (r *ReconcileSriovOperatorConfig) syncWebhookConfigMap(cr *sriovnetworkv1.SriovOperatorConfig, in *corev1.ConfigMap) error {
	logger := log.WithName("syncWebhookConfigMap")
	logger.Info("Start to sync config map", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	cm := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create config map: %v", err)
			}
			logger.Info("Create config map for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get config map: %v", err)
		}
	} else {
		if *cr.Spec.EnableInjector != true {
			logger.Info("Deleting webhook ConfigMap")
			err = r.client.Delete(context.TODO(), cm)
			if err != nil {
				return fmt.Errorf("Couldn't delete webhook configmap: %v", err)
			}
		} else {
			logger.Info("Config map already exists, updating")
			_, ok := cm.Data["service-ca.crt"]
			if ok {
				in.Data = cm.Data
			}
			err = r.client.Update(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't update config map: %v", err)
			}
		}
	}
	return nil
}

func (r *ReconcileSriovOperatorConfig) deleteK8sResource(in *uns.Unstructured) error {
	if err := apply.DeleteObject(context.TODO(), r.client, in); err != nil {
		return fmt.Errorf("failed to delete object %v with err: %v", in, err)
	}
        return nil
}

func (r *ReconcileSriovOperatorConfig) syncK8sResource(cr *sriovnetworkv1.SriovOperatorConfig, in *uns.Unstructured) error {
	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	if err := apply.ApplyObject(context.TODO(), r.client, in); err != nil {
		return fmt.Errorf("failed to apply object %v with err: %v", in, err)
	}
        return nil
}
