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

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apply"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var webhooks = map[string](string){
	constants.InjectorWebHookName: constants.InjectorWebHookPath,
	constants.OperatorWebHookName: constants.OperatorWebHookPath,
}

const (
	clusterRoleResourceName               = "ClusterRole"
	clusterRoleBindingResourceName        = "ClusterRoleBinding"
	mutatingWebhookConfigurationCRDName   = "MutatingWebhookConfiguration"
	validatingWebhookConfigurationCRDName = "ValidatingWebhookConfiguration"
	machineConfigCRDName                  = "MachineConfig"
	trueString                            = "true"
)

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

func GetDefaultNodeSelector() map[string]string {
	return map[string]string{"node-role.kubernetes.io/worker": "",
		"kubernetes.io/os": "linux"}
}

// hasNoValidPolicy returns true if no SriovNetworkNodePolicy
// or only the (deprecated) "default" policy is present
func hasNoValidPolicy(pl []sriovnetworkv1.SriovNetworkNodePolicy) bool {
	switch len(pl) {
	case 0:
		return true
	case 1:
		return pl[0].Name == constants.DefaultPolicyName
	default:
		return false
	}
}

func syncPluginDaemonObjs(ctx context.Context,
	client k8sclient.Client,
	scheme *runtime.Scheme,
	dc *sriovnetworkv1.SriovOperatorConfig,
	pl *sriovnetworkv1.SriovNetworkNodePolicyList) error {
	logger := log.Log.WithName("syncPluginDaemonObjs")
	logger.V(1).Info("Start to sync sriov daemons objects")

	// render plugin manifests
	data := render.MakeRenderData()
	data.Data["Namespace"] = vars.Namespace
	data.Data["SRIOVDevicePluginImage"] = os.Getenv("SRIOV_DEVICE_PLUGIN_IMAGE")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["ResourcePrefix"] = os.Getenv("RESOURCE_PREFIX")
	data.Data["ImagePullSecrets"] = GetImagePullSecrets()
	data.Data["NodeSelectorField"] = GetDefaultNodeSelector()
	data.Data["UseCDI"] = dc.Spec.UseCDI
	objs, err := renderDsForCR(constants.PluginPath, &data)
	if err != nil {
		logger.Error(err, "Fail to render SR-IoV manifests")
		return err
	}

	if hasNoValidPolicy(pl.Items) {
		for _, obj := range objs {
			err := deleteK8sResource(ctx, client, obj)
			if err != nil {
				return err
			}
		}
		return nil
	}

	// Sync DaemonSets
	for _, obj := range objs {
		if obj.GetKind() == constants.DaemonSet && len(dc.Spec.ConfigDaemonNodeSelector) > 0 {
			scheme := kscheme.Scheme
			ds := &appsv1.DaemonSet{}
			err = scheme.Convert(obj, ds, nil)
			if err != nil {
				logger.Error(err, "Fail to convert to DaemonSet")
				return err
			}
			ds.Spec.Template.Spec.NodeSelector = dc.Spec.ConfigDaemonNodeSelector
			err = scheme.Convert(ds, obj, nil)
			if err != nil {
				logger.Error(err, "Fail to convert to Unstructured")
				return err
			}
		}
		err = syncDsObject(ctx, client, scheme, dc, pl, obj)
		if err != nil {
			logger.Error(err, "Couldn't sync SR-IoV daemons objects")
			return err
		}
	}

	return nil
}

func deleteK8sResource(ctx context.Context, client k8sclient.Client, in *uns.Unstructured) error {
	if err := apply.DeleteObject(ctx, client, in); err != nil {
		return fmt.Errorf("failed to delete object %v with err: %v", in, err)
	}
	return nil
}

func syncDsObject(ctx context.Context, client k8sclient.Client, scheme *runtime.Scheme, dc *sriovnetworkv1.SriovOperatorConfig, pl *sriovnetworkv1.SriovNetworkNodePolicyList, obj *uns.Unstructured) error {
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
		err = syncDaemonSet(ctx, client, scheme, dc, pl, ds)
		if err != nil {
			logger.Error(err, "Fail to sync DaemonSet", "Namespace", ds.Namespace, "Name", ds.Name)
			return err
		}
	}
	return nil
}

func syncDaemonSet(ctx context.Context, client k8sclient.Client, scheme *runtime.Scheme, dc *sriovnetworkv1.SriovOperatorConfig, pl *sriovnetworkv1.SriovNetworkNodePolicyList, in *appsv1.DaemonSet) error {
	logger := log.Log.WithName("syncDaemonSet")
	logger.V(1).Info("Start to sync DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
	var err error

	if pl != nil {
		if err = setDsNodeAffinity(pl, in); err != nil {
			return err
		}
	}
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
			// DeepDerivative has issue detecting nodeAffinity change
			// https://bugzilla.redhat.com/show_bug.cgi?id=1914066
			// DeepDerivative doesn't detect changes in containers args section
			// This code should be fixed both with NodeAffinity comparation
			if equality.Semantic.DeepEqual(in.Spec.Template.Spec.Affinity.NodeAffinity,
				ds.Spec.Template.Spec.Affinity.NodeAffinity) &&
				equality.Semantic.DeepEqual(in.Spec.Template.Spec.Containers[0].Args,
					ds.Spec.Template.Spec.Containers[0].Args) {
				logger.V(1).Info("Daemonset spec did not change, not updating")
				return nil
			}
		}
		err = client.Update(ctx, in)
		if err != nil {
			logger.Error(err, "Fail to update DaemonSet", "Namespace", in.Namespace, "Name", in.Name)
			return err
		}
	}
	return nil
}
