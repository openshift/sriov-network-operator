package sriovnetwork

import (
	"context"
	"reflect"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	. "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

var log = logf.Log.WithName("controller_sriovnetwork")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new SriovNetwork Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileSriovNetwork{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("sriovnetwork-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource SriovNetwork
	err = c.Watch(&source.Kind{Type: &SriovNetwork{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}
	// Watch for changes to secondary resource NetworkAttachmentDefinition
	err = c.Watch(&source.Kind{Type: &netattdefv1.NetworkAttachmentDefinition{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileSriovNetwork{}

// ReconcileSriovNetwork reconciles a SriovNetwork object
type ReconcileSriovNetwork struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a SriovNetwork object and makes changes based on the state read
// and what is in the SriovNetwork.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileSriovNetwork) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling SriovNetwork")
	var err error

	// The SriovNetwork CR shall only be defined in operator namespace.
	request.Namespace, err = k8sutil.GetWatchNamespace()
	if err != nil {
		reqLogger.Error(err, "Failed get operator namespace")
		return reconcile.Result{}, err
	}
	// Fetch the SriovNetwork instance
	instance := &SriovNetwork{}
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
	// name of our custom finalizer

	// examine DeletionTimestamp to determine if object is under deletion
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !StringInArray(FINALIZERNAME, instance.ObjectMeta.Finalizers) {
			instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, FINALIZERNAME)
			if err := r.client.Update(context.Background(), instance); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if StringInArray(FINALIZERNAME, instance.ObjectMeta.Finalizers) {
			// our finalizer is present, so lets handle any external dependency
			reqLogger.Info("delete NetworkAttachmentDefinition CR", "Namespace", instance.Spec.NetworkNamespace, "Name", instance.Name)
			if err := instance.DeleteNetAttDef(r.client); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return reconcile.Result{}, err
			}
			// remove our finalizer from the list and update it.
			instance.ObjectMeta.Finalizers = RemoveString(FINALIZERNAME, instance.ObjectMeta.Finalizers)
			if err := r.client.Update(context.Background(), instance); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, err
	}
	raw, err := instance.RenderNetAttDef()
	if err != nil {
		return reconcile.Result{}, err
	}
	netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
	scheme := kscheme.Scheme
	err = scheme.Convert(raw, netAttDef, nil)
	if err != nil {
		return reconcile.Result{}, err
	}
	if lnns, ok := instance.GetAnnotations()[LASTNETWORKNAMESPACE]; ok && netAttDef.GetNamespace() != lnns {
		err = r.client.Delete(context.TODO(), &netattdefv1.NetworkAttachmentDefinition{
			ObjectMeta: v1.ObjectMeta{
				Name:      instance.GetName(),
				Namespace: lnns,
			},
		})
		if err != nil {
			reqLogger.Error(err, "Couldn't delete NetworkAttachmentDefinition CR", "Namespace", instance.GetName(), "Name", lnns)
			return reconcile.Result{}, err
		}
	}
	// Check if this NetworkAttachmentDefinition already exists
	found := &netattdefv1.NetworkAttachmentDefinition{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: netAttDef.Name, Namespace: netAttDef.Namespace}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("NetworkAttachmentDefinition CR not exist, creating")
			err = r.client.Create(context.TODO(), netAttDef)
			if err != nil {
				reqLogger.Error(err, "Couldn't create NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
				return reconcile.Result{}, err
			}
			anno := map[string]string{LASTNETWORKNAMESPACE: netAttDef.Namespace}
			instance.SetAnnotations(anno)
			if err := r.client.Update(context.Background(), instance); err != nil {
				return reconcile.Result{}, err
			}
		} else {
			reqLogger.Error(err, "Couldn't get NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("NetworkAttachmentDefinition CR already exist")
		if !reflect.DeepEqual(found.Spec, netAttDef.Spec) || !reflect.DeepEqual(found.GetAnnotations(), netAttDef.GetAnnotations()) {
			reqLogger.Info("Update NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
			netAttDef.SetResourceVersion(found.GetResourceVersion())
			err = r.client.Update(context.TODO(), netAttDef)
			if err != nil {
				reqLogger.Error(err, "Couldn't update NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
				return reconcile.Result{}, err
			}
		}
	}
	return reconcile.Result{}, nil
}
