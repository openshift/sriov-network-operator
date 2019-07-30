package sriovnetworknodepolicy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	// "time"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	render "github.com/openshift/sriov-network-operator/pkg/render"

	dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	errs "github.com/pkg/errors"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_sriovnetworknodepolicy")

// ManifestPaths is the path to the manifest templates
// bad, but there's no way to pass configuration to the reconciler right now
const (
	PLUGIN_PATH                 = "./bindata/manifests/plugins"
	DAEMON_PATH                 = "./bindata/manifests/daemon"
	WEBHOOK_PATH                = "./bindata/manifests/webhook"
	DEFAULT_POLICY_NAME         = "default"
	CONFIGMAP_NAME              = "device-plugin-config"
	DP_CONFIG_FILENAME          = "config.json"
	SERVICE_CA_CONFIGMAP        = "openshift-service-ca"
	SRIOV_MUTATING_WEBHOOK_NAME = "network-resources-injector-config"
)

var Namespace = os.Getenv("NAMESPACE")

// Add creates a new SriovNetworkNodePolicy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileSriovNetworkNodePolicy{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("sriovnetworknodepolicy-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource SriovNetworkNodePolicy
	err = c.Watch(&source.Kind{Type: &sriovnetworkv1.SriovNetworkNodePolicy{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileSriovNetworkNodePolicy{}

// ReconcileSriovNetworkNodePolicy reconciles a SriovNetworkNodePolicy object
type ReconcileSriovNetworkNodePolicy struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a SriovNetworkNodePolicy object and makes changes based on the state read
// and what is in the SriovNetworkNodePolicy.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileSriovNetworkNodePolicy) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling SriovNetworkNodePolicy")

	defaultPolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: DEFAULT_POLICY_NAME, Namespace: Namespace}, defaultPolicy)
	if err != nil {
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Fetch the SriovNetworkNodePolicyList
	policyList := &sriovnetworkv1.SriovNetworkNodePolicyList{}
	err = r.client.List(context.TODO(), &client.ListOptions{}, policyList)
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
	// Fetch the Nodes
	nodeList := &corev1.NodeList{}
	lo := &client.ListOptions{}
	lbl := make(map[string]string)
	lbl["node-role.kubernetes.io/worker"] = ""
	lo.MatchingLabels(lbl)
	err = r.client.List(context.TODO(), lo, nodeList)
	if err != nil {
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Fail to list nodes")
		return reconcile.Result{}, err
	}

	// Sort the policies with priority, higher priority ones is applied later
	sort.Sort(sriovnetworkv1.ByPriority(policyList.Items))
	// Sync SriovNetworkNodeState objects
	if err = r.syncAllSriovNetworkNodeStates(defaultPolicy, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	// Sync SriovNetworkConfigDaemon objects
	if err = r.syncConfigDaemonSet(defaultPolicy); err != nil {
		return reconcile.Result{}, err
	}
	if len(policyList.Items) > 1 {
		// Sync Sriov device plugin ConfigMap object
		if err = r.syncDevicePluginConfigMap(policyList); err != nil {
			return reconcile.Result{}, err
		}
		// Render and sync Daemon objects
		if err = r.syncPluginDaemonObjs(defaultPolicy, policyList); err != nil {
			return reconcile.Result{}, err
		}
	}
	// Render and sync Webhook objects
	if err = r.syncWebhookObjs(defaultPolicy); err != nil {
		return reconcile.Result{}, err
	}

	// All was successful. Request that this be re-triggered after ResyncPeriod,
	// so we can reconcile state again.
	return reconcile.Result{}, nil
}
func (r *ReconcileSriovNetworkNodePolicy) syncConfigDaemonSet(dp *sriovnetworkv1.SriovNetworkNodePolicy) error {
	logger := log.WithName("syncConfigDaemonset")
	logger.Info("Start to sync config daemonset")
	// var err error
	objs := []*uns.Unstructured{}

	data := render.MakeRenderData()
	data.Data["Image"] = os.Getenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE")
	data.Data["Namespace"] = os.Getenv("NAMESPACE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	objs, err := renderDsForCR(DAEMON_PATH, &data)
	if err != nil {
		logger.Error(err, "Fail to render config daemon manifests")
		return err
	}
	// Sync DaemonSets
	for _, obj := range objs {
		err = r.syncDsObject(dp, nil, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncDevicePluginConfigMap(pl *sriovnetworkv1.SriovNetworkNodePolicyList) error {
	logger := log.WithName("syncDevicePluginConfigMap")
	logger.Info("Start to sync device plugin ConfigMap")

	data, err := renderDevicePluginConfigData(pl)
	if err != nil {
		return err
	}
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CONFIGMAP_NAME,
			Namespace: Namespace,
		},
		Data: data,
	}
	found := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), cm)
			if err != nil {
				return fmt.Errorf("Couldn't create ConfigMap: %v", err)
			}
			logger.Info("Created ConfigMap for", cm.Namespace, cm.Name)
		} else {
			return fmt.Errorf("Failed to get ConfigMap: %v", err)
		}
	} else {
		logger.Info("ConfigMap already exists, updating")
		err = r.client.Update(context.TODO(), cm)
		if err != nil {
			return fmt.Errorf("Couldn't update ConfigMap: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncAllSriovNetworkNodeStates(np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, nl *corev1.NodeList) error {
	logger := log.WithName("syncAllSriovNetworkNodeStates")
	logger.Info("Start to sync all SriovNetworkNodeState custom resource")
	found := &corev1.ConfigMap{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: Namespace, Name: CONFIGMAP_NAME}, found); err != nil {
		logger.Info("Fail to get", "ConfigMap", CONFIGMAP_NAME)
	}
	md5sum := md5.Sum([]byte(found.Data[DP_CONFIG_FILENAME]))
	for _, node := range nl.Items {
		logger.Info("Sync SriovNetworkNodeState CR", "name", node.Name)
		ns := &sriovnetworkv1.SriovNetworkNodeState{}
		ns.Name = node.Name
		ns.Namespace = Namespace
		j, _ := json.Marshal(ns)
		fmt.Printf("SriovNetworkNodeState:\n%s\n\n", j)
		if err := r.syncSriovNetworkNodeState(np, npl, ns, &node, hex.EncodeToString(md5sum[:])); err != nil {
			logger.Error(err, "Fail to sync", "SriovNetworkNodeState", ns.Name)
			return err
		}
	}

	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncSriovNetworkNodeState(np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, ns *sriovnetworkv1.SriovNetworkNodeState, node *corev1.Node, cksum string) error {
	logger := log.WithName("syncSriovNetworkNodeState")
	logger.Info("Start to sync SriovNetworkNodeState", "Name", ns.Name, "cksum", cksum)

	if err := controllerutil.SetControllerReference(np, ns, r.scheme); err != nil {
		return err
	}
	found := &sriovnetworkv1.SriovNetworkNodeState{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: ns.Namespace, Name: ns.Name}, found)
	if err != nil {
		logger.Info("Fail to get SriovNetworkNodeState", "namespace", ns.Namespace, "name", ns.Name)
		if errors.IsNotFound(err) {
			ns.Spec.DpConfigVersion = cksum
			err = r.client.Create(context.TODO(), ns)
			if err != nil {
				return fmt.Errorf("Couldn't create SriovNetworkNodeState: %v", err)
			}
			logger.Info("Created SriovNetworkNodeState for", ns.Namespace, ns.Name)
		} else {
			return fmt.Errorf("Failed to get SriovNetworkNodeState: %v", err)
		}
	} else {
		logger.Info("SriovNetworkNodeState already exists, updating")
		found.Spec = ns.Spec
		for _, p := range npl.Items {
			if p.Name == "default" {
				continue
			}
			if p.Selected(node) {
				fmt.Printf("apply policy %s for node %s\n", p.Name, node.Name)
				p.Apply(found)
			}
		}
		found.Spec.DpConfigVersion = cksum
		err = r.client.Update(context.TODO(), found)
		if err != nil {
			return fmt.Errorf("Couldn't update SriovNetworkNodeState: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncPluginDaemonObjs(dp *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList) error {
	logger := log.WithName("syncPluginDaemonObjs")
	logger.Info("Start to sync sriov daemons objects")
	ns := os.Getenv("NAMESPACE")

	if len(pl.Items) < 2 {
		r.tryDeleteDs(ns, "sriov-device-plugin")
		r.tryDeleteDs(ns, "sriov-cni")
		return nil
	}
	// render RawCNIConfig manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = ns
	data.Data["SRIOVCNIImage"] = os.Getenv("SRIOV_CNI_IMAGE")
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ResourcePrefix"] = os.Getenv("RESOURCE_PREFIX")
	objs, err := renderDsForCR(PLUGIN_PATH, &data)
	if err != nil {
		logger.Error(err, "Fail to render SR-IoV manifests")
		return err
	}
	// Sync DaemonSets
	for _, obj := range objs {
		err = r.syncDsObject(dp, pl, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncWebhookObjs(dp *sriovnetworkv1.SriovNetworkNodePolicy) error {
	logger := log.WithName("syncWebhookObjs")
	logger.Info("Start to sync webhook objects")

	enable := os.Getenv("ENABLE_ADMISSION_CONTROLLER")
	if enable != "true" {
		logger.Info("SR-IOV Admission Controller is disabled, existing.")
		logger.Info("To enable SR-IOV Admission Controller, set 'ENABLE_ADMISSION_CONTROLLER' to 'true'.")
		return nil
	}

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
	// Sync Webhook
	for _, obj := range objs {
		err = r.syncWebhookObject(dp, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync webhook objects")
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) tryDeleteDs(namespace, name string) error {
	logger := log.WithName("tryDeleteDsObject")
	ds := &appsv1.DaemonSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: name}, ds)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		} else {
			logger.Error(err, "Fail to get DaemonSet", "Namespace", namespace, "Name", name)
			return err
		}
	} else {
		err = r.client.Delete(context.TODO(), ds)
		if err != nil {
			logger.Error(err, "Fail to delete DaemonSet", "Namespace", namespace, "Name", name)
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncDsObject(dp *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList, obj *uns.Unstructured) error {
	var err error
	logger := log.WithName("syncDsObject")
	logger.Info("Start to sync Objects")
	scheme := kscheme.Scheme
	switch kind := obj.GetKind(); kind {
	case "ServiceAccount":
		sa := &corev1.ServiceAccount{}
		err = scheme.Convert(obj, sa, nil)
		r.syncServiceAccount(dp, sa)
		if err != nil {
			logger.Error(err, "Fail to sync ServiceAccount")
			return err
		}
	case "DaemonSet":
		ds := &appsv1.DaemonSet{}
		err = scheme.Convert(obj, ds, nil)
		r.syncDaemonSet(dp, pl, ds)
		if err != nil {
			logger.Error(err, "Fail to sync DaemonSet", "Namespace", ds.Namespace, "Name", ds.Name)
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncWebhookObject(dp *sriovnetworkv1.SriovNetworkNodePolicy, obj *uns.Unstructured) error {
	var err error
	logger := log.WithName("syncWebhookObject")
	logger.Info("Start to sync Objects")
	scheme := kscheme.Scheme
	switch kind := obj.GetKind(); kind {
	case "MutatingWebhookConfiguration":
		whs := &admissionregistrationv1beta1.MutatingWebhookConfiguration{}
		err = scheme.Convert(obj, whs, nil)
		r.syncWebhook(dp, whs)
		if err != nil {
			logger.Error(err, "Fail to sync mutate webhook")
			return err
		}
	case "ConfigMap":
		cm := &corev1.ConfigMap{}
		err = scheme.Convert(obj, cm, nil)
		r.syncWebhookConfigMap(dp, cm)
		if err != nil {
			logger.Error(err, "Fail to sync webhook config map")
			return err
		}
	case "ServiceAccount":
		sa := &corev1.ServiceAccount{}
		err = scheme.Convert(obj, sa, nil)
		r.syncServiceAccount(dp, sa)
		if err != nil {
			logger.Error(err, "Fail to sync ServiceAccount")
			return err
		}
	case "DaemonSet":
		ds := &appsv1.DaemonSet{}
		err = scheme.Convert(obj, ds, nil)
		r.syncWebhookDaemonSet(dp, ds)
		if err != nil {
			logger.Error(err, "Fail to sync DaemonSet", "Namespace", ds.Namespace, "Name", ds.Name)
			return err
		}
	case "Service":
		s := &corev1.Service{}
		err = scheme.Convert(obj, s, nil)
		r.syncService(dp, s)
		if err != nil {
			logger.Error(err, "Fail to sync Service", "Namespace", s.Namespace, "Name", s.Name)
			return err
		}
	case "Secret":
		s := &corev1.Secret{}
		err = scheme.Convert(obj, s, nil)
		r.syncSecret(dp, s)
		if err != nil {
			logger.Error(err, "Fail to sync Secret", "Namespace", s.Namespace, "Name", s.Name)
			return err
		}
	case "ClusterRole":
		cr := &rbacv1.ClusterRole{}
		err = scheme.Convert(obj, cr, nil)
		r.syncClusterRole(dp, cr)
		if err != nil {
			logger.Error(err, "Fail to sync cluster role", "Namespace", cr.Namespace, "Name", cr.Name)
			return err
		}
	case "ClusterRoleBinding":
		crb := &rbacv1.ClusterRoleBinding{}
		err = scheme.Convert(obj, crb, nil)
		r.syncClusterRoleBinding(dp, crb)
		if err != nil {
			logger.Error(err, "Fail to sync cluster role binding", "Namespace", crb.Namespace, "Name", crb.Name)
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncWebhook(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *admissionregistrationv1beta1.MutatingWebhookConfiguration) error {
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
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncWebhookConfigMap(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *corev1.ConfigMap) error {
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
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncService(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *corev1.Service) error {
	logger := log.WithName("syncService")
	logger.Info("Start to sync service", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	s := &corev1.Service{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, s)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create service: %v", err)
			}
			logger.Info("Create service for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get service: %v", err)
		}
	} else {
		logger.Info("Service already exists, updating")
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			return fmt.Errorf("Couldn't update service: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncSecret(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *corev1.Secret) error {
	logger := log.WithName("syncSecret")
	logger.Info("Start to sync secret", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	s := &corev1.Secret{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, s)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create secret: %v", err)
			}
			logger.Info("Create secret for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get secret: %v", err)
		}
	} else {
		logger.Info("Secret already exists, updating")
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			return fmt.Errorf("Couldn't update secret: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncClusterRole(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *rbacv1.ClusterRole) error {
	logger := log.WithName("syncClusterRole")
	logger.Info("Start to sync cluster role", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	clusterRole := &rbacv1.ClusterRole{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, clusterRole)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create cluster role: %v", err)
			}
			logger.Info("Create cluster role for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get cluster role: %v", err)
		}
	} else {
		logger.Info("Cluster role already exists, updating")
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			return fmt.Errorf("Couldn't update cluster role: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncClusterRoleBinding(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *rbacv1.ClusterRoleBinding) error {
	logger := log.WithName("syncClusterRoleBinding")
	logger.Info("Start to sync cluster role binding", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, clusterRoleBinding)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create cluster role binding: %v", err)
			}
			logger.Info("Create cluster role binding for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get cluster role binding: %v", err)
		}
	} else {
		logger.Info("Cluster role binding already exists, updating")
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			return fmt.Errorf("Couldn't update cluster role binding: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncWebhookDaemonSet(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *appsv1.DaemonSet) error {
	logger := log.WithName("syncWebhookDaemonSet")
	logger.Info("Start to sync webhook daemon", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	ds := &appsv1.DaemonSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, ds)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create webhook daemon: %v", err)
			}
			logger.Info("Create webhook daemon for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to webhook daemon: %v", err)
		}
	} else {
		logger.Info("Webhook daemon already exists, updating")
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			return fmt.Errorf("Couldn't update webhook daemon: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncServiceAccount(cr *sriovnetworkv1.SriovNetworkNodePolicy, in *corev1.ServiceAccount) error {
	logger := log.WithName("syncServiceAccount")
	logger.Info("Start to sync ServiceAccount", "Name", in.Name, "Namespace", in.Namespace)

	if err := controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	sa := &corev1.ServiceAccount{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, sa)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				return fmt.Errorf("Couldn't create ServiceAccount: %v", err)
			}
			logger.Info("Create ServiceAccount for", in.Namespace, in.Name)
		} else {
			return fmt.Errorf("Fail to get ServiceAccount: %v", err)
		}
	} else {
		logger.Info("ServiceAccount already exists, updating")
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			return fmt.Errorf("Couldn't update ServiceAccount: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncDaemonSet(cr *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList, in *appsv1.DaemonSet) error {
	logger := log.WithName("syncDaemonSet")
	logger.Info("Start to sync DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
	var err error

	if pl != nil {
		if err = setDsNodeAffinity(pl, in); err != nil {
			return err
		}
	}
	if err = controllerutil.SetControllerReference(cr, in, r.scheme); err != nil {
		return err
	}
	ds := &appsv1.DaemonSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, ds)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Created DaemonSet", in.Namespace, in.Name)
			err = r.client.Create(context.TODO(), in)
			if err != nil {
				logger.Error(err, "Fail to create Daemonset", "Namespace", in.Namespace, "Name", in.Name)
				return err
			}
		} else {
			logger.Error(err, "Fail to get Daemonset", "Namespace", in.Namespace, "Name", in.Name)
			return err
		}
	} else {
		logger.Info("DaemonSet already exists, updating")
		if ds.Spec.Template.Spec.Affinity != nil && in.Spec.Template.Spec.Affinity != nil {
			if !reflect.DeepEqual(*ds.Spec.Template.Spec.Affinity.NodeAffinity, *in.Spec.Template.Spec.Affinity.NodeAffinity) {
				ds.Spec.Template.Spec.Affinity.NodeAffinity = in.Spec.Template.Spec.Affinity.NodeAffinity
			}
		}
		err = r.client.Update(context.TODO(), ds)
		if err != nil {
			logger.Error(err, "Fail to update DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
			return err
		}
	}
	return nil
}

func setDsNodeAffinity(pl *sriovnetworkv1.SriovNetworkNodePolicyList, ds *appsv1.DaemonSet) error {
	terms := []corev1.NodeSelectorTerm{}
	for _, p := range pl.Items {
		nodeSelector := corev1.NodeSelectorTerm{}
		if len(p.Spec.NodeSelector) == 0 {
			continue
		}
		for k, v := range p.Spec.NodeSelector {
			expressions := []corev1.NodeSelectorRequirement{}
			exp := corev1.NodeSelectorRequirement{
				Operator: corev1.NodeSelectorOpIn,
				Key:      k,
				Values:   []string{v},
			}
			expressions = append(expressions, exp)
			nodeSelector = corev1.NodeSelectorTerm{
				MatchExpressions: expressions,
			}
		}
		terms = append(terms, nodeSelector)
	}

	if len(terms) > 0 {
		ds.Spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: terms,
				},
			},
		}
	}
	return nil
}

// renderDsForCR returns a busybox pod with the same name/namespace as the cr
func renderDsForCR(path string, data *render.RenderData) ([]*uns.Unstructured, error) {
	logger := log.WithName("renderDsForCR")
	logger.Info("Start to render objects")
	var err error
	objs := []*uns.Unstructured{}

	objs, err = render.RenderDir(path, data)
	if err != nil {
		return nil, errs.Wrap(err, "failed to render OpenShiftSRIOV Network manifests")
	}
	return objs, nil
}

func renderDevicePluginConfigData(pl *sriovnetworkv1.SriovNetworkNodePolicyList) (map[string]string, error) {
	data := make(map[string]string)
	rcl := &dptypes.ResourceConfList{}
	for _, p := range pl.Items {
		if p.Name == "default" {
			continue
		}

		found, i := resourceNameInList(p.Spec.ResourceName, rcl)
		if found {
			if p.Spec.NicSelector.Vendor != "" && !sriovnetworkv1.StringInArray(p.Spec.NicSelector.Vendor, rcl.ResourceList[i].Selectors.Vendors) {
				rcl.ResourceList[i].Selectors.Vendors = append(rcl.ResourceList[i].Selectors.Vendors, p.Spec.NicSelector.Vendor)
			}
			if p.Spec.NicSelector.DeviceID != "" && !sriovnetworkv1.StringInArray(p.Spec.NicSelector.DeviceID, rcl.ResourceList[i].Selectors.Devices) {
				rcl.ResourceList[i].Selectors.Devices = append(rcl.ResourceList[i].Selectors.Devices, p.Spec.NicSelector.DeviceID)
			}
			if len(p.Spec.NicSelector.PfNames) > 0 {
				rcl.ResourceList[i].Selectors.PfNames = sriovnetworkv1.UniqueAppend(rcl.ResourceList[i].Selectors.PfNames, p.Spec.NicSelector.PfNames...)
			}
			if p.Spec.DeviceType == "vfio-pci" {
				rcl.ResourceList[i].Selectors.Drivers = sriovnetworkv1.UniqueAppend(rcl.ResourceList[i].Selectors.Drivers, p.Spec.DeviceType)
			} else {
				if p.Spec.NumVfs > 0 {
					///////////////////////////////////
					// TODO: remove unsupport VF driver
					///////////////////////////////////
					rcl.ResourceList[i].Selectors.Drivers = sriovnetworkv1.UniqueAppend(rcl.ResourceList[i].Selectors.Drivers, "iavf", "mlx5_core", "ixgbevf", "i40evf")
				}
			}
			fmt.Printf("Update ResourcList = %v\n", rcl.ResourceList)
		} else {
			rc := &dptypes.ResourceConfig{
				ResourceName: p.Spec.ResourceName,
				IsRdma:       p.Spec.IsRdma,
			}
			if p.Spec.NicSelector.Vendor != "" {
				rc.Selectors.Vendors = append(rc.Selectors.Vendors, p.Spec.NicSelector.Vendor)
			}
			if p.Spec.NicSelector.DeviceID != "" {
				rc.Selectors.Devices = append(rc.Selectors.Devices, p.Spec.NicSelector.DeviceID)
			}
			if l := len(p.Spec.NicSelector.PfNames); l > 0 {
				rc.Selectors.PfNames = append(rc.Selectors.PfNames, p.Spec.NicSelector.PfNames...)
			}

			if p.Spec.DeviceType == "vfio-pci" {
				rc.Selectors.Drivers = append(rc.Selectors.Drivers, p.Spec.DeviceType)
			} else {
				if p.Spec.NumVfs > 0 {
					///////////////////////////////////
					// TODO: remove unsupport VF driver
					///////////////////////////////////
					rc.Selectors.Drivers = append(rc.Selectors.Drivers, "iavf", "mlx5_core", "i40evf", "ixgbevf")
				}
			}
			rcl.ResourceList = append(rcl.ResourceList, *rc)
			fmt.Printf("Add ResourcList = %v\n", rcl.ResourceList)
		}

	}
	config, err := json.Marshal(rcl)
	if err != nil {
		return nil, err
	}
	data[DP_CONFIG_FILENAME] = string(config)
	return data, nil
}

func resourceNameInList(name string, rcl *dptypes.ResourceConfList) (bool, int) {
	for i, rc := range rcl.ResourceList {
		if rc.ResourceName == name {
			return true, i
		}
	}
	return false, 0
}
