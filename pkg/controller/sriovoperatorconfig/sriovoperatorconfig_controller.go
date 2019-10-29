package sriovoperatorconfig

import (
	"context"
	"fmt"
	"os"
	"time"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	apply "github.com/openshift/sriov-network-operator/pkg/apply"
	render "github.com/openshift/sriov-network-operator/pkg/render"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
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
	ResyncPeriod                    = 5 * time.Minute
	DEFAULT_CONFIG_NAME             = "default"
	CONFIG_DAEMON_PATH              = "./bindata/manifests/daemon"
	INJECTOR_WEBHOOK_PATH           = "./bindata/manifests/webhook"
	OPERATOR_WEBHOOK_PATH           = "./bindata/manifests/operator-webhook"
	INJECTOR_SERVICE_CA_CONFIGMAP   = "injector-service-ca"
	WEBHOOK_SERVICE_CA_CONFIGMAP    = "webhook-service-ca"
	SERVICE_CA_CONFIGMAP_ANNOTATION = "service.beta.openshift.io/inject-cabundle"
	INJECTOR_WEBHOOK_NAME           = "network-resources-injector-config"
	OPERATOR_WEBHOOK_NAME           = "operator-webhook-config"
)

var Webhooks = map[string](string){
	INJECTOR_WEBHOOK_NAME: INJECTOR_WEBHOOK_PATH,
	OPERATOR_WEBHOOK_NAME: OPERATOR_WEBHOOK_PATH,
}

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
	err = c.Watch(&source.Kind{Type: &sriovnetworkv1.SriovOperatorConfig{}},
		&handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource DaemonSet
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
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
	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name: DEFAULT_CONFIG_NAME, Namespace: Namespace}, defaultConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			// Default Config object not found, create it.
			defaultConfig.SetNamespace(Namespace)
			defaultConfig.SetName(DEFAULT_CONFIG_NAME)
			defaultConfig.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
				EnableInjector:           func() *bool { b := true; return &b }(),
				EnableOperatorWebhook:    func() *bool { b := true; return &b }(),
				ConfigDaemonNodeSelector: map[string]string{},
			}
			err = r.client.Create(context.TODO(), defaultConfig)
			if err != nil {
				reqLogger.Error(err, "Failed to create default Operator Config", "Namespace",
					Namespace, "Name", DEFAULT_CONFIG_NAME)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if request.Namespace != Namespace {
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

	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}

func (r *ReconcileSriovOperatorConfig) syncConfigDaemonSet(dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.WithName("syncConfigDaemonset")
	logger.Info("Start to sync config daemonset")
	// var err error
	objs := []*uns.Unstructured{}

	data := render.MakeRenderData()
	data.Data["Image"] = os.Getenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE")
	data.Data["Namespace"] = os.Getenv("NAMESPACE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
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

func (r *ReconcileSriovOperatorConfig) syncWebhookObjs(dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.WithName("syncWebhookObjs")
	logger.Info("Start to sync webhook objects")

	for name, path := range Webhooks {
		// Render Webhook manifests
		data := render.MakeRenderData()
		data.Data["Namespace"] = os.Getenv("NAMESPACE")
		data.Data["InjectorServiceCAConfigMap"] = INJECTOR_SERVICE_CA_CONFIGMAP
		data.Data["WebhookServiceCAConfigMap"] = WEBHOOK_SERVICE_CA_CONFIGMAP
		data.Data["SRIOVMutatingWebhookName"] = name
		data.Data["NetworkResourcesInjectorImage"] = os.Getenv("NETWORK_RESOURCES_INJECTOR_IMAGE")
		data.Data["SriovNetworkWebhookImage"] = os.Getenv("SRIOV_NETWORK_WEBHOOK_IMAGE")
		data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
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
			err = r.syncWebhookObject(dc, obj)
			if err != nil {
				logger.Error(err, "Couldn't sync webhook objects")
				return err
			}
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
		r.syncMutatingWebhook(dc, whs)
		if err != nil {
			logger.Error(err, "Fail to sync mutate webhook")
			return err
		}
	case "ValidatingWebhookConfiguration":
		whs := &admissionregistrationv1beta1.ValidatingWebhookConfiguration{}
		err = scheme.Convert(obj, whs, nil)
		r.syncValidatingWebhook(dc, whs)
		if err != nil {
			logger.Error(err, "Fail to sync validate webhook")
			return err
		}
	case "ConfigMap":
		cm := &corev1.ConfigMap{}
		err = scheme.Convert(obj, cm, nil)
		err = r.syncWebhookConfigMap(dc, cm)
		if err != nil {
			logger.Error(err, "Fail to sync webhook config map")
			return err
		}
	case "ServiceAccount", "DaemonSet", "Service", "ClusterRole", "ClusterRoleBinding":
		err = r.syncK8sResource(dc, obj)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovOperatorConfig) syncMutatingWebhook(cr *sriovnetworkv1.SriovOperatorConfig, in *admissionregistrationv1beta1.MutatingWebhookConfiguration) error {
	logger := log.WithName("syncMutatingWebhook")
	logger.Info("Start to sync mutating webhook", "Name", in.Name, "Namespace", in.Namespace)

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
	}

	// Note:
	// we don't need to manage the update of MutatingWebhookConfiguration here
	// as it's handled by caconfig controller

	return nil
}

func (r *ReconcileSriovOperatorConfig) syncValidatingWebhook(cr *sriovnetworkv1.SriovOperatorConfig, in *admissionregistrationv1beta1.ValidatingWebhookConfiguration) error {
	logger := log.WithName("syncValidatingWebhook")
	logger.Info("Start to sync validating webhook", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	whs := &admissionregistrationv1beta1.ValidatingWebhookConfiguration{}
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
	}

	// Note:
	// we don't need to manage the update of MutatingWebhookConfiguration here
	// as it's handled by caconfig controller

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
				return fmt.Errorf("Couldn't create webhook config map: %v", err)
			}
			logger.Info("Create webhook config map for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get webhook config map: %v", err)
		}
	} else {
		logger.Info("Webhook ConfigMap already exists, updating")
		cm.ObjectMeta.Annotations[SERVICE_CA_CONFIGMAP_ANNOTATION] = "true"
		err = r.client.Update(context.TODO(), cm)
		if err != nil {
			return fmt.Errorf("Couldn't update webhook config map: %v", err)
		}

	}

	// Note:
	// we don't need to manage the update of WebhookConfigMap here
	// as service-ca.crt is automatically injected by service-ca-operator

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
