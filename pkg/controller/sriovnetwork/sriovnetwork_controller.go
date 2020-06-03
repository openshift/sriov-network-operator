package sriovnetwork

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	render "github.com/openshift/sriov-network-operator/pkg/render"
)

const (
	MANIFESTS_PATH       = "./bindata/manifests/cni-config"
	LASTNETWORKNAMESPACE = "operator.sriovnetwork.openshift.io/last-network-namespace"
	FINALIZERNAME        = "netattdef.finalizers.sriovnetwork.openshift.io"
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
	err = c.Watch(&source.Kind{Type: &sriovnetworkv1.SriovNetwork{}}, &handler.EnqueueRequestForObject{})
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
	instance := &sriovnetworkv1.SriovNetwork{}
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
		if !sriovnetworkv1.StringInArray(FINALIZERNAME, instance.ObjectMeta.Finalizers) {
			instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, FINALIZERNAME)
			if err := r.client.Update(context.Background(), instance); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if sriovnetworkv1.StringInArray(FINALIZERNAME, instance.ObjectMeta.Finalizers) {
			// our finalizer is present, so lets handle any external dependency
			reqLogger.Info("delete NetworkAttachmentDefinition CR", "Namespace", instance.Spec.NetworkNamespace, "Name", instance.Name)
			if err := r.deleteNetAttDef(instance); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return reconcile.Result{}, err
			}
			// remove our finalizer from the list and update it.
			instance.ObjectMeta.Finalizers = sriovnetworkv1.RemoveString(FINALIZERNAME, instance.ObjectMeta.Finalizers)
			if err := r.client.Update(context.Background(), instance); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, err
	}
	raw, err := renderNetAttDef(instance)
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

func (r *ReconcileSriovNetwork) deleteNetAttDef(sn *sriovnetworkv1.SriovNetwork) error {
	// Fetch the NetworkAttachmentDefinition instance
	instance := &netattdefv1.NetworkAttachmentDefinition{}
	namespace := sn.GetNamespace()
	if sn.Spec.NetworkNamespace != "" {
		namespace = sn.Spec.NetworkNamespace
	}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: sn.GetName()}, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	err = r.client.Delete(context.TODO(), instance)
	if err != nil {
		return err
	}
	return nil
}

// renderNetAttDef returns a busybox pod with the same name/namespace as the cr
func renderNetAttDef(cr *sriovnetworkv1.SriovNetwork) (*uns.Unstructured, error) {
	logger := log.WithName("renderNetAttDef")
	logger.Info("Start to render SRIOV CNI NetworkAttachementDefinition")
	var err error
	objs := []*uns.Unstructured{}

	// render RawCNIConfig manifests
	data := render.MakeRenderData()
	data.Data["SriovNetworkName"] = cr.Name
	if cr.Spec.NetworkNamespace == "" {
		data.Data["SriovNetworkNamespace"] = cr.Namespace
	} else {
		data.Data["SriovNetworkNamespace"] = cr.Spec.NetworkNamespace
	}
	data.Data["SriovCniResourceName"] = os.Getenv("RESOURCE_PREFIX") + "/" + cr.Spec.ResourceName
	data.Data["SriovCniVlan"] = cr.Spec.Vlan

	if cr.Spec.VlanQoS <= 7 && cr.Spec.VlanQoS >= 0 {
		data.Data["VlanQoSConfigured"] = true
		data.Data["SriovCniVlanQoS"] = cr.Spec.VlanQoS
	} else {
		data.Data["VlanQoSConfigured"] = false
	}

	if cr.Spec.Capabilities == "" {
		data.Data["CapabilitiesConfigured"] = false
	} else {
		data.Data["CapabilitiesConfigured"] = true
		data.Data["SriovCniCapabilities"] = cr.Spec.Capabilities
	}

	data.Data["SpoofChkConfigured"] = true
	switch cr.Spec.SpoofChk {
	case "off":
		data.Data["SriovCniSpoofChk"] = "off"
	case "on":
		data.Data["SriovCniSpoofChk"] = "on"
	default:
		data.Data["SpoofChkConfigured"] = false
	}

	data.Data["TrustConfigured"] = true
	switch cr.Spec.Trust {
	case "on":
		data.Data["SriovCniTrust"] = "on"
	case "off":
		data.Data["SriovCniTrust"] = "off"
	default:
		data.Data["TrustConfigured"] = false
	}

	data.Data["StateConfigured"] = true
	switch cr.Spec.LinkState {
	case "enable":
		data.Data["SriovCniState"] = "enable"
	case "disable":
		data.Data["SriovCniState"] = "disable"
	case "auto":
		data.Data["SriovCniState"] = "auto"
	default:
		data.Data["StateConfigured"] = false
	}

	data.Data["MinTxRateConfigured"] = false
	if cr.Spec.MinTxRate != nil {
		if *cr.Spec.MinTxRate >= 0 {
			data.Data["MinTxRateConfigured"] = true
			data.Data["SriovCniMinTxRate"] = *cr.Spec.MinTxRate
		}
	}

	data.Data["MaxTxRateConfigured"] = false
	if cr.Spec.MaxTxRate != nil {
		if *cr.Spec.MaxTxRate >= 0 {
			data.Data["MaxTxRateConfigured"] = true
			data.Data["SriovCniMaxTxRate"] = *cr.Spec.MaxTxRate
		}
	}

	if cr.Spec.IPAM != "" {
		data.Data["SriovCniIpam"] = "\"ipam\":" + strings.Join(strings.Fields(cr.Spec.IPAM), "")
	} else {
		data.Data["SriovCniIpam"] = "\"ipam\":{}"
	}

	objs, err = render.RenderDir(MANIFESTS_PATH, &data)
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		raw, _ := json.Marshal(obj)
		logger.Info("render NetworkAttachementDefinition output", "raw", string(raw))
	}
	return objs[0], nil
}
