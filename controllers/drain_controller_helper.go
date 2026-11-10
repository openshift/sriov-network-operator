package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const maxDrainErrorMessages = 10

func (dr *DrainReconcile) handleNodeIdleNodeStateDrainingOrCompleted(ctx context.Context,
	node *corev1.Node,
	nodeNetworkState *sriovnetworkv1.SriovNetworkNodeState) (ctrl.Result, error) {
	reqLogger := ctx.Value(constants.LoggerContextKey).(logr.Logger).WithName("handleNodeIdleNodeStateDrainingOrCompleted")
	completed, err := dr.drainer.CompleteDrainNode(ctx, node)
	if err != nil {
		reqLogger.Error(err, "failed to complete drain on node")
		dr.recorder.Eventf(nodeNetworkState, nil,
			corev1.EventTypeWarning,
			"DrainController",
			"CompleteDrain",
			"failed to drain node")
		return ctrl.Result{}, err
	}

	// if we didn't manage to complete the un drain of the node we retry
	if !completed {
		reqLogger.Info("complete drain was not completed re queueing the request")
		dr.recorder.Eventf(nodeNetworkState, nil,
			corev1.EventTypeWarning,
			"DrainController",
			"CompleteDrain",
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

	// Update drain conditions to idle state
	if err := dr.updateDrainConditions(ctx, nodeNetworkState, sriovnetworkv1.DrainStateIdle, ""); err != nil {
		reqLogger.Error(err, "failed to update drain conditions to idle state")
		return ctrl.Result{}, err
	}

	reqLogger.Info("completed the un drain for node")
	dr.recorder.Eventf(nodeNetworkState, nil,
		corev1.EventTypeWarning,
		"DrainController",
		"CompleteDrain",
		"node un drain completed")
	return ctrl.Result{}, nil
}

func (dr *DrainReconcile) handleNodeDrainOrReboot(ctx context.Context,
	node *corev1.Node,
	nodeNetworkState *sriovnetworkv1.SriovNetworkNodeState,
	nodeDrainAnnotation,
	nodeStateDrainAnnotationCurrent string) (ctrl.Result, error) {
	reqLogger := ctx.Value(constants.LoggerContextKey).(logr.Logger).WithName("handleNodeDrainOrReboot")

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

	drainingCondition := meta.FindStatusCondition(nodeNetworkState.Status.Conditions, sriovnetworkv1.ConditionDraining)
	if nodeStateDrainAnnotationCurrent == constants.Draining &&
		drainingCondition != nil &&
		drainingCondition.Status == metav1.ConditionFalse &&
		drainingCondition.Reason == sriovnetworkv1.ReasonDrainCompleted {
		reqLogger.Info("drain already completed, reconciling stale node state annotation")
		if err := utils.AnnotateObject(ctx, nodeNetworkState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete, dr.Client); err != nil {
			reqLogger.Error(err, "failed to annotate node with annotation", "annotation", constants.DrainComplete)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
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

	// Keep the latest unique drain errors (e.g. PDB violations) so the condition
	// message names the pods blocking the drain without flooding the API server.
	// kubectl drain writes ErrOut from a loop of goroutines, so the callback
	// must serialize map/slice updates and condition patches.
	var drainErrorsMu sync.Mutex
	seenDrainErrors := make(map[string]struct{})
	var drainErrors []string
	onDrainError := func(drainErr error) {
		drainErrorsMu.Lock()
		defer drainErrorsMu.Unlock()
		msg := drainErr.Error()
		if _, seen := seenDrainErrors[msg]; seen {
			return
		}
		seenDrainErrors[msg] = struct{}{}
		drainErrors = append(drainErrors, msg)
		if len(drainErrors) > maxDrainErrorMessages {
			delete(seenDrainErrors, drainErrors[0])
			drainErrors = drainErrors[1:]
		}
		reqLogger.Info("drain encountered error", "error", drainErr)
		if condErr := dr.updateDrainConditions(ctx, nodeNetworkState, sriovnetworkv1.DrainStateDrainingWithErrors, strings.Join(drainErrors, "; ")); condErr != nil {
			reqLogger.Error(condErr, "failed to update drain conditions to degraded state")
		}
	}

	// call the drain function that will also call drain to other platform providers like openshift
	drained, err := dr.drainer.DrainNode(ctx, node, fullNodeDrain, singleNode, onDrainError)
	if err != nil {
		reqLogger.Error(err, "error trying to drain the node")
		dr.recorder.Eventf(nodeNetworkState, nil,
			corev1.EventTypeWarning,
			"DrainController",
			"DrainNode",
			"failed to drain node")
		errorMessage := strings.Join(drainErrors, "; ")
		if errorMessage == "" {
			errorMessage = err.Error()
		}
		if condErr := dr.updateDrainConditions(ctx, nodeNetworkState, sriovnetworkv1.DrainStateDrainingWithErrors, errorMessage); condErr != nil {
			reqLogger.Error(condErr, "failed to update drain conditions to degraded state")
		}
		return reconcile.Result{}, err
	}

	// if we didn't manage to complete the drain of the node we retry
	if !drained {
		reqLogger.Info("the nodes was not drained re queueing the request")
		dr.recorder.Eventf(nodeNetworkState, nil,
			corev1.EventTypeWarning,
			"DrainController",
			"DrainNode",
			"node drain operation was not completed")
		return reconcile.Result{RequeueAfter: constants.DrainControllerRequeueTime}, nil
	}

	// Update drain conditions to completed state
	// this needs to be done before we annotate the node state with drain completed
	if err := dr.updateDrainConditions(ctx, nodeNetworkState, sriovnetworkv1.DrainStateComplete, ""); err != nil {
		reqLogger.Error(err, "failed to update drain conditions to completed state")
		return ctrl.Result{}, err
	}

	// if we manage to drain we label the node state with drain completed and finish
	err = utils.AnnotateObject(ctx, nodeNetworkState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete, dr.Client)
	if err != nil {
		reqLogger.Error(err, "failed to annotate node with annotation", "annotation", constants.DrainComplete)
		return ctrl.Result{}, err
	}

	reqLogger.Info("node drained successfully")
	dr.recorder.Eventf(nodeNetworkState, nil,
		corev1.EventTypeWarning,
		"DrainController",
		"DrainNode",
		"node drain completed")
	return ctrl.Result{}, nil
}

func (dr *DrainReconcile) tryDrainNode(ctx context.Context, node *corev1.Node) (*reconcile.Result, error) {
	reqLogger := ctx.Value(constants.LoggerContextKey).(logr.Logger).WithName("tryDrainNode")

	//critical section we need to check if we can start the draining
	dr.drainCheckMutex.Lock()
	defer dr.drainCheckMutex.Unlock()

	// find the relevant node pool
	nodePool, nodeList, err := findNodePoolConfig(log.IntoContext(ctx, reqLogger), node, dr.Client)
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
				reqLogger.V(2).Info("node doesn't have a sriovNetworkNodeState, skipping")
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

	if currentSnns == nil {
		return nil, fmt.Errorf("failed to find sriov network node state for requested node")
	}

	if utils.ObjectHasAnnotation(currentSnns, constants.NodeStateDrainAnnotationCurrent, constants.Draining) ||
		utils.ObjectHasAnnotation(currentSnns, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete) {
		reqLogger.Info("node drain already transitioned while waiting for drain slot check",
			"current-state", currentSnns.GetAnnotations()[constants.NodeStateDrainAnnotationCurrent])
		return nil, nil
	}

	// if maxUnv is zero this means we drain all the nodes in parallel without a limit
	if maxUnv == -1 {
		reqLogger.Info("draining all the nodes in parallel")
	} else if current >= maxUnv {
		if err := dr.updateDrainConditions(ctx, currentSnns, sriovnetworkv1.DrainStatePending, "Waiting for an available drain slot"); err != nil {
			reqLogger.Error(err, "failed to update drain conditions to pending state")
			return nil, err
		}
		// the node requested to be drained, but we are at the limit so we re-enqueue the request
		reqLogger.Info("MaxParallelNodeConfiguration limit reached for draining nodes re-enqueue the request")
		// TODO: make this time configurable
		return &reconcile.Result{RequeueAfter: constants.DrainControllerRequeueTime}, nil
	}

	err = utils.AnnotateObject(ctx, currentSnns, constants.NodeStateDrainAnnotationCurrent, constants.Draining, dr.Client)
	if err != nil {
		reqLogger.Error(err, "failed to annotate node with annotation", "annotation", constants.Draining)
		return nil, err
	}

	// Update drain conditions to draining state - use currentSnns for consistency
	if err := dr.updateDrainConditions(ctx, currentSnns, sriovnetworkv1.DrainStateDraining, ""); err != nil {
		reqLogger.Error(err, "failed to update drain conditions to draining state")
		return nil, err
	}

	return nil, nil
}

// updateDrainConditions updates the drain-related conditions on the SriovNetworkNodeState
func (dr *DrainReconcile) updateDrainConditions(ctx context.Context, nodeNetworkState *sriovnetworkv1.SriovNetworkNodeState, state sriovnetworkv1.DrainState, errorMessage string) error {
	reqLogger := ctx.Value(constants.LoggerContextKey).(logr.Logger).WithName("updateDrainConditions")

	// Get the latest version of the nodeNetworkState
	latestState := &sriovnetworkv1.SriovNetworkNodeState{}
	if err := dr.Get(ctx, client.ObjectKey{Namespace: nodeNetworkState.Namespace, Name: nodeNetworkState.Name}, latestState); err != nil {
		if errors.IsNotFound(err) {
			reqLogger.V(2).Info("node state no longer exists, skipping drain condition update")
			return nil
		}
		return fmt.Errorf("failed to fetch latest SriovNetworkNodeState %s/%s: %w",
			nodeNetworkState.Namespace, nodeNetworkState.Name, err)
	}

	conditionState := latestState.DeepCopy()
	conditionState.SetNodeStateDrainConditions(state, errorMessage)

	drainingCondition := meta.FindStatusCondition(conditionState.Status.Conditions, sriovnetworkv1.ConditionDraining)
	if drainingCondition == nil {
		return nil
	}

	if err := dr.StatusPatcher.ApplyCondition(ctx, latestState, *drainingCondition); err != nil {
		reqLogger.Error(err, "failed to update drain conditions")
		return err
	}

	reqLogger.V(2).Info("updated drain conditions", "state", state)
	return nil
}
