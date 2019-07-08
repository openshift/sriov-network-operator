package caconfig

import (
	"bytes"
	"context"
	"os"
	"time"

	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	SERVICE_CA_CONFIGMAP        = "openshift-service-ca"
	SRIOV_MUTATING_WEBHOOK_NAME = "network-resources-injector-config"
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

// Reconcile updates MutatingWebhookConfiguration CABundle, given from SERVICE_CA_CONFIGMAP
func (r *ReconcileCAConfigMap) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling CA config map")

	if request.Namespace != os.Getenv("NAMESPACE") || request.Name != SERVICE_CA_CONFIGMAP {
		return reconcile.Result{}, nil
	}

	caBundleConfigMap := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), request.NamespacedName, caBundleConfigMap)
	if err != nil {
		reqLogger.Error(err, "Couldn't get caBundle ConfigMap")
		return reconcile.Result{}, err
	}

	caBundleData, ok := caBundleConfigMap.Data["service-ca.crt"]
	if !ok {
		return reconcile.Result{}, err
	}

	webhookConfig := &admissionregistrationv1beta1.MutatingWebhookConfiguration{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: SRIOV_MUTATING_WEBHOOK_NAME}, webhookConfig)
	if errors.IsNotFound(err) {
		reqLogger.Error(err, "Couldn't find webhook config")
		return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
	}

	modified := false
	for idx, webhook := range webhookConfig.Webhooks {
		// Update CABundle if CABundle is empty or updated.
		if webhook.ClientConfig.CABundle == nil || !bytes.Equal(webhook.ClientConfig.CABundle, []byte(caBundleData)) {
			modified = true
			webhookConfig.Webhooks[idx].ClientConfig.CABundle = []byte(caBundleData)
		}
	}
	if !modified {
		return reconcile.Result{}, err
	}

	// Update webhookConfig
	err = r.client.Update(context.TODO(), webhookConfig)
	if err != nil {
		reqLogger.Error(err, "Couldn't update webhook config")
	}

	return reconcile.Result{}, nil
}
