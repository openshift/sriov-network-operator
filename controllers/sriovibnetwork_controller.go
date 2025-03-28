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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

// SriovIBNetworkReconciler reconciles a SriovIBNetwork object
type SriovIBNetworkReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	genericReconciler *genericNetworkReconciler
}

//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovibnetworks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovibnetworks/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovibnetworks/finalizers,verbs=update

// Reconcile loop for SriovIBNetwork CRs
func (r *SriovIBNetworkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.genericReconciler.Reconcile(ctx, req)
}

// return name of the controller
func (r *SriovIBNetworkReconciler) Name() string {
	return "SriovIBNetwork"
}

// return empty instance of the SriovIBNetwork CR
func (r *SriovIBNetworkReconciler) GetObject() NetworkCRInstance {
	return &sriovnetworkv1.SriovIBNetwork{}
}

// return empty list of the SriovIBNetwork CRs
func (r *SriovIBNetworkReconciler) GetObjectList() client.ObjectList {
	return &sriovnetworkv1.SriovIBNetworkList{}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SriovIBNetworkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.genericReconciler = newGenericNetworkReconciler(r.Client, r.Scheme, r)
	return r.genericReconciler.SetupWithManager(mgr)
}
