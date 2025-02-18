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
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/drain"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type DrainReconcile struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
	drainer  drain.DrainInterface

	drainCheckMutex sync.Mutex
}

func NewDrainReconcileController(client client.Client, Scheme *runtime.Scheme, recorder record.EventRecorder, platformHelper platforms.Interface) (*DrainReconcile, error) {
	drainer, err := drain.NewDrainer(platformHelper)
	if err != nil {
		return nil, err
	}

	return &DrainReconcile{
		client,
		Scheme,
		recorder,
		drainer,
		sync.Mutex{}}, nil
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnodestates,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (dr *DrainReconcile) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx)
	reqLogger.Info("Reconciling Drain")

	req.Namespace = vars.Namespace

	// get node object
	node := &corev1.Node{}
	found, err := dr.getObject(ctx, req, node)
	if err != nil {
		reqLogger.Error(err, "failed to get node object")
		return ctrl.Result{}, err
	}
	if !found {
		reqLogger.Info("node not found don't, requeue the request")
		return ctrl.Result{}, nil
	}

	// get sriovNodeNodeState object
	nodeNetworkState := &sriovnetworkv1.SriovNetworkNodeState{}
	found, err = dr.getObject(ctx, req, nodeNetworkState)
	if err != nil {
		reqLogger.Error(err, "failed to get sriovNetworkNodeState object")
		return ctrl.Result{}, err
	}
	if !found {
		reqLogger.Info("sriovNetworkNodeState not found, don't requeue the request")
		return ctrl.Result{}, nil
	}

	// create the drain state annotation if it doesn't exist in the sriovNetworkNodeState object
	nodeStateDrainAnnotationCurrent, currentNodeStateExist, err := dr.ensureAnnotationExists(ctx, nodeNetworkState, constants.NodeStateDrainAnnotationCurrent)
	if err != nil {
		reqLogger.Error(err, "failed to ensure nodeStateDrainAnnotationCurrent")
		return ctrl.Result{}, err
	}
	_, desireNodeStateExist, err := dr.ensureAnnotationExists(ctx, nodeNetworkState, constants.NodeStateDrainAnnotation)
	if err != nil {
		reqLogger.Error(err, "failed to ensure nodeStateDrainAnnotation")
		return ctrl.Result{}, err
	}

	// create the drain state annotation if it doesn't exist in the node object
	nodeDrainAnnotation, nodeExist, err := dr.ensureAnnotationExists(ctx, node, constants.NodeDrainAnnotation)
	if err != nil {
		reqLogger.Error(err, "failed to ensure nodeStateDrainAnnotation")
		return ctrl.Result{}, err
	}

	// requeue the request if we needed to add any of the annotations
	if !nodeExist || !currentNodeStateExist || !desireNodeStateExist {
		return ctrl.Result{Requeue: true}, nil
	}
	reqLogger.V(2).Info("Drain annotations", "nodeAnnotation", nodeDrainAnnotation, "nodeStateAnnotation", nodeStateDrainAnnotationCurrent)

	// Check the node request
	if nodeDrainAnnotation == constants.DrainIdle {
		// this cover the case the node is on idle

		// node request to be on idle and the currect state is idle
		// we don't do anything
		if nodeStateDrainAnnotationCurrent == constants.DrainIdle {
			reqLogger.Info("node and nodeState are on idle nothing todo")
			return reconcile.Result{}, nil
		}

		// we have two options here:
		// 1. node request idle and the current status is drain complete
		// this means the daemon finish is work, so we need to clean the drain
		//
		// 2. the operator is still draining the node but maybe the sriov policy changed and the daemon
		//  doesn't need to drain anymore, so we can stop the drain
		if nodeStateDrainAnnotationCurrent == constants.DrainComplete ||
			nodeStateDrainAnnotationCurrent == constants.Draining {
			return dr.handleNodeIdleNodeStateDrainingOrCompleted(ctx, &reqLogger, node, nodeNetworkState)
		}
	}

	// this cover the case a node request to drain or reboot
	if nodeDrainAnnotation == constants.DrainRequired ||
		nodeDrainAnnotation == constants.RebootRequired {
		return dr.handleNodeDrainOrReboot(ctx, &reqLogger, node, nodeNetworkState, nodeDrainAnnotation, nodeStateDrainAnnotationCurrent)
	}

	reqLogger.Error(nil, "unexpected node drain annotation")
	return reconcile.Result{}, fmt.Errorf("unexpected node drain annotation")
}

func (dr *DrainReconcile) getObject(ctx context.Context, req ctrl.Request, object client.Object) (bool, error) {
	err := dr.Get(ctx, req.NamespacedName, object)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (dr *DrainReconcile) ensureAnnotationExists(ctx context.Context, object client.Object, key string) (string, bool, error) {
	value, exist := object.GetAnnotations()[key]
	if !exist {
		err := utils.AnnotateObject(ctx, object, key, constants.DrainIdle, dr.Client)
		if err != nil {
			return "", false, err
		}
		return constants.DrainIdle, false, nil
	}

	return value, true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (dr *DrainReconcile) SetupWithManager(mgr ctrl.Manager) error {
	createUpdateEnqueue := handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: vars.Namespace,
				Name:      e.Object.GetName(),
			}})
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: vars.Namespace,
				Name:      e.ObjectNew.GetName(),
			}})
		},
	}

	// Watch for spec and annotation changes
	nodePredicates := builder.WithPredicates(DrainAnnotationPredicate{})
	nodeStatePredicates := builder.WithPredicates(DrainStateAnnotationPredicate{})

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 50}).
		For(&corev1.Node{}, nodePredicates).
		Watches(&sriovnetworkv1.SriovNetworkNodeState{}, createUpdateEnqueue, nodeStatePredicates).
		Complete(dr)
}
