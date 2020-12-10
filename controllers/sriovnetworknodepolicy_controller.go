/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/go-logr/logr"
	dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	errs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apply"
	render "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
)

// SriovNetworkNodePolicyReconciler reconciles a SriovNetworkNodePolicy object
type SriovNetworkNodePolicyReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

var ctlrlogger = logf.Log.WithName("SriovNetworkNodePolicyController")

// +kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworknodepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworknodepolicies/status,verbs=get;update;patch

func (r *SriovNetworkNodePolicyReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	reqLogger := r.Log.WithValues("sriovnetworknodepolicy", req.NamespacedName)

	reqLogger.Info("Reconciling")

	defaultPolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
	err := r.Get(context.TODO(), types.NamespacedName{Name: DEFAULT_POLICY_NAME, Namespace: namespace}, defaultPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// Default policy object not found, create it.
			defaultPolicy.SetNamespace(namespace)
			defaultPolicy.SetName(DEFAULT_POLICY_NAME)
			defaultPolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
				NumVfs:       0,
				NodeSelector: make(map[string]string),
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
			}
			err = r.Create(context.TODO(), defaultPolicy)
			if err != nil {
				reqLogger.Error(err, "Failed to create default Policy", "Namespace", namespace, "Name", DEFAULT_POLICY_NAME)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Fetch the SriovNetworkNodePolicyList
	policyList := &sriovnetworkv1.SriovNetworkNodePolicyList{}
	err = r.List(context.TODO(), policyList, &client.ListOptions{})
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
	if err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: DEFAULT_CONFIG_NAME}, defaultOpConf); err != nil {
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
	err = r.List(context.TODO(), nodeList, lo)
	if err != nil {
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Fail to list nodes")
		return reconcile.Result{}, err
	}

	// Sort the policies with priority, higher priority ones is applied later
	sort.Sort(sriovnetworkv1.ByPriority(policyList.Items))
	// Sync Sriov device plugin ConfigMap object
	if err = r.syncDevicePluginConfigMap(policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	// Render and sync Daemon objects
	if err = r.syncPluginDaemonObjs(defaultPolicy, policyList); err != nil {
		return reconcile.Result{}, err
	}
	// Sync SriovNetworkNodeState objects
	if err = r.syncAllSriovNetworkNodeStates(defaultPolicy, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}

	// All was successful. Request that this be re-triggered after ResyncPeriod,
	// so we can reconcile state again.
	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}

func (r *SriovNetworkNodePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sriovnetworkv1.SriovNetworkNodePolicy{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&sriovnetworkv1.SriovNetworkNodeState{}).
		Complete(r)
}

func (r *SriovNetworkNodePolicyReconciler) syncDevicePluginConfigMap(pl *sriovnetworkv1.SriovNetworkNodePolicyList, nl *corev1.NodeList) error {
	logger := r.Log.WithName("syncDevicePluginConfigMap")
	logger.Info("Start to sync device plugin ConfigMap")

	configData := make(map[string]string)
	for _, node := range nl.Items {
		data, err := r.renderDevicePluginConfigData(pl, &node)
		if err != nil {
			return err
		}
		config, err := json.Marshal(data)
		if err != nil {
			return err
		}
		configData[node.Name] = string(config)
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CONFIGMAP_NAME,
			Namespace: namespace,
		},
		Data: configData,
	}
	found := &corev1.ConfigMap{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Create(context.TODO(), cm)
			if err != nil {
				return fmt.Errorf("Couldn't create ConfigMap: %v", err)
			}
			logger.Info("Created ConfigMap for", cm.Namespace, cm.Name)
		} else {
			return fmt.Errorf("Failed to get ConfigMap: %v", err)
		}
	} else {
		logger.Info("ConfigMap already exists, updating")
		err = r.Update(context.TODO(), cm)
		if err != nil {
			return fmt.Errorf("Couldn't update ConfigMap: %v", err)
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncAllSriovNetworkNodeStates(np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, nl *corev1.NodeList) error {
	logger := r.Log.WithName("syncAllSriovNetworkNodeStates")
	logger.Info("Start to sync all SriovNetworkNodeState custom resource")
	found := &corev1.ConfigMap{}
	if err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: CONFIGMAP_NAME}, found); err != nil {
		logger.Info("Fail to get", "ConfigMap", CONFIGMAP_NAME)
	}
	for _, node := range nl.Items {
		logger.Info("Sync SriovNetworkNodeState CR", "name", node.Name)
		ns := &sriovnetworkv1.SriovNetworkNodeState{}
		ns.Name = node.Name
		ns.Namespace = namespace
		j, _ := json.Marshal(ns)
		logger.Info("SriovNetworkNodeState CR", "content", j)
		if err := r.syncSriovNetworkNodeState(np, npl, ns, &node, found.GetResourceVersion()); err != nil {
			logger.Error(err, "Fail to sync", "SriovNetworkNodeState", ns.Name)
			return err
		}
	}
	logger.Info("Remove SriovNetworkNodeState custom resource for unselected node")
	nsList := &sriovnetworkv1.SriovNetworkNodeStateList{}
	err := r.List(context.TODO(), nsList, &client.ListOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Info("Fail to list SriovNetworkNodeState CRs")
			return err
		}
	} else {
		for _, ns := range nsList.Items {
			found := false
			for _, node := range nl.Items {
				logger.Info("validate", "SriovNetworkNodeState", ns.GetName(), "node", node.GetName())
				if ns.GetName() == node.GetName() {
					found = true
					break
				}
			}
			if !found {
				err := r.Delete(context.TODO(), &ns, &client.DeleteOptions{})
				if err != nil {
					logger.Info("Fail to Delete", "SriovNetworkNodeState CR:", ns.GetName())
					return err
				}
			}
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncSriovNetworkNodeState(np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, ns *sriovnetworkv1.SriovNetworkNodeState, node *corev1.Node, cksum string) error {
	logger := r.Log.WithName("syncSriovNetworkNodeState")
	logger.Info("Start to sync SriovNetworkNodeState", "Name", ns.Name, "cksum", cksum)

	if err := controllerutil.SetControllerReference(np, ns, r.Scheme); err != nil {
		return err
	}
	found := &sriovnetworkv1.SriovNetworkNodeState{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: ns.Namespace, Name: ns.Name}, found)
	if err != nil {
		logger.Info("Fail to get SriovNetworkNodeState", "namespace", ns.Namespace, "name", ns.Name)
		if errors.IsNotFound(err) {
			ns.Spec.DpConfigVersion = cksum
			err = r.Create(context.TODO(), ns)
			if err != nil {
				return fmt.Errorf("Couldn't create SriovNetworkNodeState: %v", err)
			}
			logger.Info("Created SriovNetworkNodeState for", ns.Namespace, ns.Name)
		} else {
			return fmt.Errorf("Failed to get SriovNetworkNodeState: %v", err)
		}
	} else {
		logger.Info("SriovNetworkNodeState already exists, updating")
		newVersion := found.DeepCopy()
		newVersion.Spec = ns.Spec

		// Previous Policy Priority(ppp) records the priority of previous evaluated policy in node policy list.
		// Since node policy list is already sorted with priority number, comparing current priority with ppp shall
		// be sufficient.
		// ppp is set to 100 as initial value to avoid matching with the first policy in policy list, although
		// it should not matter since the flag used in p.Apply() will only be applied when VF partition is detected.
		ppp := 100
		for _, p := range npl.Items {
			if p.Name == "default" {
				continue
			}
			if p.Selected(node) {
				logger.Info("apply", "policy", p.Name, "node", node.Name)
				// Merging only for policies with the same priority (ppp == p.Spec.Priority)
				// This boolean flag controls merging of PF configuration (e.g. mtu, numvfs etc)
				// when VF partition is configured.
				p.Apply(newVersion, bool(ppp == p.Spec.Priority))
				// record the evaluated policy priority for next loop
				ppp = p.Spec.Priority
			}
		}
		newVersion.Spec.DpConfigVersion = cksum
		if equality.Semantic.DeepDerivative(newVersion.Spec, found.Spec) {
			logger.Info("SriovNetworkNodeState did not change, not updating")
			return nil
		}
		err = r.Update(context.TODO(), newVersion)
		if err != nil {
			return fmt.Errorf("Couldn't update SriovNetworkNodeState: %v", err)
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncPluginDaemonObjs(dp *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList) error {
	logger := r.Log.WithName("syncPluginDaemonObjs")
	logger.Info("Start to sync sriov daemons objects")

	if len(pl.Items) < 2 {
		r.tryDeleteDsPods(namespace, "sriov-device-plugin")
		r.tryDeleteDsPods(namespace, "sriov-cni")
		return nil
	}
	// render RawCNIConfig manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = namespace
	data.Data["SRIOVCNIImage"] = os.Getenv("SRIOV_CNI_IMAGE")
	data.Data["SRIOVInfiniBandCNIImage"] = os.Getenv("SRIOV_INFINIBAND_CNI_IMAGE")
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ResourcePrefix"] = os.Getenv("RESOURCE_PREFIX")
	envCniBinPath := os.Getenv("SRIOV_CNI_BIN_PATH")
	if envCniBinPath == "" {
		data.Data["CNIBinPath"] = "/var/lib/cni/bin"
	} else {
		logger.Info("New cni bin found", "CNIBinPath", envCniBinPath)
		data.Data["CNIBinPath"] = envCniBinPath
	}

	objs, err := renderDsForCR(PLUGIN_PATH, &data)
	if err != nil {
		logger.Error(err, "Fail to render SR-IoV manifests")
		return err
	}

	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err = r.Get(context.TODO(), types.NamespacedName{
		Name: DEFAULT_CONFIG_NAME, Namespace: namespace}, defaultConfig)
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
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) tryDeleteDsPods(namespace, name string) error {
	logger := r.Log.WithName("tryDeleteDsPods")
	ds := &appsv1.DaemonSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: name}, ds)
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
		err = r.Update(context.TODO(), ds)
		if err != nil {
			logger.Error(err, "Fail to update DaemonSet", "Namespace", namespace, "Name", name)
			return err
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncDsObject(dp *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList, obj *uns.Unstructured) error {
	logger := r.Log.WithName("syncDsObject")
	kind := obj.GetKind()
	logger.Info("Start to sync Objects", "Kind", kind)
	switch kind {
	case "ServiceAccount", "Role", "RoleBinding":
		if err := controllerutil.SetControllerReference(dp, obj, r.Scheme); err != nil {
			return err
		}
		if err := apply.ApplyObject(context.TODO(), r, obj); err != nil {
			logger.Error(err, "Fail to sync", "Kind", kind)
			return err
		}
	case "DaemonSet":
		ds := &appsv1.DaemonSet{}
		err := r.Scheme.Convert(obj, ds, nil)
		r.syncDaemonSet(dp, pl, ds)
		if err != nil {
			logger.Error(err, "Fail to sync DaemonSet", "Namespace", ds.Namespace, "Name", ds.Name)
			return err
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncDaemonSet(cr *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList, in *appsv1.DaemonSet) error {
	logger := r.Log.WithName("syncDaemonSet")
	logger.Info("Start to sync DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
	var err error

	if pl != nil {
		if err = setDsNodeAffinity(pl, in); err != nil {
			return err
		}
	}
	if err = controllerutil.SetControllerReference(cr, in, r.Scheme); err != nil {
		return err
	}
	ds := &appsv1.DaemonSet{}
	err = r.Get(context.TODO(), types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, ds)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Created DaemonSet", in.Namespace, in.Name)
			err = r.Create(context.TODO(), in)
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
		// DeepDerivative checks for changes only comparing non zero fields in the source struct.
		// This skips default values added by the api server.
		// References in https://github.com/kubernetes-sigs/kubebuilder/issues/592#issuecomment-625738183
		if equality.Semantic.DeepDerivative(in.Spec, ds.Spec) {
			logger.Info("Daemonset spec did not change, not updating")
			return nil
		}
		err = r.Update(context.TODO(), in)
		if err != nil {
			logger.Error(err, "Fail to update DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
			return err
		}
	}
	return nil
}

func setDsNodeAffinity(pl *sriovnetworkv1.SriovNetworkNodePolicyList, ds *appsv1.DaemonSet) error {
	terms := nodeSelectorTermsForPolicyList(pl.Items)
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

func nodeSelectorTermsForPolicyList(policies []sriovnetworkv1.SriovNetworkNodePolicy) []corev1.NodeSelectorTerm {
	terms := []corev1.NodeSelectorTerm{}
	for _, p := range policies {
		nodeSelector := corev1.NodeSelectorTerm{}
		if len(p.Spec.NodeSelector) == 0 {
			continue
		}
		expressions := []corev1.NodeSelectorRequirement{}
		for k, v := range p.Spec.NodeSelector {
			exp := corev1.NodeSelectorRequirement{
				Operator: corev1.NodeSelectorOpIn,
				Key:      k,
				Values:   []string{v},
			}
			expressions = append(expressions, exp)
		}
		// sorting is needed to keep the daemon spec stable.
		// the items are popped in a random order from the map
		sort.Slice(expressions, func(i, j int) bool {
			return expressions[i].Key < expressions[j].Key
		})
		nodeSelector = corev1.NodeSelectorTerm{
			MatchExpressions: expressions,
		}
		terms = append(terms, nodeSelector)
	}

	return terms
}

// renderDsForCR returns a busybox pod with the same name/namespace as the cr
func renderDsForCR(path string, data *render.RenderData) ([]*uns.Unstructured, error) {
	logger := ctlrlogger.WithName("renderDsForCR")
	logger.Info("Start to render objects")
	var err error
	objs := []*uns.Unstructured{}

	objs, err = render.RenderDir(path, data)
	if err != nil {
		return nil, errs.Wrap(err, "failed to render OpenShiftSRIOV Network manifests")
	}
	return objs, nil
}

func (r *SriovNetworkNodePolicyReconciler) renderDevicePluginConfigData(pl *sriovnetworkv1.SriovNetworkNodePolicyList, node *corev1.Node) (dptypes.ResourceConfList, error) {
	logger := ctlrlogger.WithName("renderDevicePluginConfigData")
	logger.Info("Start to render device plugin config data")
	rcl := dptypes.ResourceConfList{}
	for _, p := range pl.Items {
		if p.Name == "default" {
			continue
		}

		// render node specific data for device plugin config
		if !p.Selected(node) {
			continue
		}

		found, i := resourceNameInList(p.Spec.ResourceName, &rcl)
		netDeviceSelectors := dptypes.NetDeviceSelectors{}
		if found {
			if err := json.Unmarshal(*rcl.ResourceList[i].Selectors, &netDeviceSelectors); err != nil {
				return rcl, err
			}

			if p.Spec.NicSelector.Vendor != "" && !sriovnetworkv1.StringInArray(p.Spec.NicSelector.Vendor, netDeviceSelectors.Vendors) {
				netDeviceSelectors.Vendors = append(netDeviceSelectors.Vendors, p.Spec.NicSelector.Vendor)
			}
			if p.Spec.NicSelector.DeviceID != "" {
				var deviceID string
				if p.Spec.NumVfs == 0 {
					deviceID = p.Spec.NicSelector.DeviceID
				} else {
					deviceID = sriovnetworkv1.GetVfDeviceId(p.Spec.NicSelector.DeviceID)
				}

				if !sriovnetworkv1.StringInArray(deviceID, netDeviceSelectors.Devices) && deviceID != "" {
					netDeviceSelectors.Devices = append(netDeviceSelectors.Devices, deviceID)
				}
			}
			if len(p.Spec.NicSelector.PfNames) > 0 {
				netDeviceSelectors.PfNames = sriovnetworkv1.UniqueAppend(netDeviceSelectors.PfNames, p.Spec.NicSelector.PfNames...)
			}
			if len(p.Spec.NicSelector.RootDevices) > 0 {
				netDeviceSelectors.RootDevices = sriovnetworkv1.UniqueAppend(netDeviceSelectors.RootDevices, p.Spec.NicSelector.RootDevices...)
			}
			// Removed driver constraint for "netdevice" DeviceType
			if p.Spec.DeviceType == "vfio-pci" {
				netDeviceSelectors.Drivers = sriovnetworkv1.UniqueAppend(netDeviceSelectors.Drivers, p.Spec.DeviceType)
			}
			// Enable the selection of devices using NetFilter
			if p.Spec.NicSelector.NetFilter != "" {
				nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
				err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: node.Name}, nodeState)
				if err == nil {
					// Loop through interfaces status to find a match for NetworkID or NetworkTag
					for _, intf := range nodeState.Status.Interfaces {
						if sriovnetworkv1.NetFilterMatch(p.Spec.NicSelector.NetFilter, intf.NetFilter) {
							// Found a match add the Interfaces PciAddress
							netDeviceSelectors.PciAddresses = sriovnetworkv1.UniqueAppend(netDeviceSelectors.PciAddresses, intf.PciAddress)
						}
					}
				}
			}

			netDeviceSelectorsMarshal, err := json.Marshal(netDeviceSelectors)
			if err != nil {
				return rcl, err
			}
			rawNetDeviceSelectors := json.RawMessage(netDeviceSelectorsMarshal)
			rcl.ResourceList[i].Selectors = &rawNetDeviceSelectors
			logger.Info("Update resource", "Resource", rcl.ResourceList[i])
		} else {
			rc := &dptypes.ResourceConfig{
				ResourceName: p.Spec.ResourceName,
			}
			netDeviceSelectors.IsRdma = p.Spec.IsRdma

			if p.Spec.NicSelector.Vendor != "" {
				netDeviceSelectors.Vendors = append(netDeviceSelectors.Vendors, p.Spec.NicSelector.Vendor)
			}
			if p.Spec.NicSelector.DeviceID != "" {
				var deviceID string
				if p.Spec.NumVfs == 0 {
					deviceID = p.Spec.NicSelector.DeviceID
				} else {
					deviceID = sriovnetworkv1.GetVfDeviceId(p.Spec.NicSelector.DeviceID)
				}

				if !sriovnetworkv1.StringInArray(deviceID, netDeviceSelectors.Devices) && deviceID != "" {
					netDeviceSelectors.Devices = append(netDeviceSelectors.Devices, deviceID)
				}
			}
			if len(p.Spec.NicSelector.PfNames) > 0 {
				netDeviceSelectors.PfNames = append(netDeviceSelectors.PfNames, p.Spec.NicSelector.PfNames...)
			}
			if len(p.Spec.NicSelector.RootDevices) > 0 {
				netDeviceSelectors.RootDevices = append(netDeviceSelectors.RootDevices, p.Spec.NicSelector.RootDevices...)
			}
			// Removed driver constraint for "netdevice" DeviceType
			if p.Spec.DeviceType == "vfio-pci" {
				netDeviceSelectors.Drivers = append(netDeviceSelectors.Drivers, p.Spec.DeviceType)
			}
			// Enable the selection of devices using NetFilter
			if p.Spec.NicSelector.NetFilter != "" {
				nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
				err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: node.Name}, nodeState)
				if err == nil {
					// Loop through interfaces status to find a match for NetworkID or NetworkTag
					for _, intf := range nodeState.Status.Interfaces {
						if sriovnetworkv1.NetFilterMatch(p.Spec.NicSelector.NetFilter, intf.NetFilter) {
							// Found a match add the Interfaces PciAddress
							netDeviceSelectors.PciAddresses = sriovnetworkv1.UniqueAppend(netDeviceSelectors.PciAddresses, intf.PciAddress)
						}
					}
				}
			}
			netDeviceSelectorsMarshal, err := json.Marshal(netDeviceSelectors)
			if err != nil {
				return rcl, err
			}
			rawNetDeviceSelectors := json.RawMessage(netDeviceSelectorsMarshal)
			rc.Selectors = &rawNetDeviceSelectors
			rcl.ResourceList = append(rcl.ResourceList, *rc)
			logger.Info("Add resource", "Resource", *rc, "Resource list", rcl.ResourceList)
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
