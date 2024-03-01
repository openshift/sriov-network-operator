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
	"reflect"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type networkCRInstance interface {
	client.Object
	// renders NetAttDef from the network instance
	RenderNetAttDef() (*uns.Unstructured, error)
	// return name of the target namespace for the network
	NetworkNamespace() string
}

// interface which controller should implement to be compatible with genericNetworkReconciler
type networkController interface {
	reconcile.Reconciler
	// GetObject should return CR type which implements networkCRInstance
	// interface
	GetObject() networkCRInstance
	// should return CR list type
	GetObjectList() client.ObjectList
	// should return name of the controller
	Name() string
}

func newGenericNetworkReconciler(c client.Client, s *runtime.Scheme, controller networkController) *genericNetworkReconciler {
	return &genericNetworkReconciler{Client: c, Scheme: s, controller: controller}
}

// genericNetworkReconciler provide common code for all network controllers
type genericNetworkReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	controller networkController
}

func (r *genericNetworkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	req.Namespace = vars.Namespace
	reqLogger := log.FromContext(ctx).WithValues(r.controller.Name(), req.NamespacedName)

	reqLogger.Info("Reconciling " + r.controller.Name())
	var err error

	// Fetch instance of the network object
	instance := r.controller.GetObject()
	err = r.Get(ctx, req.NamespacedName, instance)
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
	instanceFinalizers := instance.GetFinalizers()
	// examine DeletionTimestamp to determine if object is under deletion
	if instance.GetDeletionTimestamp().IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !sriovnetworkv1.StringInArray(sriovnetworkv1.NETATTDEFFINALIZERNAME, instanceFinalizers) {
			instance.SetFinalizers(append(instanceFinalizers, sriovnetworkv1.NETATTDEFFINALIZERNAME))
			if err := r.Update(ctx, instance); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if sriovnetworkv1.StringInArray(sriovnetworkv1.NETATTDEFFINALIZERNAME, instanceFinalizers) {
			// our finalizer is present, so lets handle any external dependency
			reqLogger.Info("delete NetworkAttachmentDefinition CR", "Namespace", instance.NetworkNamespace(), "Name", instance.GetName())
			if err := r.deleteNetAttDef(ctx, instance); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return reconcile.Result{}, err
			}
			// remove our finalizer from the list and update it.
			newFinalizers, found := sriovnetworkv1.RemoveString(sriovnetworkv1.NETATTDEFFINALIZERNAME, instanceFinalizers)
			if found {
				instance.SetFinalizers(newFinalizers)
				if err := r.Update(ctx, instance); err != nil {
					return reconcile.Result{}, err
				}
			}
		}
		return reconcile.Result{}, err
	}
	raw, err := instance.RenderNetAttDef()
	if err != nil {
		return reconcile.Result{}, err
	}
	netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
	err = r.Scheme.Convert(raw, netAttDef, nil)
	if err != nil {
		return reconcile.Result{}, err
	}
	// format CNI config json in CR for easier readability
	netAttDef.Spec.Config, err = formatJSON(netAttDef.Spec.Config)
	if err != nil {
		reqLogger.Error(err, "Couldn't process rendered NetworkAttachmentDefinition config", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
		return reconcile.Result{}, err
	}
	if lnns, ok := instance.GetAnnotations()[sriovnetworkv1.LASTNETWORKNAMESPACE]; ok && netAttDef.GetNamespace() != lnns {
		err = r.Delete(ctx, &netattdefv1.NetworkAttachmentDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instance.GetName(),
				Namespace: lnns,
			},
		})
		if err != nil {
			reqLogger.Error(err, "Couldn't delete NetworkAttachmentDefinition CR", "Namespace", instance.GetName(), "Name", lnns)
			return reconcile.Result{}, err
		}
	}
	// Check if this NetworkAttachmentDefinition already exists
	found := &netattdefv1.NetworkAttachmentDefinition{}
	err = r.Get(ctx, types.NamespacedName{Name: netAttDef.Name, Namespace: netAttDef.Namespace}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			targetNamespace := &corev1.Namespace{}
			err = r.Get(ctx, types.NamespacedName{Name: netAttDef.Namespace}, targetNamespace)
			if errors.IsNotFound(err) {
				reqLogger.Info("Target namespace doesn't exist, NetworkAttachmentDefinition will be created when namespace is available", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
				return reconcile.Result{}, nil
			}

			reqLogger.Info("NetworkAttachmentDefinition CR not exist, creating")
			err = r.Create(ctx, netAttDef)
			if err != nil {
				reqLogger.Error(err, "Couldn't create NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
				return reconcile.Result{}, err
			}
			anno := map[string]string{sriovnetworkv1.LASTNETWORKNAMESPACE: netAttDef.Namespace}
			instance.SetAnnotations(anno)
			if err := r.Update(ctx, instance); err != nil {
				return reconcile.Result{}, err
			}
		} else {
			reqLogger.Error(err, "Couldn't get NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("NetworkAttachmentDefinition CR already exist")
		if !reflect.DeepEqual(found.Spec, netAttDef.Spec) || !reflect.DeepEqual(found.GetAnnotations(), netAttDef.GetAnnotations()) {
			reqLogger.Info("Update NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
			netAttDef.SetResourceVersion(found.GetResourceVersion())
			err = r.Update(ctx, netAttDef)
			if err != nil {
				reqLogger.Error(err, "Couldn't update NetworkAttachmentDefinition CR", "Namespace", netAttDef.Namespace, "Name", netAttDef.Name)
				return reconcile.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *genericNetworkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Reconcile when the target namespace is created after the network object.
	namespaceHandler := handler.Funcs{
		CreateFunc: r.namespaceHandlerCreate,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(r.controller.GetObject()).
		Watches(&netattdefv1.NetworkAttachmentDefinition{}, &handler.EnqueueRequestForObject{}).
		Watches(&corev1.Namespace{}, &namespaceHandler).
		Complete(r.controller)
}

func (r *genericNetworkReconciler) namespaceHandlerCreate(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
	networkList := r.controller.GetObjectList()
	err := r.List(ctx,
		networkList,
		client.MatchingFields{"spec.networkNamespace": e.Object.GetName()},
	)
	logger := log.Log.WithName(r.controller.Name() + " reconciler")
	if err != nil {
		logger.Info("Can't list networks for namespace", "resource", e.Object.GetName(), "error", err)
		return
	}
	unsContent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(networkList)
	if err != nil {
		logger.Info("Can't convert network list to unstructured object", "resource", e.Object.GetName(), "error", err)
		return
	}
	unsList := &uns.Unstructured{}
	unsList.SetUnstructuredContent(unsContent)
	_ = unsList.EachListItem(func(o runtime.Object) error {
		unsObj := o.(*uns.Unstructured)
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: unsObj.GetNamespace(),
			Name:      unsObj.GetName(),
		}})
		return nil
	})
}

// deleteNetAttDef deletes the generated net-att-def CR
func (r *genericNetworkReconciler) deleteNetAttDef(ctx context.Context, cr networkCRInstance) error {
	// Fetch the NetworkAttachmentDefinition instance
	namespace := cr.NetworkNamespace()
	if namespace == "" {
		namespace = cr.GetNamespace()
	}
	instance := &netattdefv1.NetworkAttachmentDefinition{ObjectMeta: metav1.ObjectMeta{Name: cr.GetName(), Namespace: namespace}}
	err := r.Delete(ctx, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}
