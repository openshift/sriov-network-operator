package sriovacceleratornodepolicy

import (
	"context"

	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"time"

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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	"github.com/openshift/sriov-network-operator/pkg/apply"
	"github.com/openshift/sriov-network-operator/pkg/controller/sriovoperatorconfig"
	render "github.com/openshift/sriov-network-operator/pkg/render"
)

var log = logf.Log.WithName("controller_sriovacceleratornodepolicy")

// ManifestPaths is the path to the manifest templates
// bad, but there's no way to pass configuration to the reconciler right now
const (
	ResyncPeriod        = 5 * time.Minute
	PLUGIN_PATH         = "./bindata/manifests/plugins/accelerator"
	DAEMON_PATH         = "./bindata/manifests/daemon"
	DEFAULT_POLICY_NAME = "default"
	CONFIGMAP_NAME      = "accelerator-device-plugin-config"
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
	return &ReconcileSriovAcceleratorNodePolicy{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("sriovacceleratornodepolicy-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource SriovAcceleratorNodePolicy
	err = c.Watch(&source.Kind{Type: &sriovnetworkv1.SriovAcceleratorNodePolicy{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource DaemonSet
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &sriovnetworkv1.SriovAcceleratorNodePolicy{},
	})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &sriovnetworkv1.SriovOperatorConfig{},
	})
	if err != nil {
		return err
	}
	return nil
}

var _ reconcile.Reconciler = &ReconcileSriovAcceleratorNodePolicy{}

// ReconcileSriovNetworkNodePolicy reconciles a SriovNetworkNodePolicy object
type ReconcileSriovAcceleratorNodePolicy struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a SriovAcceleratorNodePolicy object and makes changes based on the state read
// and what is in the SriovAcceleratorNodePolicy.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileSriovAcceleratorNodePolicy) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling SriovAcceleratorNodePolicy")

	defaultPolicy := &sriovnetworkv1.SriovAcceleratorNodePolicy{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: DEFAULT_POLICY_NAME, Namespace: Namespace}, defaultPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// Default policy object not found, create it.
			defaultPolicy.SetNamespace(Namespace)
			defaultPolicy.SetName(DEFAULT_POLICY_NAME)
			defaultPolicy.Spec = sriovnetworkv1.SriovAcceleratorNodePolicySpec{
				NumVfs:        0,
				NodeSelector:  make(map[string]string),
				AccelSelector: sriovnetworkv1.SriovAcceleratorNicSelector{},
				Config:        "",
			}
			err = r.client.Create(context.TODO(), defaultPolicy)
			if err != nil {
				reqLogger.Error(err, "Failed to create default accelerator policy", "Namespace", Namespace, "Name", DEFAULT_POLICY_NAME)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Fetch the SriovAcceleratorNodePolicyList
	policyList := &sriovnetworkv1.SriovAcceleratorNodePolicyList{}
	err = r.client.List(context.TODO(), policyList, &client.ListOptions{})
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
	lo := &client.MatchingLabels{}
	defaultOpConf := &sriovnetworkv1.SriovOperatorConfig{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: Namespace, Name: sriovoperatorconfig.DEFAULT_CONFIG_NAME}, defaultOpConf); err != nil {
		return reconcile.Result{}, err
	}
	if len(defaultOpConf.Spec.ConfigDaemonNodeSelector) > 0 {
		labels := client.MatchingLabels(defaultOpConf.Spec.ConfigDaemonNodeSelector)
		lo = &labels

	} else {
		lo = &client.MatchingLabels{
			"node-role.kubernetes.io/worker": "",
			"beta.kubernetes.io/os":          "linux",
		}
	}
	err = r.client.List(context.TODO(), nodeList, lo)
	if err != nil {
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Fail to list nodes")
		return reconcile.Result{}, err
	}

	// Sort the policies with priority, higher priority ones is applied later
	sort.Sort(sriovnetworkv1.AcceleratorPolicyByPriority(policyList.Items))
	// Sync Sriov device plugin ConfigMap object
	if err = r.syncDevicePluginConfigMap(policyList); err != nil {
		return reconcile.Result{}, err
	}
	// Render and sync Daemon objects
	if err = r.syncPluginDaemonObjs(defaultPolicy, policyList); err != nil {
		return reconcile.Result{}, err
	}
	// Sync SriovNetworkNodeState objects
	if err = r.syncAllSriovAcceleratorNodeStates(defaultPolicy, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}

	// All was successful. Request that this be re-triggered after ResyncPeriod,
	// so we can reconcile state again.
	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}

func (r *ReconcileSriovAcceleratorNodePolicy) syncDevicePluginConfigMap(pl *sriovnetworkv1.SriovAcceleratorNodePolicyList) error {
	logger := log.WithName("syncDevicePluginConfigMap")
	logger.Info("Start to sync device plugin ConfigMap")

	data, err := renderDevicePluginConfigData(pl)
	if err != nil {
		return err
	}
	config, err := json.Marshal(data)
	if err != nil {
		return err
	}
	configData := make(map[string]string)
	configData[DP_CONFIG_FILENAME] = string(config)

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CONFIGMAP_NAME,
			Namespace: Namespace,
		},
		Data: configData,
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
		currentConfig := dptypes.ResourceConfList{}
		err = json.Unmarshal([]byte(string(found.Data[DP_CONFIG_FILENAME])), &currentConfig)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(currentConfig, data) {
			logger.Info("No content change, skip update")
			return nil
		}
		err = r.client.Update(context.TODO(), cm)
		if err != nil {
			return fmt.Errorf("Couldn't update ConfigMap: %v", err)
		}
	}
	return nil
}

func (r *ReconcileSriovAcceleratorNodePolicy) syncAllSriovAcceleratorNodeStates(np *sriovnetworkv1.SriovAcceleratorNodePolicy, npl *sriovnetworkv1.SriovAcceleratorNodePolicyList, nl *corev1.NodeList) error {
	logger := log.WithName("syncAllSriovAcceleratorNodeStates")
	logger.Info("Start to sync all syncAllSriovAcceleratorNodeStates custom resource")
	found := &corev1.ConfigMap{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: Namespace, Name: CONFIGMAP_NAME}, found); err != nil {
		logger.Info("Fail to get", "ConfigMap", CONFIGMAP_NAME)
	}
	for _, node := range nl.Items {
		logger.Info("Sync SriovNetworkNodeState CR", "name", node.Name)
		ns := &sriovnetworkv1.SriovAcceleratorNodeState{}
		ns.Name = node.Name
		ns.Namespace = Namespace
		j, _ := json.Marshal(ns)
		logger.Info("SriovAcceleratorNodeState CR", "content", j)
		if err := r.syncSriovAcceleratorNodeState(np, npl, ns, &node, found.GetResourceVersion()); err != nil {
			logger.Error(err, "Fail to sync", "SriovNetworkNodeState", ns.Name)
			return err
		}
	}
	logger.Info("Remove SriovAcceleratorNodeState custom resource for unselected node")
	nsList := &sriovnetworkv1.SriovNetworkNodeStateList{}
	err := r.client.List(context.TODO(), nsList, &client.ListOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Info("Fail to list SriovAcceleratorNodeState CRs")
			return err
		}
	} else {
		for _, ns := range nsList.Items {
			found := false
			for _, node := range nl.Items {
				logger.Info("validate", "SriovAcceleratorNodeState", ns.GetName(), "node", node.GetName())
				if ns.GetName() == node.GetName() {
					found = true
					break
				}
			}
			if !found {
				err := r.client.Delete(context.TODO(), &ns, &client.DeleteOptions{})
				if err != nil {
					logger.Info("Fail to Delete", "SriovAcceleratorNodeState CR:", ns.GetName())
					return err
				}
			}
		}
	}
	return nil
}

func (r *ReconcileSriovAcceleratorNodePolicy) syncSriovAcceleratorNodeState(np *sriovnetworkv1.SriovAcceleratorNodePolicy, npl *sriovnetworkv1.SriovAcceleratorNodePolicyList, ns *sriovnetworkv1.SriovAcceleratorNodeState, node *corev1.Node, cksum string) error {
	logger := log.WithName("syncSriovAcceleratorNodeState")
	logger.Info("Start to sync SriovAcceleratorNodeState", "Name", ns.Name, "cksum", cksum)

	if err := controllerutil.SetControllerReference(np, ns, r.scheme); err != nil {
		return err
	}
	found := &sriovnetworkv1.SriovAcceleratorNodeState{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: ns.Namespace, Name: ns.Name}, found)
	if err != nil {
		logger.Info("Fail to get SriovAcceleratorNodeState", "namespace", ns.Namespace, "name", ns.Name)
		if errors.IsNotFound(err) {
			ns.Spec.DpConfigVersion = cksum
			err = r.client.Create(context.TODO(), ns)
			if err != nil {
				return fmt.Errorf("Couldn't create SriovNetworkNodeState: %v", err)
			}
			logger.Info("Created SriovAcceleratorNodeState for", ns.Namespace, ns.Name)
		} else {
			return fmt.Errorf("Failed to get SriovNetworkNodeState: %v", err)
		}
	} else {
		logger.Info("SriovAcceleratorNodeState already exists, updating")
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

func (r *ReconcileSriovAcceleratorNodePolicy) syncPluginDaemonObjs(dp *sriovnetworkv1.SriovAcceleratorNodePolicy, pl *sriovnetworkv1.SriovAcceleratorNodePolicyList) error {
	logger := log.WithName("syncPluginDaemonObjs")
	logger.Info("Start to sync sriov accelerator daemons objects")

	if len(pl.Items) < 2 {
		r.tryDeleteDsPods(Namespace, "sriov-accelerator-device-plugin")
		return nil
	}
	// render RawCNIConfig manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = Namespace
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ResourcePrefix"] = os.Getenv("RESOURCE_PREFIX")

	objs, err := renderDsForCR(PLUGIN_PATH, &data)
	if err != nil {
		logger.Error(err, "Fail to render SR-IoV manifests")
		return err
	}

	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err = r.client.Get(context.TODO(), types.NamespacedName{
		Name: sriovoperatorconfig.DEFAULT_CONFIG_NAME, Namespace: Namespace}, defaultConfig)
	if err != nil {
		return err
	}
	// Sync DaemonSets
	for _, obj := range objs {
		if obj.GetKind() == "DaemonSet" && len(defaultConfig.Spec.ConfigDaemonNodeSelector) > 0 {
			scheme := kscheme.Scheme
			ds := &appsv1.DaemonSet{}
			err = scheme.Convert(obj, ds, nil)
			if err != nil {
				logger.Error(err, "Fail to convert to DaemonSet")
				return err
			}
			ds.Spec.Template.Spec.NodeSelector = defaultConfig.Spec.ConfigDaemonNodeSelector
			err = scheme.Convert(ds, obj, nil)
			if err != nil {
				logger.Error(err, "Fail to convert to Unstructured")
				return err
			}
		}
		err = r.syncDsObject(dp, pl, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync accelerator SR-IOV daemons objects")
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovAcceleratorNodePolicy) tryDeleteDsPods(namespace, name string) error {
	logger := log.WithName("tryDeleteDsPods")
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
		ds.Spec.Template.Spec.NodeSelector = map[string]string{"beta.kubernetes.io/os": "none"}
		ds.Spec.Template.Spec.Affinity = &corev1.Affinity{}
		err = r.client.Update(context.TODO(), ds)
		if err != nil {
			logger.Error(err, "Fail to update DaemonSet", "Namespace", namespace, "Name", name)
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovAcceleratorNodePolicy) syncDsObject(dp *sriovnetworkv1.SriovAcceleratorNodePolicy, pl *sriovnetworkv1.SriovAcceleratorNodePolicyList, obj *uns.Unstructured) error {
	logger := log.WithName("syncDsObject")
	kind := obj.GetKind()
	logger.Info("Start to sync Objects", "Kind", kind)
	switch kind {
	case "ServiceAccount", "Role", "RoleBinding":
		if err := controllerutil.SetControllerReference(dp, obj, r.scheme); err != nil {
			return err
		}
		if err := apply.ApplyObject(context.TODO(), r.client, obj); err != nil {
			logger.Error(err, "Fail to sync", "Kind", kind)
			return err
		}
	case "DaemonSet":
		ds := &appsv1.DaemonSet{}
		err := kscheme.Scheme.Convert(obj, ds, nil)
		r.syncDaemonSet(dp, pl, ds)
		if err != nil {
			logger.Error(err, "Fail to sync accelerator DaemonSet", "Namespace", ds.Namespace, "Name", ds.Name)
			return err
		}
	}
	return nil
}

func (r *ReconcileSriovAcceleratorNodePolicy) syncDaemonSet(cr *sriovnetworkv1.SriovAcceleratorNodePolicy, pl *sriovnetworkv1.SriovAcceleratorNodePolicyList, in *appsv1.DaemonSet) error {
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
		err = r.client.Update(context.TODO(), in)
		if err != nil {
			logger.Error(err, "Fail to update DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
			return err
		}
	}
	return nil
}

func setDsNodeAffinity(pl *sriovnetworkv1.SriovAcceleratorNodePolicyList, ds *appsv1.DaemonSet) error {
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

func renderDevicePluginConfigData(pl *sriovnetworkv1.SriovAcceleratorNodePolicyList) (dptypes.ResourceConfList, error) {
	logger := log.WithName("renderDevicePluginConfigData")
	logger.Info("Start to render device plugin config data")
	rcl := dptypes.ResourceConfList{}
	for _, p := range pl.Items {
		if p.Name == "default" {
			continue
		}

		found, i := resourceNameInList(p.Spec.ResourceName, &rcl)
		netDeviceSelectors := dptypes.AccelDeviceSelectors{}
		if found {
			if p.Spec.AccelSelector.Vendor != "" && !sriovnetworkv1.StringInArray(p.Spec.AccelSelector.Vendor, netDeviceSelectors.Vendors) {
				netDeviceSelectors.Vendors = append(netDeviceSelectors.Vendors, p.Spec.AccelSelector.Vendor)
			}
			if p.Spec.AccelSelector.DeviceID != "" {
				var deviceID string
				if p.Spec.NumVfs == 0 {
					deviceID = p.Spec.AccelSelector.DeviceID
				} else {
					deviceID = sriovnetworkv1.AcceleratorSriovPfVfMap[p.Spec.AccelSelector.DeviceID]
				}

				if !sriovnetworkv1.StringInArray(p.Spec.AccelSelector.DeviceID, netDeviceSelectors.Devices) {
					netDeviceSelectors.Devices = append(netDeviceSelectors.Devices, deviceID)
				}
			}

			if len(p.Spec.AccelSelector.RootDevices) > 0 {
				netDeviceSelectors.PciAddresses = sriovnetworkv1.UniqueAppend(netDeviceSelectors.PciAddresses, p.Spec.AccelSelector.RootDevices...)
			}

			netDeviceSelectorsMarshal, err := json.Marshal(netDeviceSelectors)
			if err != nil {
				return rcl, err
			}
			rawNetDeviceSelectors := json.RawMessage(netDeviceSelectorsMarshal)
			rcl.ResourceList[i].Selectors = &rawNetDeviceSelectors
			rcl.ResourceList[i].DeviceType = "accelerator"
			logger.Info("Update accelerator resource", "Resource", rcl.ResourceList[i])
		} else {
			rc := &dptypes.ResourceConfig{
				ResourceName: p.Spec.ResourceName,
			}

			if p.Spec.AccelSelector.Vendor != "" {
				netDeviceSelectors.Vendors = append(netDeviceSelectors.Vendors, p.Spec.AccelSelector.Vendor)
			}
			if p.Spec.AccelSelector.DeviceID != "" {
				if p.Spec.NumVfs == 0 {
					netDeviceSelectors.Devices = append(netDeviceSelectors.Devices, p.Spec.AccelSelector.DeviceID)
				} else {
					netDeviceSelectors.Devices = append(netDeviceSelectors.Devices, sriovnetworkv1.AcceleratorSriovPfVfMap[p.Spec.AccelSelector.DeviceID])
				}
			}

			if l := len(p.Spec.AccelSelector.RootDevices); l > 0 {
				netDeviceSelectors.PciAddresses = append(netDeviceSelectors.PciAddresses, p.Spec.AccelSelector.RootDevices...)
			}

			if len(netDeviceSelectors.Devices) > 0 ||
				len(netDeviceSelectors.Vendors) > 0 ||
				len(netDeviceSelectors.PciAddresses) > 0 {

				netDeviceSelectors.Drivers = append(netDeviceSelectors.Drivers, "vfio-pci")
			}

			netDeviceSelectorsMarshal, err := json.Marshal(netDeviceSelectors)
			if err != nil {
				return rcl, err
			}
			rawNetDeviceSelectors := json.RawMessage(netDeviceSelectorsMarshal)
			rc.Selectors = &rawNetDeviceSelectors
			rc.DeviceType = "accelerator"
			rcl.ResourceList = append(rcl.ResourceList, *rc)
			logger.Info("Add accelerator resource", "Resource", *rc, "Resource list", rcl.ResourceList)
		}
	}
	return rcl, nil
}

func resourceNameInList(name string, rcl *dptypes.ResourceConfList) (bool, int) {
	for i, rc := range rcl.ResourceList {
		if rc.ResourceName == name {
			return true, i
		}
	}
	return false, 0
}
