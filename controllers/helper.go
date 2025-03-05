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
	"os"
	"strings"

	errs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apply"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
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
	clusterRoleResourceName               = "ClusterRole"
	clusterRoleBindingResourceName        = "ClusterRoleBinding"
	mutatingWebhookConfigurationCRDName   = "MutatingWebhookConfiguration"
	validatingWebhookConfigurationCRDName = "ValidatingWebhookConfiguration"
	machineConfigCRDName                  = "MachineConfig"
	trueString                            = "true"
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

	oldAnno, hasOldAnno := e.ObjectOld.GetLabels()[constants.NodeStateDrainAnnotationCurrent]
	newAnno, hasNewAnno := e.ObjectNew.GetLabels()[constants.NodeStateDrainAnnotationCurrent]

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

func syncPluginDaemonObjs(ctx context.Context,
	client k8sclient.Client,
	scheme *runtime.Scheme,
	dc *sriovnetworkv1.SriovOperatorConfig) error {
	logger := log.Log.WithName("syncPluginDaemonObjs")
	logger.V(1).Info("Start to sync sriov daemons objects")

	// render plugin manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = vars.Namespace
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ResourcePrefix"] = vars.ResourcePrefix
	data.Data["ImagePullSecrets"] = GetImagePullSecrets()
	data.Data["NodeSelectorField"] = GetNodeSelectorForDevicePlugin(dc)
	data.Data["UseCDI"] = dc.Spec.UseCDI
	objs, err := renderDsForCR(constants.PluginPath, &data)
	if err != nil {
		logger.Error(err, "Fail to render SR-IoV manifests")
		return err
	}

	// Sync DaemonSets
	for _, obj := range objs {
		err = syncDsObject(ctx, client, scheme, dc, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}

	return nil
}

func syncDsObject(ctx context.Context, client k8sclient.Client, scheme *runtime.Scheme, dc *sriovnetworkv1.SriovOperatorConfig, obj *uns.Unstructured) error {
	logger := log.Log.WithName("syncDsObject")
	kind := obj.GetKind()
	logger.V(1).Info("Start to sync Objects", "Kind", kind)
	switch kind {
	case constants.ServiceAccount, constants.Role, constants.RoleBinding:
		if err := controllerutil.SetControllerReference(dc, obj, scheme); err != nil {
			return err
		}
		if err := apply.ApplyObject(ctx, client, obj); err != nil {
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
		// we skip hw offload objects
		if npc.Spec.OvsHardwareOffloadConfig.Name != "" {
			continue
		}

		if npc.Spec.NodeSelector == nil {
			npc.Spec.NodeSelector = &metav1.LabelSelector{}
		}

		selector, err := metav1.LabelSelectorAsSelector(npc.Spec.NodeSelector)
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
		selector, err := metav1.LabelSelectorAsSelector(selectedNpcl[0].Spec.NodeSelector)
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
