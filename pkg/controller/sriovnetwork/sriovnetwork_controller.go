package sriovnetwork

import (
	"context"
	"fmt"
	"os"
	"strings"

	netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	render "github.com/openshift/sriov-network-operator/pkg/render"

	"encoding/json"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	MANIFESTS_PATH = "./bindata/manifests/cni-config"
)

var log = logf.Log.WithName("controller_sriovnetwork")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new SriovNetwork Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	reconciler, err := newReconciler(mgr)
	if err != nil {
		return err
	}

	return add(mgr, reconciler)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	// The default client serves read requests from the cache which contains
	// objects only from the namespace the operator is watching. Given we need
	// to query other namespaces for NetworkAttachmentDefinitions, we create our
	// own client and pass it the manager's scheme which has all our registered types
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	client, err := client.New(cfg, client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return nil, err
	}

	return &ReconcileSriovNetwork{client: client, scheme: mgr.GetScheme()}, nil
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

	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileSriovNetwork{}
var lastNetworkNamespace = make(map[types.UID]string)

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

	// Fetch the SriovNetwork instance
	instance := &sriovnetworkv1.SriovNetwork{}
	var ok bool
	var namespace = ""
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
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

	// Set SriovNetwork instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, netAttDef, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	if namespace, ok = lastNetworkNamespace[instance.GetUID()]; ok {
		if namespace == netAttDef.GetNamespace() {
			// Check if this NetworkAttachmentDefinition already exists
			found := &netattdefv1.NetworkAttachmentDefinition{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: netAttDef.Name, Namespace: namespace}, found)
			if err != nil {
				return reconcile.Result{}, err
			}
			reqLogger.Info("NetworkAttachmentDefinition already exists, updating")
			found.Spec = netAttDef.Spec
			err = r.client.Update(context.TODO(), found)
			if err != nil {
				reqLogger.Error(err, "Couldn't update NetworkAttachmentDefinition", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
				return reconcile.Result{}, err
			}
			// NetworkAttachmentDefinition updated successfully - don't requeue
			return reconcile.Result{}, nil
		} else {
			err = r.client.Delete(context.TODO(), &netattdefv1.NetworkAttachmentDefinition{
			    ObjectMeta: metav1.ObjectMeta{
					Name: netAttDef.GetName(), 
					Namespace: namespace,
				},
			})
			if err != nil {
				reqLogger.Error(err, "Couldn't delete NetworkAttachmentDefinition", "Namespace", namespace, "Name", netAttDef.GetName())
				return reconcile.Result{}, err
			}
		}
	} else {
		lastNetworkNamespace[instance.GetUID()] = netAttDef.GetNamespace()
		namespace = netAttDef.GetNamespace()
	}
	reqLogger.Info("Creating a new NetworkAttachmentDefinition", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
	err = r.client.Create(context.TODO(), netAttDef)
	if err != nil {
		reqLogger.Error(err, "Couldn't create NetworkAttachmentDefinition", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
		return reconcile.Result{}, err
	}
	lastNetworkNamespace[instance.GetUID()] = netAttDef.GetNamespace()
	// NetworkAttachmentDefinition re-created successfully - don't requeue
	return reconcile.Result{}, nil
}

// renderDsForCR returns a busybox pod with the same name/namespace as the cr
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
	if cr.Spec.SpoofChk == nil {
		data.Data["SpoofChkConfigured"] = false
	} else {
		data.Data["SpoofChkConfigured"] = true
		switch *cr.Spec.SpoofChk {
		case false:
			data.Data["SriovCniSpoofChk"] = "off"
		default:
			data.Data["SriovCniSpoofChk"] = "on"
		}
	}
	if cr.Spec.Trust == nil {
		data.Data["TrustConfigured"] = false
	} else {
		data.Data["TrustConfigured"] = true
		switch *cr.Spec.Trust {
		case true:
			data.Data["SriovCniTrust"] = "on"
		default:
			data.Data["SriovCniTrust"] = "off"
		}
	}
	data.Data["SriovCniIpam"] = "\"ipam\":" + strings.Join(strings.Fields(cr.Spec.IPAM), "")

	objs, err = render.RenderDir(MANIFESTS_PATH, &data)
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		raw, _ := json.Marshal(obj)
		fmt.Printf("manifest %s\n", raw)
	}
	return objs[0], nil
}
