package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func (dr *DrainReconcile) handleNodeIdleNodeStateDrainingOrCompleted(ctx context.Context,
	reqLogger *logr.Logger,
	node *corev1.Node,
	nodeNetworkState *sriovnetworkv1.SriovNetworkNodeState) (ctrl.Result, error) {
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
		// TODO: make this time configurable
		return reconcile.Result{RequeueAfter: constants.DrainControllerRequeueTime}, nil
	}

	// move the node state back to idle
	err = utils.AnnotateObject(ctx, nodeNetworkState, constants.NodeStateDrainAnnotationCurrent, constants.DrainIdle, dr.Client)
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

func (dr *DrainReconcile) handleNodeDrainOrReboot(ctx context.Context,
	reqLogger *logr.Logger,
	node *corev1.Node,
	nodeNetworkState *sriovnetworkv1.SriovNetworkNodeState,
	nodeDrainAnnotation,
	nodeStateDrainAnnotationCurrent string) (ctrl.Result, error) {
	// nothing to do here we need to wait for the node to move back to idle
	if nodeStateDrainAnnotationCurrent == constants.DrainComplete {
		reqLogger.Info("node requested a drain and nodeState is on drain completed nothing todo")
		return ctrl.Result{}, nil
	}

	// we need to start the drain, but first we need to check that we can drain the node
	if nodeStateDrainAnnotationCurrent == constants.DrainIdle {
		result, err := dr.tryDrainNode(ctx, node)
		if err != nil {
			reqLogger.Error(err, "failed to check if we can drain the node")
			return ctrl.Result{}, err
		}

		// in case we need to wait because we just to the max number of draining nodes
		if result != nil {
			return *result, nil
		}
	}

	// Check if we are on a single node, and we require a reboot/full-drain we just return
	fullNodeDrain := nodeDrainAnnotation == constants.RebootRequired
	singleNode := false
	if fullNodeDrain {
		nodeList := &corev1.NodeList{}
		err := dr.Client.List(ctx, nodeList)
		if err != nil {
			reqLogger.Error(err, "failed to list nodes")
			return ctrl.Result{}, err
		}
		if len(nodeList.Items) == 1 {
			reqLogger.Info("drainNode(): FullNodeDrain requested and we are on Single node")
			singleNode = true
		}
	}

	// call the drain function that will also call drain to other platform providers like openshift
	drained, err := dr.drainer.DrainNode(ctx, node, fullNodeDrain, singleNode)
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
		return reconcile.Result{RequeueAfter: constants.DrainControllerRequeueTime}, nil
	}

	// if we manage to drain we label the node state with drain completed and finish
	err = utils.AnnotateObject(ctx, nodeNetworkState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete, dr.Client)
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

func (dr *DrainReconcile) tryDrainNode(ctx context.Context, node *corev1.Node) (*reconcile.Result, error) {
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
		return &reconcile.Result{RequeueAfter: constants.DrainControllerRequeueTime}, nil
	}

	if currentSnns == nil {
		return nil, fmt.Errorf("failed to find sriov network node state for requested node")
	}

	err = utils.AnnotateObject(ctx, currentSnns, constants.NodeStateDrainAnnotationCurrent, constants.Draining, dr.Client)
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
		err = dr.List(ctx, nodeList, &client.ListOptions{LabelSelector: selector})
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
		err = dr.List(ctx, nodeList, &client.ListOptions{LabelSelector: selector})
		if err != nil {
			logger.Error(err, "failed to list nodes using with label selector", "labelSelector", selector)
			return nil, nil, err
		}

		return selectedNpcl[0], nodeList.Items, nil
	} else {
		// in this case we get all the nodes and remove the ones that already part of any pool
		logger.V(1).Info("node doesn't belong to any pool, using default drain configuration with MaxUnavailable of one", "pool", *defaultPoolConfig)
		nodeList := &corev1.NodeList{}
		err = dr.List(ctx, nodeList)
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
