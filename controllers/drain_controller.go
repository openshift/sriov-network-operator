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
	"os"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	Scheme         *runtime.Scheme
	recorder       record.EventRecorder
	drainer        drain.DrainInterface
	resourcePrefix string

	nodesInReconcile      map[string]interface{}
	nodesInReconcileMutex sync.Mutex
	drainCheckMutex       sync.Mutex
}

func NewDrainReconcileController(client client.Client, Scheme *runtime.Scheme, recorder record.EventRecorder, platformHelper platforms.Interface) (*DrainReconcile, error) {
	resourcePrefix := os.Getenv("RESOURCE_PREFIX")
	if resourcePrefix == "" {
		return nil, fmt.Errorf("RESOURCE_PREFIX environment variable can't be empty")
	}

	drainer, err := drain.NewDrainer(resourcePrefix, platformHelper)
	if err != nil {
		return nil, err
	}

	return &DrainReconcile{
		client,
		Scheme,
		recorder,
		drainer,
		resourcePrefix,
		map[string]interface{}{},
		sync.Mutex{},
		sync.Mutex{}}, nil
}
func (dr *DrainReconcile) TryLockNode(nodeName string) bool {
	dr.nodesInReconcileMutex.Lock()
	defer dr.nodesInReconcileMutex.Unlock()

	_, exist := dr.nodesInReconcile[nodeName]
	if exist {
		return false
	}

	dr.nodesInReconcile[nodeName] = nil
	return true
}

func (dr *DrainReconcile) unlockNode(nodeName string) {
	dr.nodesInReconcileMutex.Lock()
	defer dr.nodesInReconcileMutex.Unlock()

	delete(dr.nodesInReconcile, nodeName)
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnodestates,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (dr *DrainReconcile) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// try to lock the node to this reconcile loop.
	// if we are not able this means there is another loop already handling this node, so we just exist
	if !dr.TryLockNode(req.Name) {
		return ctrl.Result{}, nil
	}

	// configure logs
	reqLogger := log.FromContext(ctx)
	reqLogger.Info("Reconciling Drain")
	reqLogger.V(2).Info("node locked for drain controller", "nodeName", req.Name)

	// we send to another function so the operator can have a defer function to release the lock
	return dr.reconcile(ctx, req)
}

func (dr *DrainReconcile) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// remove the lock when we exist the function
	defer dr.unlockNode(req.Name)

	req.Namespace = vars.Namespace

	// configure logs
	reqLogger := log.FromContext(ctx)

	// get node object
	node := &corev1.Node{}
	err := dr.Get(ctx, req.NamespacedName, node)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Error(err, "node object doesn't exist", "nodeName", req.Name)
			return ctrl.Result{}, nil
		}

		reqLogger.Error(err, "failed to get node object from api re-queue the request", "nodeName", req.Name)
		return ctrl.Result{}, err
	}

	// get sriovNodeNodeState object
	nodeNetworkState := &sriovnetworkv1.SriovNetworkNodeState{}
	err = dr.Get(ctx, req.NamespacedName, nodeNetworkState)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Error(err, "sriovNetworkNodeState object doesn't exist", "nodeName", req.Name)
			return ctrl.Result{}, nil
		}

		reqLogger.Error(err, "failed to get sriovNetworkNodeState object from api re-queue the request", "nodeName", req.Name)
		return ctrl.Result{}, err
	}

	// create the drain state annotation if it doesn't exist in the sriovNetworkNodeState object
	nodeStateDrainAnnotationCurrent, NodeStateDrainAnnotationCurrentExist := nodeNetworkState.Annotations[constants.NodeStateDrainAnnotationCurrent]
	if !NodeStateDrainAnnotationCurrentExist {
		err = utils.AnnotateObject(nodeNetworkState, constants.NodeStateDrainAnnotationCurrent, constants.DrainIdle, dr.Client)
		if err != nil {
			return ctrl.Result{}, err
		}
		nodeStateDrainAnnotationCurrent = constants.DrainIdle
	}

	// create the drain state annotation if it doesn't exist in the node object
	nodeDrainAnnotation, nodeDrainAnnotationExist := node.Annotations[constants.NodeDrainAnnotation]
	if !nodeDrainAnnotationExist {
		err = utils.AnnotateObject(node, constants.NodeDrainAnnotation, constants.DrainIdle, dr.Client)
		if err != nil {
			return ctrl.Result{}, err
		}
		nodeDrainAnnotation = constants.DrainIdle
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
			completed, err := dr.drainer.CompleteDrainNode(ctx, node)
			if err != nil {
				reqLogger.Error(err, "failed to complete drain on node")
				dr.recorder.Event(nodeNetworkState,
					corev1.EventTypeWarning,
					"DrainController",
					"failed to drain node")
				return ctrl.Result{}, err
			}

			// if we didn't manage to complete the un drain of the node we retry
			if !completed {
				reqLogger.Info("complete drain was not completed re queueing the request")
				dr.recorder.Event(nodeNetworkState,
					corev1.EventTypeWarning,
					"DrainController",
					"node complete drain was not completed")
				return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
			}

			// move the node state back to idle
			err = utils.AnnotateObject(nodeNetworkState, constants.NodeStateDrainAnnotationCurrent, constants.DrainIdle, dr.Client)
			if err != nil {
				reqLogger.Error(err, "failed to annotate node with annotation", "annotation", constants.DrainIdle)
				return ctrl.Result{}, err
			}

			reqLogger.Info("completed the un drain for node")
			dr.recorder.Event(nodeNetworkState,
				corev1.EventTypeWarning,
				"DrainController",
				"node un drain completed")
			return ctrl.Result{}, nil
		}
	} else {
		// this cover the case a node request to drain or reboot

		// nothing to do here we need to wait for the node to move back to idle
		if nodeStateDrainAnnotationCurrent == constants.DrainComplete {
			reqLogger.Info("node requested a drain and nodeState is on drain completed nothing todo")
			return ctrl.Result{}, nil
		}

		// we need to start the drain, but first we need to check that we can drain the node
		if nodeStateDrainAnnotationCurrent == constants.DrainIdle {
			result, err := dr.checkForNodeDrain(ctx, node)
			if err != nil {
				reqLogger.Error(err, "failed to check if we can drain the node")
				return ctrl.Result{}, err
			}

			// in case we need to wait because we just to the max number of draining nodes
			if result != nil {
				return *result, nil
			}
		}

		// class the drain function that will also call drain to other platform providers like openshift
		drained, err := dr.drainer.DrainNode(ctx, node, nodeDrainAnnotation == constants.RebootRequired)
		if err != nil {
			reqLogger.Error(err, "error trying to drain the node")
			dr.recorder.Event(nodeNetworkState,
				corev1.EventTypeWarning,
				"DrainController",
				"failed to drain node")
			return reconcile.Result{}, err
		}

		// if we didn't manage to complete the drain of the node we retry
		if !drained {
			reqLogger.Info("the nodes was not drained re queueing the request")
			dr.recorder.Event(nodeNetworkState,
				corev1.EventTypeWarning,
				"DrainController",
				"node drain operation was not completed")
			return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// if we manage to drain we label the node state with drain completed and finish
		err = utils.AnnotateObject(nodeNetworkState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete, dr.Client)
		if err != nil {
			reqLogger.Error(err, "failed to annotate node with annotation", "annotation", constants.DrainComplete)
			return ctrl.Result{}, err
		}

		reqLogger.Info("node drained successfully")
		dr.recorder.Event(nodeNetworkState,
			corev1.EventTypeWarning,
			"DrainController",
			"node drain completed")
		return ctrl.Result{}, nil
	}

	reqLogger.Error(nil, "unexpected node drain annotation")
	return reconcile.Result{}, fmt.Errorf("unexpected node drain annotation")
}

func (dr *DrainReconcile) checkForNodeDrain(ctx context.Context, node *corev1.Node) (*reconcile.Result, error) {
	// configure logs
	reqLogger := log.FromContext(ctx)
	reqLogger.Info("checkForNodeDrain():")

	//critical section we need to check if we can start the draining
	dr.drainCheckMutex.Lock()
	defer dr.drainCheckMutex.Unlock()

	// find the relevant node pool
	nodePool, nodeList, err := dr.findNodePoolConfig(ctx, node)
	if err != nil {
		reqLogger.Error(err, "failed to find the pool for the requested node")
		return nil, err
	}

	// check how many nodes we can drain in parallel for the specific pool
	maxUnv, err := nodePool.MaxUnavailable(len(nodeList))
	if err != nil {
		reqLogger.Error(err, "failed to calculate max unavailable")
		return nil, err
	}

	current := 0
	snns := &sriovnetworkv1.SriovNetworkNodeState{}

	var currentSnns *sriovnetworkv1.SriovNetworkNodeState
	for _, nodeObj := range nodeList {
		err = dr.Get(ctx, client.ObjectKey{Name: nodeObj.GetName(), Namespace: vars.Namespace}, snns)
		if err != nil {
			if errors.IsNotFound(err) {
				reqLogger.V(2).Info("node doesn't have a sriovNetworkNodePolicy")
				continue
			}
			return nil, err
		}

		if snns.GetName() == node.GetName() {
			currentSnns = snns.DeepCopy()
		}

		if utils.ObjectHasAnnotation(snns, constants.NodeStateDrainAnnotationCurrent, constants.Draining) ||
			utils.ObjectHasAnnotation(snns, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete) {
			current++
		}
	}
	reqLogger.Info("Max node allowed to be draining at the same time", "MaxParallelNodeConfiguration", maxUnv)
	reqLogger.Info("Count of draining", "drainingNodes", current)

	// if maxUnv is zero this means we drain all the nodes in parallel without a limit
	if maxUnv == -1 {
		reqLogger.Info("draining all the nodes in parallel")
	} else if current >= maxUnv {
		// the node requested to be drained, but we are at the limit so we re-enqueue the request
		reqLogger.Info("MaxParallelNodeConfiguration limit reached for draining nodes re-enqueue the request")
		// TODO: make this time configurable
		return &reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if currentSnns == nil {
		return nil, fmt.Errorf("failed to find sriov network node state for requested node")
	}

	err = utils.AnnotateObject(currentSnns, constants.NodeStateDrainAnnotationCurrent, constants.Draining, dr.Client)
	if err != nil {
		reqLogger.Error(err, "failed to annotate node with annotation", "annotation", constants.Draining)
		return nil, err
	}

	return nil, nil
}

func (dr *DrainReconcile) findNodePoolConfig(ctx context.Context, node *corev1.Node) (*sriovnetworkv1.SriovNetworkPoolConfig, []corev1.Node, error) {
	logger := log.FromContext(ctx)
	logger.Info("findNodePoolConfig():")
	// get all the sriov network pool configs
	npcl := &sriovnetworkv1.SriovNetworkPoolConfigList{}
	err := dr.List(ctx, npcl)
	if err != nil {
		logger.Error(err, "failed to list sriovNetworkPoolConfig")
		return nil, nil, err
	}

	selectedNpcl := []*sriovnetworkv1.SriovNetworkPoolConfig{}
	var defaultNpcl *sriovnetworkv1.SriovNetworkPoolConfig

	for _, npc := range npcl.Items {
		// we skip hw offload objects
		if npc.Spec.OvsHardwareOffloadConfig.Name != "" {
			continue
		}

		// we save the default to use it if we don't find any other one
		if npc.GetName() == "default" {
			defaultNpcl = npc.DeepCopy()
			continue
		}

		// if the node selector is empty we skip
		if npc.Spec.NodeSelector == nil {
			continue
		}

		selector, err := metav1.LabelSelectorAsSelector(npc.Spec.NodeSelector)
		if err != nil {
			logger.Error(err, "failed to create label selector from nodeSelector", "nodeSelector", npc.Spec.NodeSelector)
			return nil, nil, err
		}

		// If a pool with a nil or empty selector creeps in, it should match nothing, not everything.
		if selector.Empty() || !selector.Matches(labels.Set(node.Labels)) {
			continue
		}

		selectedNpcl = append(selectedNpcl, npc.DeepCopy())
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
		err = dr.List(ctx, nodeList, &client.ListOptions{LabelSelector: selector})
		if err != nil {
			logger.Error(err, "failed to list nodes using with label selector", "labelSelector", selector)
			return nil, nil, err
		}

		return selectedNpcl[0], nodeList.Items, nil
	} else {
		// we use the default policy if it was found
		if defaultNpcl == nil {
			return nil, nil, fmt.Errorf("failed to find the default sriov network pool config")
		}
		logger.V(2).Info("found sriovNetworkPool", "pool", *defaultNpcl)

		selector, err := metav1.LabelSelectorAsSelector(defaultNpcl.Spec.NodeSelector)
		if err != nil {
			logger.Error(err, "failed to create label selector from nodeSelector", "nodeSelector", defaultNpcl.Spec.NodeSelector)
			return nil, nil, err
		}

		// return the list of nodes
		nodeList := &corev1.NodeList{}
		err = dr.List(ctx, nodeList, &client.ListOptions{LabelSelector: selector})
		if err != nil {
			logger.Error(err, "failed to list nodes using with label selector", "labelSelector", selector)
			return nil, nil, err
		}

		return defaultNpcl, nodeList.Items, nil
	}
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
