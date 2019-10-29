package caconfig

import (
	"bytes"
	"context"
	"time"

	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	config "github.com/openshift/sriov-network-operator/pkg/controller/sriovoperatorconfig"
)

var (
	log          = logf.Log.WithName("controller_caconfig")
	ResyncPeriod = 1 * time.Minute
)

// Add creates a new CA ConfigMap Controller and adds it to the Manager.
// The Manager will set fields on the Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileCAConfigMap{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("caconfig-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource CA ConfigMap
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileCAConfigMap{}

// ReconcileCAConfigMap reconciles a ConfigMap object
type ReconcileCAConfigMap struct {
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile updates MutatingWebhookConfiguration CABundle
func (r *ReconcileCAConfigMap) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling CA config map")

	whName := ""
	if request.Name == config.INJECTOR_SERVICE_CA_CONFIGMAP {
		whName = config.INJECTOR_WEBHOOK_NAME
	} else if request.Name == config.WEBHOOK_SERVICE_CA_CONFIGMAP {
		whName = config.OPERATOR_WEBHOOK_NAME
	} else {
		return reconcile.Result{}, nil
	}

	caBundleConfigMap := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), request.NamespacedName, caBundleConfigMap)
	if err != nil {
		reqLogger.Error(err, "Couldn't get caBundle ConfigMap", "name", request.Name)
		return reconcile.Result{}, err
	}

	caBundleData, ok := caBundleConfigMap.Data["service-ca.crt"]
	if !ok {
		return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
	}

	mutateWebhookConfig := &admissionregistrationv1beta1.MutatingWebhookConfiguration{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: whName}, mutateWebhookConfig)
	if errors.IsNotFound(err) {
		reqLogger.Error(err, "Couldn't find", "mutate webhook config:", whName)
	}
	modified := false
	for idx, webhook := range mutateWebhookConfig.Webhooks {
		// Update CABundle if CABundle is empty or updated.
		if webhook.ClientConfig.CABundle == nil || !bytes.Equal(webhook.ClientConfig.CABundle, []byte(caBundleData)) {
			modified = true
			mutateWebhookConfig.Webhooks[idx].ClientConfig.CABundle = []byte(caBundleData)
		}
	}
	if modified {
		// Update webhookConfig
		err = r.client.Update(context.TODO(), mutateWebhookConfig)
		if err != nil {
			reqLogger.Error(err, "Couldn't update mutate webhook config")
		}
	}

	validateWebhookConfig := &admissionregistrationv1beta1.ValidatingWebhookConfiguration{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: whName}, validateWebhookConfig)
	if errors.IsNotFound(err) {
		reqLogger.Info("Couldn't find", "validate webhook config:", whName)
	}
	modified = false
	for idx, webhook := range validateWebhookConfig.Webhooks {
		// Update CABundle if CABundle is empty or updated.
		if webhook.ClientConfig.CABundle == nil || !bytes.Equal(webhook.ClientConfig.CABundle, []byte(caBundleData)) {
			modified = true
			validateWebhookConfig.Webhooks[idx].ClientConfig.CABundle = []byte(caBundleData)
		}
	}
	if modified {
		// Update webhookConfig
		err = r.client.Update(context.TODO(), validateWebhookConfig)
		if err != nil {
			reqLogger.Error(err, "Couldn't update validate webhook config")
		}
	}
	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}
