/*
Copyright 2021.

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
	"strings"

	errs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dptypes "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/types"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apply"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	render "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
)

// SriovNetworkNodePolicyReconciler reconciles a SriovNetworkNodePolicy object
type SriovNetworkNodePolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworknodepolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworknodepolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworknodepolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SriovNetworkNodePolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *SriovNetworkNodePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx).WithValues("sriovnetworknodepolicy", req.NamespacedName)

	reqLogger.Info("Reconciling")

	defaultPolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
	err := r.Get(ctx, types.NamespacedName{Name: constants.DefaultPolicyName, Namespace: namespace}, defaultPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// Default policy object not found, create it.
			defaultPolicy.SetNamespace(namespace)
			defaultPolicy.SetName(constants.DefaultPolicyName)
			defaultPolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
				NumVfs:       0,
				NodeSelector: make(map[string]string),
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
			}
			err = r.Create(ctx, defaultPolicy)
			if err != nil {
				reqLogger.Error(err, "Failed to create default Policy", "Namespace", namespace, "Name", constants.DefaultPolicyName)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Fetch the SriovNetworkNodePolicyList
	policyList := &sriovnetworkv1.SriovNetworkNodePolicyList{}
	err = r.List(ctx, policyList, &client.ListOptions{})
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
	lo := &client.MatchingLabels{
		"node-role.kubernetes.io/worker": "",
		"beta.kubernetes.io/os":          "linux",
	}
	defaultOpConf := &sriovnetworkv1.SriovOperatorConfig{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: constants.DefaultConfigName}, defaultOpConf); err != nil {
		return reconcile.Result{}, err
	}
	if len(defaultOpConf.Spec.ConfigDaemonNodeSelector) > 0 {
		labels := client.MatchingLabels(defaultOpConf.Spec.ConfigDaemonNodeSelector)
		lo = &labels
	}
	err = r.List(ctx, nodeList, lo)
	if err != nil {
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Fail to list nodes")
		return reconcile.Result{}, err
	}

	// Sort the policies with priority, higher priority ones is applied later
	sort.Sort(sriovnetworkv1.ByPriority(policyList.Items))
	// Sync Sriov device plugin ConfigMap object
	if err = r.syncDevicePluginConfigMap(ctx, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	// Render and sync Daemon objects
	if err = r.syncPluginDaemonObjs(ctx, defaultPolicy, policyList); err != nil {
		return reconcile.Result{}, err
	}
	// Sync SriovNetworkNodeState objects
	if err = r.syncAllSriovNetworkNodeStates(ctx, defaultPolicy, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}

	// All was successful. Request that this be re-triggered after ResyncPeriod,
	// so we can reconcile state again.
	return reconcile.Result{RequeueAfter: constants.ResyncPeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SriovNetworkNodePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sriovnetworkv1.SriovNetworkNodePolicy{}).
		Complete(r)
}

func (r *SriovNetworkNodePolicyReconciler) syncDevicePluginConfigMap(ctx context.Context, pl *sriovnetworkv1.SriovNetworkNodePolicyList, nl *corev1.NodeList) error {
	logger := log.Log.WithName("syncDevicePluginConfigMap")
	logger.Info("Start to sync device plugin ConfigMap")

	configData := make(map[string]string)
	for _, node := range nl.Items {
		data, err := r.renderDevicePluginConfigData(ctx, pl, &node)
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
			Name:      constants.ConfigMapName,
			Namespace: namespace,
		},
		Data: configData,
	}
	found := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Create(ctx, cm)
			if err != nil {
				return fmt.Errorf("couldn't create ConfigMap: %v", err)
			}
			logger.Info("Created ConfigMap for", cm.Namespace, cm.Name)
		} else {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}
	} else {
		logger.Info("ConfigMap already exists, updating")
		err = r.Update(ctx, cm)
		if err != nil {
			return fmt.Errorf("couldn't update ConfigMap: %v", err)
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncAllSriovNetworkNodeStates(ctx context.Context, np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, nl *corev1.NodeList) error {
	logger := log.Log.WithName("syncAllSriovNetworkNodeStates")
	logger.Info("Start to sync all SriovNetworkNodeState custom resource")
	found := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: constants.ConfigMapName}, found); err != nil {
		logger.Info("Fail to get", "ConfigMap", constants.ConfigMapName)
	}
	for _, node := range nl.Items {
		logger.Info("Sync SriovNetworkNodeState CR", "name", node.Name)
		ns := &sriovnetworkv1.SriovNetworkNodeState{}
		ns.Name = node.Name
		ns.Namespace = namespace
		j, _ := json.Marshal(ns)
		logger.Info("SriovNetworkNodeState CR", "content", j)
		if err := r.syncSriovNetworkNodeState(ctx, np, npl, ns, &node, found.GetResourceVersion()); err != nil {
			logger.Error(err, "Fail to sync", "SriovNetworkNodeState", ns.Name)
			return err
		}
	}
	logger.Info("Remove SriovNetworkNodeState custom resource for unselected node")
	nsList := &sriovnetworkv1.SriovNetworkNodeStateList{}
	err := r.List(ctx, nsList, &client.ListOptions{})
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
				err := r.Delete(ctx, &ns, &client.DeleteOptions{})
				if err != nil {
					logger.Info("Fail to Delete", "SriovNetworkNodeState CR:", ns.GetName())
					return err
				}
			}
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncSriovNetworkNodeState(ctx context.Context, np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, ns *sriovnetworkv1.SriovNetworkNodeState, node *corev1.Node, cksum string) error {
	logger := log.Log.WithName("syncSriovNetworkNodeState")
	logger.Info("Start to sync SriovNetworkNodeState", "Name", ns.Name, "cksum", cksum)

	if err := controllerutil.SetControllerReference(np, ns, r.Scheme); err != nil {
		return err
	}
	found := &sriovnetworkv1.SriovNetworkNodeState{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ns.Namespace, Name: ns.Name}, found)
	if err != nil {
		logger.Info("Fail to get SriovNetworkNodeState", "namespace", ns.Namespace, "name", ns.Name)
		if errors.IsNotFound(err) {
			ns.Spec.DpConfigVersion = cksum
			err = r.Create(ctx, ns)
			if err != nil {
				return fmt.Errorf("couldn't create SriovNetworkNodeState: %v", err)
			}
			logger.Info("Created SriovNetworkNodeState for", ns.Namespace, ns.Name)
		} else {
			return fmt.Errorf("failed to get SriovNetworkNodeState: %v", err)
		}
	} else {
		if len(found.Status.Interfaces) == 0 {
			logger.Info("SriovNetworkNodeState Status Interfaces are empty. Skip update of policies in spec",
				"namespace", ns.Namespace, "name", ns.Name)
			return nil
		}

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
			if p.Name == constants.DefaultPolicyName {
				continue
			}
			if p.Selected(node) {
				logger.Info("apply", "policy", p.Name, "node", node.Name)
				// Merging only for policies with the same priority (ppp == p.Spec.Priority)
				// This boolean flag controls merging of PF configuration (e.g. mtu, numvfs etc)
				// when VF partition is configured.
				err = p.Apply(newVersion, ppp == p.Spec.Priority)
				if err != nil {
					return err
				}
				// record the evaluated policy priority for next loop
				ppp = p.Spec.Priority
			}
		}
		newVersion.Spec.DpConfigVersion = cksum
		if equality.Semantic.DeepDerivative(newVersion.Spec, found.Spec) {
			logger.Info("SriovNetworkNodeState did not change, not updating")
			return nil
		}
		err = r.Update(ctx, newVersion)
		if err != nil {
			return fmt.Errorf("couldn't update SriovNetworkNodeState: %v", err)
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncPluginDaemonObjs(ctx context.Context, dp *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList) error {
	logger := log.Log.WithName("syncPluginDaemonObjs")
	logger.Info("Start to sync sriov daemons objects")

	// render plugin manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = namespace
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ResourcePrefix"] = os.Getenv("RESOURCE_PREFIX")
	data.Data["ImagePullSecrets"] = GetImagePullSecrets()

	objs, err := renderDsForCR(constants.PluginPath, &data)
	if err != nil {
		logger.Error(err, "Fail to render SR-IoV manifests")
		return err
	}

	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err = r.Get(ctx, types.NamespacedName{
		Name: constants.DefaultConfigName, Namespace: namespace}, defaultConfig)
	if err != nil {
		return err
	}

	if len(pl.Items) < 2 {
		for _, obj := range objs {
			err := r.deleteK8sResource(ctx, obj)
			if err != nil {
				return err
			}
		}
		return nil
	}

	// Sync DaemonSets
	for _, obj := range objs {
		if obj.GetKind() == constants.DaemonSet && len(defaultConfig.Spec.ConfigDaemonNodeSelector) > 0 {
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
		err = r.syncDsObject(ctx, dp, pl, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}

	// Sriov-cni container has been moved to sriov-network-config-daemon DaemonSet.
	// Delete stale sriov-cni manifests. Revert this change once sriov-cni daemonSet
	// is deprecated.
	err = r.deleteSriovCniManifests(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (r *SriovNetworkNodePolicyReconciler) deleteSriovCniManifests(ctx context.Context) error {
	ds := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "sriov-cni"}, ds)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
	} else {
		err = r.Delete(ctx, ds)
		if err != nil {
			return err
		}
	}

	rb := &rbacv1.RoleBinding{}
	err = r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "sriov-cni"}, rb)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
	} else {
		err = r.Delete(ctx, rb)
		if err != nil {
			return err
		}
	}

	sa := &corev1.ServiceAccount{}
	err = r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "sriov-cni"}, sa)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
	} else {
		err = r.Delete(ctx, sa)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *SriovNetworkNodePolicyReconciler) deleteK8sResource(ctx context.Context, in *uns.Unstructured) error {
	if err := apply.DeleteObject(ctx, r.Client, in); err != nil {
		return fmt.Errorf("failed to delete object %v with err: %v", in, err)
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncDsObject(ctx context.Context, dp *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList, obj *uns.Unstructured) error {
	logger := log.Log.WithName("syncDsObject")
	kind := obj.GetKind()
	logger.Info("Start to sync Objects", "Kind", kind)
	switch kind {
	case "ServiceAccount", "Role", "RoleBinding":
		if err := controllerutil.SetControllerReference(dp, obj, r.Scheme); err != nil {
			return err
		}
		if err := apply.ApplyObject(ctx, r.Client, obj); err != nil {
			logger.Error(err, "Fail to sync", "Kind", kind)
			return err
		}
	case constants.DaemonSet:
		ds := &appsv1.DaemonSet{}
		err := r.Scheme.Convert(obj, ds, nil)
		r.syncDaemonSet(ctx, dp, pl, ds)
		if err != nil {
			logger.Error(err, "Fail to sync DaemonSet", "Namespace", ds.Namespace, "Name", ds.Name)
			return err
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncDaemonSet(ctx context.Context, cr *sriovnetworkv1.SriovNetworkNodePolicy, pl *sriovnetworkv1.SriovNetworkNodePolicyList, in *appsv1.DaemonSet) error {
	logger := log.Log.WithName("syncDaemonSet")
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
	err = r.Get(ctx, types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, ds)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Created DaemonSet", in.Namespace, in.Name)
			err = r.Create(ctx, in)
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
			// DeepDerivative has issue detecting nodeAffinity change
			// https://bugzilla.redhat.com/show_bug.cgi?id=1914066
			if equality.Semantic.DeepEqual(in.Spec.Template.Spec.Affinity.NodeAffinity,
				ds.Spec.Template.Spec.Affinity.NodeAffinity) {
				logger.Info("Daemonset spec did not change, not updating")
				return nil
			}
		}
		err = r.Update(ctx, in)
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
		nodeSelector := corev1.NodeSelectorTerm{
			MatchExpressions: expressions,
		}
		terms = append(terms, nodeSelector)
	}

	return terms
}

// renderDsForCR returns a busybox pod with the same name/namespace as the cr
func renderDsForCR(path string, data *render.RenderData) ([]*uns.Unstructured, error) {
	logger := log.Log.WithName("renderDsForCR")
	logger.Info("Start to render objects")

	objs, err := render.RenderDir(path, data)
	if err != nil {
		return nil, errs.Wrap(err, "failed to render OpenShiftSRIOV Network manifests")
	}
	return objs, nil
}

func (r *SriovNetworkNodePolicyReconciler) renderDevicePluginConfigData(ctx context.Context, pl *sriovnetworkv1.SriovNetworkNodePolicyList, node *corev1.Node) (dptypes.ResourceConfList, error) {
	logger := log.Log.WithName("renderDevicePluginConfigData")
	logger.Info("Start to render device plugin config data")
	rcl := dptypes.ResourceConfList{}
	for _, p := range pl.Items {
		if p.Name == constants.DefaultPolicyName {
			continue
		}

		// render node specific data for device plugin config
		if !p.Selected(node) {
			continue
		}

		nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
		err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: node.Name}, nodeState)
		if err != nil {
			return rcl, err
		}

		found, i := resourceNameInList(p.Spec.ResourceName, &rcl)

		if found {
			err := updateDevicePluginResource(ctx, &rcl.ResourceList[i], &p, nodeState)
			if err != nil {
				return rcl, err
			}
			logger.Info("Update resource", "Resource", rcl.ResourceList[i])
		} else {
			rc, err := createDevicePluginResource(ctx, &p, nodeState)
			if err != nil {
				return rcl, err
			}
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

func createDevicePluginResource(
	ctx context.Context,
	p *sriovnetworkv1.SriovNetworkNodePolicy,
	nodeState *sriovnetworkv1.SriovNetworkNodeState) (*dptypes.ResourceConfig, error) {
	netDeviceSelectors := dptypes.NetDeviceSelectors{}

	rc := &dptypes.ResourceConfig{
		ResourceName: p.Spec.ResourceName,
	}
	netDeviceSelectors.IsRdma = p.Spec.IsRdma
	netDeviceSelectors.NeedVhostNet = p.Spec.NeedVhostNet
	netDeviceSelectors.VdpaType = dptypes.VdpaType(p.Spec.VdpaType)

	if p.Spec.NicSelector.Vendor != "" {
		netDeviceSelectors.Vendors = append(netDeviceSelectors.Vendors, p.Spec.NicSelector.Vendor)
	}
	if p.Spec.NicSelector.DeviceID != "" {
		var deviceID string
		if p.Spec.NumVfs == 0 {
			deviceID = p.Spec.NicSelector.DeviceID
		} else {
			deviceID = sriovnetworkv1.GetVfDeviceID(p.Spec.NicSelector.DeviceID)
		}

		if !sriovnetworkv1.StringInArray(deviceID, netDeviceSelectors.Devices) && deviceID != "" {
			netDeviceSelectors.Devices = append(netDeviceSelectors.Devices, deviceID)
		}
	}
	if len(p.Spec.NicSelector.PfNames) > 0 {
		netDeviceSelectors.PfNames = append(netDeviceSelectors.PfNames, p.Spec.NicSelector.PfNames...)
	}
	// vfio-pci device link type is not detectable
	if p.Spec.DeviceType != constants.DeviceTypeVfioPci {
		if p.Spec.LinkType != "" {
			linkType := constants.LinkTypeEthernet
			if strings.EqualFold(p.Spec.LinkType, constants.LinkTypeIB) {
				linkType = constants.LinkTypeInfiniband
			}
			netDeviceSelectors.LinkTypes = sriovnetworkv1.UniqueAppend(netDeviceSelectors.LinkTypes, linkType)
		}
	}
	if len(p.Spec.NicSelector.RootDevices) > 0 {
		netDeviceSelectors.RootDevices = append(netDeviceSelectors.RootDevices, p.Spec.NicSelector.RootDevices...)
	}
	// Removed driver constraint for "netdevice" DeviceType
	if p.Spec.DeviceType == constants.DeviceTypeVfioPci {
		netDeviceSelectors.Drivers = append(netDeviceSelectors.Drivers, p.Spec.DeviceType)
	}
	// Enable the selection of devices using NetFilter
	if p.Spec.NicSelector.NetFilter != "" {
		// Loop through interfaces status to find a match for NetworkID or NetworkTag
		for _, intf := range nodeState.Status.Interfaces {
			if sriovnetworkv1.NetFilterMatch(p.Spec.NicSelector.NetFilter, intf.NetFilter) {
				// Found a match add the Interfaces PciAddress
				netDeviceSelectors.PciAddresses = sriovnetworkv1.UniqueAppend(netDeviceSelectors.PciAddresses, intf.PciAddress)
			}
		}
	}

	netDeviceSelectorsMarshal, err := json.Marshal(netDeviceSelectors)
	if err != nil {
		return nil, err
	}
	rawNetDeviceSelectors := json.RawMessage(netDeviceSelectorsMarshal)
	rc.Selectors = &rawNetDeviceSelectors

	return rc, nil
}

func updateDevicePluginResource(
	ctx context.Context,
	rc *dptypes.ResourceConfig,
	p *sriovnetworkv1.SriovNetworkNodePolicy,
	nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	netDeviceSelectors := dptypes.NetDeviceSelectors{}

	if err := json.Unmarshal(*rc.Selectors, &netDeviceSelectors); err != nil {
		return err
	}

	if p.Spec.NicSelector.Vendor != "" && !sriovnetworkv1.StringInArray(p.Spec.NicSelector.Vendor, netDeviceSelectors.Vendors) {
		netDeviceSelectors.Vendors = append(netDeviceSelectors.Vendors, p.Spec.NicSelector.Vendor)
	}
	if p.Spec.NicSelector.DeviceID != "" {
		var deviceID string
		if p.Spec.NumVfs == 0 {
			deviceID = p.Spec.NicSelector.DeviceID
		} else {
			deviceID = sriovnetworkv1.GetVfDeviceID(p.Spec.NicSelector.DeviceID)
		}

		if !sriovnetworkv1.StringInArray(deviceID, netDeviceSelectors.Devices) && deviceID != "" {
			netDeviceSelectors.Devices = append(netDeviceSelectors.Devices, deviceID)
		}
	}
	if len(p.Spec.NicSelector.PfNames) > 0 {
		netDeviceSelectors.PfNames = sriovnetworkv1.UniqueAppend(netDeviceSelectors.PfNames, p.Spec.NicSelector.PfNames...)
	}
	// vfio-pci device link type is not detectable
	if p.Spec.DeviceType != constants.DeviceTypeVfioPci {
		if p.Spec.LinkType != "" {
			linkType := constants.LinkTypeEthernet
			if strings.EqualFold(p.Spec.LinkType, constants.LinkTypeIB) {
				linkType = constants.LinkTypeInfiniband
			}
			if !sriovnetworkv1.StringInArray(linkType, netDeviceSelectors.LinkTypes) {
				netDeviceSelectors.LinkTypes = sriovnetworkv1.UniqueAppend(netDeviceSelectors.LinkTypes, linkType)
			}
		}
	}
	if len(p.Spec.NicSelector.RootDevices) > 0 {
		netDeviceSelectors.RootDevices = sriovnetworkv1.UniqueAppend(netDeviceSelectors.RootDevices, p.Spec.NicSelector.RootDevices...)
	}
	// Removed driver constraint for "netdevice" DeviceType
	if p.Spec.DeviceType == constants.DeviceTypeVfioPci {
		netDeviceSelectors.Drivers = sriovnetworkv1.UniqueAppend(netDeviceSelectors.Drivers, p.Spec.DeviceType)
	}
	// Enable the selection of devices using NetFilter
	if p.Spec.NicSelector.NetFilter != "" {
		// Loop through interfaces status to find a match for NetworkID or NetworkTag
		for _, intf := range nodeState.Status.Interfaces {
			if sriovnetworkv1.NetFilterMatch(p.Spec.NicSelector.NetFilter, intf.NetFilter) {
				// Found a match add the Interfaces PciAddress
				netDeviceSelectors.PciAddresses = sriovnetworkv1.UniqueAppend(netDeviceSelectors.PciAddresses, intf.PciAddress)
			}
		}
	}

	netDeviceSelectorsMarshal, err := json.Marshal(netDeviceSelectors)
	if err != nil {
		return err
	}
	rawNetDeviceSelectors := json.RawMessage(netDeviceSelectorsMarshal)
	rc.Selectors = &rawNetDeviceSelectors

	return nil
}
