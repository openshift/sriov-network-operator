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
	"sort"
	"strings"
	"time"

	errs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	utils "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"

	dptypes "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/types"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
)

const nodePolicySyncEventName = "node-policy-sync-event"

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
	// Only handle node-policy-sync-event
	if req.Name != nodePolicySyncEventName || req.Namespace != "" {
		return reconcile.Result{}, nil
	}

	reqLogger := log.FromContext(ctx)
	reqLogger.Info("Reconciling")

	defaultPolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
	err := r.Get(ctx, types.NamespacedName{Name: constants.DefaultPolicyName, Namespace: vars.Namespace}, defaultPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// Default policy object not found, create it.
			defaultPolicy.SetNamespace(vars.Namespace)
			defaultPolicy.SetName(constants.DefaultPolicyName)
			defaultPolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
				NumVfs:       0,
				NodeSelector: make(map[string]string),
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
			}
			err = r.Create(ctx, defaultPolicy)
			if err != nil {
				reqLogger.Error(err, "Failed to create default Policy", "Namespace", vars.Namespace, "Name", constants.DefaultPolicyName)
				return reconcile.Result{}, err
			}
			reqLogger.Info("Default policy created")
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
		"kubernetes.io/os":               "linux",
	}
	defaultOpConf := &sriovnetworkv1.SriovOperatorConfig{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: constants.DefaultConfigName}, defaultOpConf); err != nil {
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
	// Sync SriovNetworkNodeState objects
	if err = r.syncAllSriovNetworkNodeStates(ctx, defaultPolicy, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	// Sync Sriov device plugin ConfigMap object
	if err = r.syncDevicePluginConfigMap(ctx, defaultOpConf, policyList, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	// Render and sync Daemon objects
	if err = syncPluginDaemonObjs(ctx, r.Client, r.Scheme, defaultPolicy, policyList); err != nil {
		return reconcile.Result{}, err
	}

	// All was successful. Request that this be re-triggered after ResyncPeriod,
	// so we can reconcile state again.
	return reconcile.Result{RequeueAfter: constants.ResyncPeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SriovNetworkNodePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	qHandler := func(q workqueue.RateLimitingInterface) {
		q.AddAfter(reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: "",
			Name:      nodePolicySyncEventName,
		}}, time.Second)
	}

	delayedEventHandler := handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
			log.Log.WithName("SriovNetworkNodePolicy").
				Info("Enqueuing sync for create event", "resource", e.Object.GetName())
			qHandler(q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
			log.Log.WithName("SriovNetworkNodePolicy").
				Info("Enqueuing sync for update event", "resource", e.ObjectNew.GetName())
			qHandler(q)
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
			log.Log.WithName("SriovNetworkNodePolicy").
				Info("Enqueuing sync for delete event", "resource", e.Object.GetName())
			qHandler(q)
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sriovnetworkv1.SriovNetworkNodePolicy{}).
		Watches(&sriovnetworkv1.SriovNetworkNodePolicy{}, delayedEventHandler).
		Complete(r)
}

func (r *SriovNetworkNodePolicyReconciler) syncDevicePluginConfigMap(ctx context.Context, dc *sriovnetworkv1.SriovOperatorConfig,
	pl *sriovnetworkv1.SriovNetworkNodePolicyList, nl *corev1.NodeList) error {
	logger := log.Log.WithName("syncDevicePluginConfigMap")
	logger.V(1).Info("Start to sync device plugin ConfigMap")

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
			Namespace: vars.Namespace,
		},
		Data: configData,
	}

	if err := controllerutil.SetControllerReference(dc, cm, r.Scheme); err != nil {
		return err
	}

	found := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Create(ctx, cm)
			if err != nil {
				return fmt.Errorf("couldn't create ConfigMap: %v", err)
			}
			logger.V(1).Info("Created ConfigMap for", cm.Namespace, cm.Name)
		} else {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}
	} else {
		logger.V(1).Info("ConfigMap already exists, updating")
		err = r.Update(ctx, cm)
		if err != nil {
			return fmt.Errorf("couldn't update ConfigMap: %v", err)
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncAllSriovNetworkNodeStates(ctx context.Context, np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, nl *corev1.NodeList) error {
	logger := log.Log.WithName("syncAllSriovNetworkNodeStates")
	logger.V(1).Info("Start to sync all SriovNetworkNodeState custom resource")
	found := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: constants.ConfigMapName}, found); err != nil {
		logger.V(1).Info("Fail to get", "ConfigMap", constants.ConfigMapName)
	}
	for _, node := range nl.Items {
		logger.V(1).Info("Sync SriovNetworkNodeState CR", "name", node.Name)
		ns := &sriovnetworkv1.SriovNetworkNodeState{}
		ns.Name = node.Name
		ns.Namespace = vars.Namespace
		j, _ := json.Marshal(ns)
		logger.V(2).Info("SriovNetworkNodeState CR", "content", j)
		if err := r.syncSriovNetworkNodeState(ctx, np, npl, ns, &node, utils.HashConfigMap(found)); err != nil {
			logger.Error(err, "Fail to sync", "SriovNetworkNodeState", ns.Name)
			return err
		}
	}
	logger.V(1).Info("Remove SriovNetworkNodeState custom resource for unselected node")
	nsList := &sriovnetworkv1.SriovNetworkNodeStateList{}
	err := r.List(ctx, nsList, &client.ListOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Fail to list SriovNetworkNodeState CRs")
			return err
		}
	} else {
		for _, ns := range nsList.Items {
			found := false
			for _, node := range nl.Items {
				if ns.GetName() == node.GetName() {
					found = true
					break
				}
			}
			if !found {
				err := r.Delete(ctx, &ns, &client.DeleteOptions{})
				if err != nil {
					logger.Error(err, "Fail to Delete", "SriovNetworkNodeState CR:", ns.GetName())
					return err
				}
			}
		}
	}
	return nil
}

func (r *SriovNetworkNodePolicyReconciler) syncSriovNetworkNodeState(ctx context.Context, np *sriovnetworkv1.SriovNetworkNodePolicy, npl *sriovnetworkv1.SriovNetworkNodePolicyList, ns *sriovnetworkv1.SriovNetworkNodeState, node *corev1.Node, cksum string) error {
	logger := log.Log.WithName("syncSriovNetworkNodeState")
	logger.V(1).Info("Start to sync SriovNetworkNodeState", "Name", ns.Name, "cksum", cksum)

	if err := controllerutil.SetControllerReference(np, ns, r.Scheme); err != nil {
		return err
	}
	found := &sriovnetworkv1.SriovNetworkNodeState{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ns.Namespace, Name: ns.Name}, found)
	if err != nil {
		logger.Error(err, "Fail to get SriovNetworkNodeState", "namespace", ns.Namespace, "name", ns.Name)
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

		logger.V(1).Info("SriovNetworkNodeState already exists, updating")
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
		if equality.Semantic.DeepEqual(newVersion.Spec, found.Spec) {
			logger.V(1).Info("SriovNetworkNodeState did not change, not updating")
			return nil
		}
		err = r.Update(ctx, newVersion)
		if err != nil {
			return fmt.Errorf("couldn't update SriovNetworkNodeState: %v", err)
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
	logger.V(1).Info("Start to render objects")

	objs, err := render.RenderDir(path, data)
	if err != nil {
		return nil, errs.Wrap(err, "failed to render SR-IOV Network Operator manifests")
	}
	return objs, nil
}

func (r *SriovNetworkNodePolicyReconciler) renderDevicePluginConfigData(ctx context.Context, pl *sriovnetworkv1.SriovNetworkNodePolicyList, node *corev1.Node) (dptypes.ResourceConfList, error) {
	logger := log.Log.WithName("renderDevicePluginConfigData")
	logger.V(1).Info("Start to render device plugin config data", "node", node.Name)
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
		err := r.Get(ctx, types.NamespacedName{Namespace: vars.Namespace, Name: node.Name}, nodeState)
		if err != nil {
			return rcl, err
		}

		found, i := resourceNameInList(p.Spec.ResourceName, &rcl)

		if found {
			err := updateDevicePluginResource(ctx, &rcl.ResourceList[i], &p, nodeState)
			if err != nil {
				return rcl, err
			}
			logger.V(1).Info("Update resource", "Resource", rcl.ResourceList[i])
		} else {
			rc, err := createDevicePluginResource(ctx, &p, nodeState)
			if err != nil {
				return rcl, err
			}
			rcl.ResourceList = append(rcl.ResourceList, *rc)
			logger.V(1).Info("Add resource", "Resource", *rc)
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
		if len(nodeState.Status.Interfaces) == 0 {
			return nil, fmt.Errorf("node state %s doesn't contain interfaces data", nodeState.Name)
		}
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

	rc.ExcludeTopology = p.Spec.ExcludeTopology

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

	rc.ExcludeTopology = p.Spec.ExcludeTopology

	return nil
}
