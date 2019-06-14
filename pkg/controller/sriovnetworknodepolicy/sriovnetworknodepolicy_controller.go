package sriovnetworknodepolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	render "github.com/openshift/sriov-network-operator/pkg/render"

	dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	errs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	PLUGIN_PATH         = "./bindata/manifests/plugins"
	DEFAULT_POLICY_NAME = "default"
	CONFIGMAP_NAME      = "device-plugin-config"
	DP_CONFIG_FILENAME  = "config.json"
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

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner SriovNetworkNodePolicy
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &sriovnetworkv1.SriovNetworkNodePolicy{},
	})
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
	// Sync Sriov device plugin ConfigMap object
	if err = r.syncDevicePluginConfigMap(policyList); err != nil {
		return reconcile.Result{}, err
	}
	// Sync SriovNetworkNodeState objects
	if err = r.syncAllSriovNetworkNodeStates(defaultPolicy, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	// Sync SriovNetworkConfigDaemon objects
	if err = r.syncConfigDaemonSet(defaultPolicy); err != nil {
		return reconcile.Result{}, err
	}
	// Render and sync Daemon objects
	if err = r.syncPluginDaemonObjs(defaultPolicy, policyList); err != nil {
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
	objs, err := renderDsForCR("./bindata/manifests/daemon", &data)
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
		logger.Error(err, "Fail to get", "ConfigMap", CONFIGMAP_NAME)
		return err
	}
	rv := found.GetResourceVersion()
	for _, node := range nl.Items {
		logger.Info("Sync SriovNetworkNodeState CR", "name", node.Name)
		ns := &sriovnetworkv1.SriovNetworkNodeState{}
		ns.Name = node.Name
		ns.Namespace = Namespace
		j, _ := json.Marshal(ns)
		fmt.Printf("SriovNetworkNodeState:\n%s\n\n", j)
		if err := r.syncSriovNetworkNodeState(np, npl, ns, &node, rv); err != nil {
			logger.Error(err, "Fail to sync", "SriovNetworkNodeState", ns.Name)
			return err
		}
	}

	return nil
}

func (r *ReconcileSriovNetworkNodePolicy) syncSriovNetworkNodeState(np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, ns *sriovnetworkv1.SriovNetworkNodeState, node *corev1.Node, cmrv string) error {
	logger := log.WithName("syncSriovNetworkNodeState")
	logger.Info("Start to sync SriovNetworkNodeState", "Name", ns.Name)

	if err := controllerutil.SetControllerReference(np, ns, r.scheme); err != nil {
		return err
	}
	found := &sriovnetworkv1.SriovNetworkNodeState{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: ns.Namespace, Name: ns.Name}, found)
	if err != nil {
		logger.Info("Fail to get SriovNetworkNodeState", "namespace", ns.Namespace, "name", ns.Name)
		if errors.IsNotFound(err) {
			ns.SetAnnotations(map[string]string{"devicePluginConfigMapResourcVersion": cmrv})
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
		found.SetAnnotations(map[string]string{"devicePluginConfigMapResourcVersion": cmrv})
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
		
	// render RawCNIConfig manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = os.Getenv("NAMESPACE")
	data.Data["SRIOVCNIImage"] = os.Getenv("SRIOV_CNI_IMAGE")
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
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
				logger.Info("DaemonSet not found", "Namespace", in.Namespace, "Name", in.Name)
				return fmt.Errorf("Couldn't create DaemonSet: %v", err)
			}
		} else {
			return fmt.Errorf("Failed to get DaemonSet: %v", err)
		}
	} else {
		logger.Info("DaemonSet already exists, updating")
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			return fmt.Errorf("Couldn't update DaemonSet: %v", err)
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

	ds.Spec.Template.Spec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: terms,
			},
		},
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
