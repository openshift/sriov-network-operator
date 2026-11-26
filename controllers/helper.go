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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"

	errs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	webhooks = map[string]string{
		constants.InjectorWebHookName: constants.InjectorWebHookPath,
		constants.OperatorWebHookName: constants.OperatorWebHookPath,
	}
	oneNode           = intstr.FromInt32(1)
	defaultPoolConfig = &sriovnetworkv1.SriovNetworkPoolConfig{Spec: sriovnetworkv1.SriovNetworkPoolConfigSpec{
		MaxUnavailable: &oneNode,
		NodeSelector:   &metav1.LabelSelector{},
		RdmaMode:       ""}}
)

const (
	// Device plugin bindata (constants.PluginPath) — keep resource names in sync with manifests.
	devicePluginDaemonSetName            = "sriov-device-plugin"
	devicePluginServiceAccountName       = "sriov-device-plugin"
	devicePluginSCCRoleName              = "sriov-plugin"
	devicePluginSCCRoleBindingName       = "sriov-device-plugin"
	devicePluginPodAccessRoleName        = "sriov-device-plugin-pod-access"
	devicePluginPodAccessRoleBindingName = "sriov-device-plugin-pod-access"

	clusterRoleResourceName               = "ClusterRole"
	clusterRoleBindingResourceName        = "ClusterRoleBinding"
	deviceClassResourceName               = "DeviceClass"
	mutatingWebhookConfigurationCRDName   = "MutatingWebhookConfiguration"
	validatingWebhookConfigurationCRDName = "ValidatingWebhookConfiguration"
	machineConfigCRDName                  = "MachineConfig"
	trueString                            = "true"
	defaultCNIBinPath                     = "/var/lib/cni/bin"
	defaultDRADriverCDIRoot               = "/var/run/cdi"
)

type DrainAnnotationPredicate struct {
	predicate.Funcs
}

func (DrainAnnotationPredicate) Create(e event.CreateEvent) bool {
	if e.Object == nil {
		return false
	}

	if _, hasAnno := e.Object.GetAnnotations()[constants.NodeDrainAnnotation]; hasAnno {
		return true
	}
	return false
}

func (DrainAnnotationPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		return false
	}
	if e.ObjectNew == nil {
		return false
	}

	oldAnno, hasOldAnno := e.ObjectOld.GetAnnotations()[constants.NodeDrainAnnotation]
	newAnno, hasNewAnno := e.ObjectNew.GetAnnotations()[constants.NodeDrainAnnotation]

	if !hasOldAnno && hasNewAnno {
		return true
	}

	return oldAnno != newAnno
}

type DrainStateAnnotationPredicate struct {
	predicate.Funcs
}

type renderManifestFunc func(string, *render.RenderData) ([]*uns.Unstructured, error)
type applyManifestFunc func(context.Context, k8sclient.Client, *uns.Unstructured) error

func (DrainStateAnnotationPredicate) Create(e event.CreateEvent) bool {
	return e.Object != nil
}

func (DrainStateAnnotationPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		return false
	}
	if e.ObjectNew == nil {
		return false
	}

	oldAnno, hasOldAnno := e.ObjectOld.GetAnnotations()[constants.NodeStateDrainAnnotationCurrent]
	newAnno, hasNewAnno := e.ObjectNew.GetAnnotations()[constants.NodeStateDrainAnnotationCurrent]

	if !hasOldAnno || !hasNewAnno {
		return true
	}

	return oldAnno != newAnno
}

func GetImagePullSecrets() []string {
	imagePullSecrets := os.Getenv("IMAGE_PULL_SECRETS")
	if imagePullSecrets != "" {
		return strings.Split(imagePullSecrets, ",")
	} else {
		return []string{}
	}
}

// GetCNIBinPath returns the host filesystem path for CNI plugin binaries.
// Matches syncConfigDaemonSet (SRIOV_CNI_BIN_PATH env or default /var/lib/cni/bin).
func GetCNIBinPath() string {
	if p := os.Getenv("SRIOV_CNI_BIN_PATH"); p != "" {
		log.Log.V(1).Info("Using custom CNI bin path", "CNIBinPath", p)
		return p
	}
	log.Log.V(1).Info("Using default CNI bin path", "CNIBinPath", defaultCNIBinPath)
	return defaultCNIBinPath
}

func formatJSON(str string) (string, error) {
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, []byte(str), "", "    "); err != nil {
		return "", err
	}
	return prettyJSON.String(), nil
}

// GetDefaultNodeSelector return a nodeSelector with worker and linux os
func GetDefaultNodeSelector() map[string]string {
	return map[string]string{
		"node-role.kubernetes.io/worker": "",
		"kubernetes.io/os":               "linux",
	}
}

// GetDefaultNodeSelectorForDevicePlugin return a nodeSelector with worker linux os
// and the enabled sriov device plugin
func GetNodeSelectorForDevicePlugin(dc *sriovnetworkv1.SriovOperatorConfig) map[string]string {
	if len(dc.Spec.ConfigDaemonNodeSelector) == 0 {
		return map[string]string{
			"kubernetes.io/os":               "linux",
			constants.SriovDevicePluginLabel: constants.SriovDevicePluginLabelEnabled,
		}
	}

	tmp := dc.Spec.DeepCopy()
	tmp.ConfigDaemonNodeSelector[constants.SriovDevicePluginLabel] = constants.SriovDevicePluginLabelEnabled
	return tmp.ConfigDaemonNodeSelector
}

// GetNodeSelectorForDRADriver returns ConfigDaemonNodeSelector when set, otherwise GetDefaultNodeSelector.
// The DRA driver DaemonSet is scheduled on the same nodes as the config daemon and does not add
// SriovDevicePluginLabel (see GetNodeSelectorForDevicePlugin).
func GetNodeSelectorForDRADriver(dc *sriovnetworkv1.SriovOperatorConfig) map[string]string {
	if len(dc.Spec.ConfigDaemonNodeSelector) > 0 {
		return dc.Spec.ConfigDaemonNodeSelector
	}
	return GetDefaultNodeSelector()
}

// renderPluginDaemonObjs is shared by deployment and cleanup so both use the same inventory.
func renderPluginDaemonObjs(dc *sriovnetworkv1.SriovOperatorConfig,
	featureGate featuregate.FeatureGate, renderFn renderManifestFunc) ([]*uns.Unstructured, error) {
	data := render.MakeRenderData()
	data.Data["Namespace"] = vars.Namespace
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["SRIOVNetworkConfigDaemonImage"] = os.Getenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ResourcePrefix"] = vars.ResourcePrefix
	data.Data["ImagePullSecrets"] = GetImagePullSecrets()
	data.Data["NodeSelectorField"] = GetNodeSelectorForDevicePlugin(dc)
	data.Data["UseCDI"] = dc.Spec.UseCDI
	data.Data["BlockDevicePluginUntilConfigured"] = featureGate.IsEnabled(constants.BlockDevicePluginUntilConfiguredFeatureGate)
	return renderDsForCR(constants.PluginPath, &data, renderFn)
}

func syncPluginDaemonObjs(ctx context.Context,
	client k8sclient.Client,
	scheme *runtime.Scheme,
	dc *sriovnetworkv1.SriovOperatorConfig,
	featureGate featuregate.FeatureGate,
	renderFn renderManifestFunc,
	applyFn applyManifestFunc) error {
	logger := log.Log.WithName("syncPluginDaemonObjs")
	logger.V(1).Info("Start to sync sriov daemons objects")

	objs, err := renderPluginDaemonObjs(dc, featureGate, renderFn)
	if err != nil {
		logger.Error(err, "Fail to render SR-IoV manifests")
		return err
	}

	// Sync DaemonSets
	for _, obj := range objs {
		err = syncDsObject(ctx, client, scheme, dc, obj, applyFn)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}

	return nil
}

// renderDRADriverObjs is shared by deployment and cleanup so both use the same inventory.
func renderDRADriverObjs(dc *sriovnetworkv1.SriovOperatorConfig,
	renderFn renderManifestFunc) ([]*uns.Unstructured, error) {
	data := render.MakeRenderData()
	data.Data["Namespace"] = vars.Namespace
	data.Data["SRIOVDRADriverImage"] = os.Getenv("SRIOV_DRA_DRIVER_IMAGE")
	data.Data["SRIOVNetworkConfigDaemonImage"] = os.Getenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ImagePullSecrets"] = GetImagePullSecrets()
	data.Data["NodeSelectorField"] = GetNodeSelectorForDRADriver(dc)

	// DRA driver specific configuration
	draDriverCDIRoot := os.Getenv("DRA_DRIVER_CDI_ROOT")
	if draDriverCDIRoot == "" {
		draDriverCDIRoot = defaultDRADriverCDIRoot
	}
	data.Data["DRADriverCDIRoot"] = draDriverCDIRoot

	return renderDsForCR(constants.DRADriverPath, &data, renderFn)
}

func syncDRADriverObjs(ctx context.Context,
	client k8sclient.Client,
	scheme *runtime.Scheme,
	dc *sriovnetworkv1.SriovOperatorConfig,
	renderFn renderManifestFunc,
	applyFn applyManifestFunc) error {
	logger := log.Log.WithName("syncDRADriverObjs")
	logger.V(1).Info("Start to sync DRA driver objects")

	objs, err := renderDRADriverObjs(dc, renderFn)
	if err != nil {
		logger.Error(err, "Fail to render DRA driver manifests")
		return err
	}

	// Sync DRA driver objects
	for _, obj := range objs {
		err = syncDsObject(ctx, client, scheme, dc, obj, applyFn)
		if err != nil {
			logger.Error(err, "Couldn't sync DRA driver objects")
			return err
		}
	}

	return nil
}

// deleteObservedObject deletes only the object version that passed ownership checks.
// Return conflicts so the next reconciliation reads fresh objects and checks
// ownership again before retrying. An already deleted object needs no further work.
func deleteObservedObject(ctx context.Context, c k8sclient.Client, obj k8sclient.Object) error {
	uid, resourceVersion := obj.GetUID(), obj.GetResourceVersion()
	preconditions := k8sclient.Preconditions{UID: &uid, ResourceVersion: &resourceVersion}
	if err := c.Delete(ctx, obj, preconditions); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete %T %s: %w", obj, k8sclient.ObjectKeyFromObject(obj), err)
	}
	return nil
}

func objectHasOperatorManagedLabel(obj metav1.Object) bool {
	if obj == nil {
		return false
	}
	labels := obj.GetLabels()
	return labels != nil && labels[constants.DRAGeneratedByLabel] == constants.SriovNetworkOperatorIdentifier
}

func mergeOperatorManagedLabels(obj *uns.Unstructured) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	maps.Copy(labels, constants.DRAOperatorLabels)
	obj.SetLabels(labels)
}

// deleteOperatorManagedIgnoringNotFound deletes obj only when it exists and carries the operator management marker.
func deleteOperatorManagedIgnoringNotFound(ctx context.Context, c k8sclient.Client, obj k8sclient.Object) error {
	key := k8sclient.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(k8sclient.Object)
	if err := c.Get(ctx, key, existing); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !objectHasOperatorManagedLabel(existing) {
		log.Log.WithName("deleteOperatorManagedIgnoringNotFound").V(1).Info(
			"Skipping delete of object without operator management marker",
			"Kind", existing.GetObjectKind().GroupVersionKind().Kind, "Name", existing.GetName())
		return nil
	}
	return deleteObservedObject(ctx, c, existing)
}

func hasSupportedProviderOwnerReference(obj metav1.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller == nil || !*ref.Controller || ref.APIVersion != sriovnetworkv1.GroupVersion.String() {
			continue
		}
		switch ref.Kind {
		case "SriovOperatorConfig":
			return ref.Name == constants.DefaultConfigName
		case "SriovNetworkNodePolicy":
			return ref.Name == constants.DefaultPolicyName
		}
	}
	return false
}

// deleteNamespacedProviderObject deletes only a live object owned by this
// operator. The node-policy owner is retained for device-plugin objects
// created by older operator versions.
func deleteNamespacedProviderObject(ctx context.Context, c k8sclient.Client, obj k8sclient.Object) error {
	key := k8sclient.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(k8sclient.Object)
	if err := c.Get(ctx, key, existing); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !hasSupportedProviderOwnerReference(existing) {
		log.Log.WithName("deleteNamespacedProviderObject").V(1).Info(
			"Skipping delete of object without a supported operator owner reference",
			"Kind", existing.GetObjectKind().GroupVersionKind().Kind, "Name", existing.GetName())
		return nil
	}
	return deleteObservedObject(ctx, c, existing)
}

// cleanupDRADriverObjs removes the DRA resources defined by the deployment manifests.
func cleanupDRADriverObjs(ctx context.Context, client k8sclient.Client,
	dc *sriovnetworkv1.SriovOperatorConfig, renderFn renderManifestFunc) error {
	objs, err := renderDRADriverObjs(dc, renderFn)
	if err != nil {
		return err
	}
	return cleanupProviderObjs(ctx, client, objs)
}

// cleanupDevicePluginObjs removes the device plugin resources defined by the deployment manifests.
func cleanupDevicePluginObjs(ctx context.Context, client k8sclient.Client,
	dc *sriovnetworkv1.SriovOperatorConfig, featureGate featuregate.FeatureGate, renderFn renderManifestFunc) error {
	objs, err := renderPluginDaemonObjs(dc, featureGate, renderFn)
	if err != nil {
		return err
	}
	return cleanupProviderObjs(ctx, client, objs)
}

func cleanupProviderObjs(ctx context.Context, client k8sclient.Client, objs []*uns.Unstructured) error {
	logger := log.FromContext(ctx).WithName("cleanupProviderObjs")
	// Request DaemonSet deletion first so a failure preserves its access.
	// Deletion is asynchronous; this does not wait for pods to terminate.
	sort.SliceStable(objs, func(i, j int) bool {
		return objs[i].GetKind() == constants.DaemonSet && objs[j].GetKind() != constants.DaemonSet
	})
	for _, manifest := range objs {
		var obj k8sclient.Object = manifest
		if manifest.GetKind() == deviceClassResourceName {
			obj = &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{Name: manifest.GetName()},
			}
		}
		var err error
		if manifest.GetNamespace() == "" {
			// Cluster-scoped resources cannot be owned by the namespaced operator config.
			// Only delete them when the live object has the operator management marker.
			err = deleteOperatorManagedIgnoringNotFound(ctx, client, obj)
		} else {
			err = deleteNamespacedProviderObject(ctx, client, obj)
		}
		if err != nil {
			if manifest.GetKind() == deviceClassResourceName && apimeta.IsNoMatchError(err) {
				logger.V(1).Info("DeviceClass API not available, skipping cleanup")
				continue
			}
			return fmt.Errorf("cleanup %s %s: %w", manifest.GetKind(), k8sclient.ObjectKeyFromObject(obj), err)
		}
	}
	logger.V(1).Info("Cleaned up provider objects")
	return nil
}

func syncDsObject(ctx context.Context, client k8sclient.Client, scheme *runtime.Scheme, dc *sriovnetworkv1.SriovOperatorConfig, obj *uns.Unstructured, applyFn applyManifestFunc) error {
	logger := log.Log.WithName("syncDsObject")
	kind := obj.GetKind()
	logger.V(1).Info("Start to sync Objects", "Kind", kind)
	switch kind {
	case constants.ServiceAccount, constants.Role, constants.RoleBinding:
		if err := controllerutil.SetControllerReference(dc, obj, scheme); err != nil {
			return err
		}
		if err := applyFn(ctx, client, obj); err != nil {
			logger.Error(err, "Fail to sync", "Kind", kind)
			return err
		}
	case clusterRoleResourceName, clusterRoleBindingResourceName, deviceClassResourceName:
		// Cluster-scoped objects cannot have a controller reference to namespaced dc.
		mergeOperatorManagedLabels(obj)
		if err := applyFn(ctx, client, obj); err != nil {
			if kind == deviceClassResourceName && apimeta.IsNoMatchError(err) {
				return fmt.Errorf("DeviceClass API is unavailable: %w", err)
			}
			logger.Error(err, "Fail to sync", "Kind", kind)
			return err
		}
	case constants.DaemonSet:
		ds := &appsv1.DaemonSet{}
		err := scheme.Convert(obj, ds, nil)
		if err != nil {
			logger.Error(err, "Fail to convert to DaemonSet")
			return err
		}
		err = syncDaemonSet(ctx, client, scheme, dc, ds)
		if err != nil {
			logger.Error(err, "Fail to sync DaemonSet", "Namespace", ds.Namespace, "Name", ds.Name)
			return err
		}
	}
	return nil
}

// renderDsForCR renders daemon objects using the provided renderer.
func renderDsForCR(path string, data *render.RenderData, renderFn renderManifestFunc) ([]*uns.Unstructured, error) {
	logger := log.Log.WithName("renderDsForCR")
	logger.V(1).Info("Start to render objects")

	objs, err := renderFn(path, data)
	if err != nil {
		return nil, errs.Wrap(err, "failed to render SR-IOV Network Operator manifests")
	}
	return objs, nil
}

func syncDaemonSet(ctx context.Context, client k8sclient.Client, scheme *runtime.Scheme, dc *sriovnetworkv1.SriovOperatorConfig, in *appsv1.DaemonSet) error {
	logger := log.Log.WithName("syncDaemonSet")
	logger.V(1).Info("Start to sync DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
	var err error

	if err = controllerutil.SetControllerReference(dc, in, scheme); err != nil {
		return err
	}
	ds := &appsv1.DaemonSet{}
	err = client.Get(ctx, types.NamespacedName{Namespace: in.Namespace, Name: in.Name}, ds)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("Created DaemonSet", in.Namespace, in.Name)
			err = client.Create(ctx, in)
			if err != nil {
				logger.Error(err, "Fail to create Daemonset", "Namespace", in.Namespace, "Name", in.Name)
				return err
			}
		} else {
			logger.Error(err, "Fail to get Daemonset", "Namespace", in.Namespace, "Name", in.Name)
			return err
		}
	} else {
		logger.V(1).Info("DaemonSet already exists, updating")
		// DeepDerivative checks for changes only comparing non-zero fields in the source struct.
		// This skips default values added by the api server.
		// References in https://github.com/kubernetes-sigs/kubebuilder/issues/592#issuecomment-625738183

		// Note(Adrianc): we check Equality of OwnerReference as we changed sriov-device-plugin owner ref
		// from SriovNetworkNodePolicy to SriovOperatorConfig, hence even if there is no change in spec,
		// we need to update the obj's owner reference.

		if equality.Semantic.DeepEqual(in.OwnerReferences, ds.OwnerReferences) &&
			equality.Semantic.DeepDerivative(in.Spec, ds.Spec) {
			logger.V(1).Info("Daemonset spec did not change, not updating")
			return nil
		}
		err = client.Update(ctx, in)
		if err != nil {
			logger.Error(err, "Fail to update DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
			return err
		}
	}
	return nil
}

func updateDaemonsetNodeSelector(obj *uns.Unstructured, nodeSelector map[string]string) error {
	if len(nodeSelector) == 0 {
		return nil
	}

	ds := &appsv1.DaemonSet{}
	scheme := kscheme.Scheme
	err := scheme.Convert(obj, ds, nil)
	if err != nil {
		return fmt.Errorf("failed to convert Unstructured [%s] to DaemonSet: %v", obj.GetName(), err)
	}

	ds.Spec.Template.Spec.NodeSelector = nodeSelector

	err = scheme.Convert(ds, obj, nil)
	if err != nil {
		return fmt.Errorf("failed to convert DaemonSet [%s] to Unstructured: %v", obj.GetName(), err)
	}
	return nil
}

func findNodePoolConfig(ctx context.Context, node *corev1.Node, c k8sclient.Client) (*sriovnetworkv1.SriovNetworkPoolConfig, []corev1.Node, error) {
	logger := log.FromContext(ctx)
	logger.Info("FindNodePoolConfig():")
	// get all the sriov network pool configs
	npcl := &sriovnetworkv1.SriovNetworkPoolConfigList{}
	err := c.List(ctx, npcl)
	if err != nil {
		logger.Error(err, "failed to list sriovNetworkPoolConfig")
		return nil, nil, err
	}

	selectedNpcl := []*sriovnetworkv1.SriovNetworkPoolConfig{}
	nodesInPools := map[string]interface{}{}

	for _, npc := range npcl.Items {
		// OpenShift hardware offload uses MachineConfigs rather than NodeStates.
		if vars.ClusterType == constants.ClusterTypeOpenshift && npc.Spec.OvsHardwareOffloadConfig != nil && npc.Spec.OvsHardwareOffloadConfig.Name != "" {
			continue
		}

		selector, err := npc.GetNodeSelector()
		if err != nil {
			logger.Error(err, "failed to create label selector from nodeSelector", "nodeSelector", npc.Spec.NodeSelector)
			return nil, nil, err
		}

		if selector.Matches(labels.Set(node.Labels)) {
			selectedNpcl = append(selectedNpcl, npc.DeepCopy())
		}

		nodeList := &corev1.NodeList{}
		err = c.List(ctx, nodeList, &k8sclient.ListOptions{LabelSelector: selector})
		if err != nil {
			logger.Error(err, "failed to list all the nodes matching the pool with label selector from nodeSelector",
				"machineConfigPoolName", npc,
				"nodeSelector", npc.Spec.NodeSelector)
			return nil, nil, err
		}

		for _, nodeName := range nodeList.Items {
			nodesInPools[nodeName.Name] = nil
		}
	}

	if len(selectedNpcl) > 1 {
		// don't allow the node to be part of multiple pools
		err = fmt.Errorf("node is part of more then one pool")
		logger.Error(err, "multiple pools founded for a specific node", "numberOfPools", len(selectedNpcl), "pools", selectedNpcl)
		return nil, nil, err
	} else if len(selectedNpcl) == 1 {
		// found one pool for our node
		logger.V(2).Info("found sriovNetworkPool", "pool", *selectedNpcl[0])
		selector, err := selectedNpcl[0].GetNodeSelector()
		if err != nil {
			logger.Error(err, "failed to create label selector from nodeSelector", "nodeSelector", selectedNpcl[0].Spec.NodeSelector)
			return nil, nil, err
		}

		// list all the nodes that are also part of this pool and return them
		nodeList := &corev1.NodeList{}
		err = c.List(ctx, nodeList, &k8sclient.ListOptions{LabelSelector: selector})
		if err != nil {
			logger.Error(err, "failed to list nodes using with label selector", "labelSelector", selector)
			return nil, nil, err
		}

		return selectedNpcl[0], nodeList.Items, nil
	} else {
		// in this case we get all the nodes and remove the ones that already part of any pool
		logger.V(1).Info("node doesn't belong to any pool, using default drain configuration with MaxUnavailable of one", "pool", *defaultPoolConfig)
		nodeList := &corev1.NodeList{}
		err = c.List(ctx, nodeList)
		if err != nil {
			logger.Error(err, "failed to list all the nodes")
			return nil, nil, err
		}

		defaultNodeLists := []corev1.Node{}
		for _, nodeObj := range nodeList.Items {
			if _, exist := nodesInPools[nodeObj.Name]; !exist {
				defaultNodeLists = append(defaultNodeLists, nodeObj)
			}
		}
		return defaultPoolConfig, defaultNodeLists, nil
	}
}

// managedNodes lists managed nodes in name order using the configured daemon selector.
func managedNodes(ctx context.Context, c k8sclient.Client, config *sriovnetworkv1.SriovOperatorConfig) ([]corev1.Node, error) {
	if config == nil {
		return nil, errors.NewNotFound(sriovnetworkv1.Resource("sriovoperatorconfigs"), constants.DefaultConfigName)
	}
	selector := GetDefaultNodeSelector()
	if len(config.Spec.ConfigDaemonNodeSelector) > 0 {
		selector = config.Spec.ConfigDaemonNodeSelector
	}
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes, k8sclient.MatchingLabels(selector)); err != nil {
		return nil, err
	}
	sort.Slice(nodes.Items, func(i, j int) bool { return nodes.Items[i].Name < nodes.Items[j].Name })
	return nodes.Items, nil
}

// nodeStatesByName indexes NodeStates from the operator namespace by node name.
func nodeStatesByName(ctx context.Context, c k8sclient.Client) (map[string]*sriovnetworkv1.SriovNetworkNodeState, error) {
	list := &sriovnetworkv1.SriovNetworkNodeStateList{}
	if err := c.List(ctx, list, k8sclient.InNamespace(vars.Namespace)); err != nil {
		return nil, err
	}
	states := make(map[string]*sriovnetworkv1.SriovNetworkNodeState, len(list.Items))
	for i := range list.Items {
		states[list.Items[i].Name] = &list.Items[i]
	}
	return states, nil
}
