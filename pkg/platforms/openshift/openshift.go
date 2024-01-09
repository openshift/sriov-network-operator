package openshift

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mcv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	mcoconsts "github.com/openshift/machine-config-operator/pkg/daemon/constants"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// OpenshiftFlavor holds metadata about the type of Openshift environment the operator is in.
type OpenshiftFlavor string

const (
	// Hypershift flavor of openshift: https://github.com/openshift/hypershift
	OpenshiftFlavorHypershift OpenshiftFlavor = "hypershift"
	// OpenshiftFlavorDefault covers all remaining flavors of openshift not explicitly called out above
	OpenshiftFlavorDefault OpenshiftFlavor = "default"
)

//go:generate ../../../bin/mockgen -destination mock/mock_openshift.go -source openshift.go
type OpenshiftContextInterface interface {
	GetFlavor() OpenshiftFlavor
	IsOpenshiftCluster() bool
	IsHypershift() bool

	OpenshiftDrainNode(context.Context, *corev1.Node) (bool, error)
	OpenshiftCompleteDrainNode(context.Context, *corev1.Node) (bool, error)

	GetNodeMachinePoolName(context.Context, *corev1.Node) (string, error)
	ChangeMachineConfigPoolPause(context.Context, *mcv1.MachineConfigPool, bool) error
}

// OpenshiftContext contains metadata and structs utilized to interact with Openshift clusters
type openshiftContext struct {
	// kubeClient is a generic client
	kubeClient client.Client

	// isOpenShiftCluster boolean to point out if the cluster is an OpenShift cluster
	isOpenShiftCluster bool

	// openshiftFlavor holds metadata about the type of Openshift environment the operator is in.
	openshiftFlavor OpenshiftFlavor

	mcpPauseMutex sync.Mutex
}

func New() (OpenshiftContextInterface, error) {
	if vars.ClusterType != consts.ClusterTypeOpenshift {
		return &openshiftContext{nil, false, "", sync.Mutex{}}, nil
	}

	kubeClient, err := client.New(vars.Config, client.Options{Scheme: vars.Scheme})
	if err != nil {
		return nil, err
	}

	openshiftFlavor := OpenshiftFlavorDefault
	infraClient, err := client.New(vars.Config, client.Options{
		Scheme: vars.Scheme,
	})
	if err != nil {
		return nil, err
	}

	isHypershift, err := utils.IsExternalControlPlaneCluster(infraClient)
	if err != nil {
		return nil, err
	}

	if isHypershift {
		openshiftFlavor = OpenshiftFlavorHypershift
	}

	return &openshiftContext{kubeClient, true, openshiftFlavor, sync.Mutex{}}, nil
}

func (c *openshiftContext) GetFlavor() OpenshiftFlavor {
	return c.openshiftFlavor
}

func (c *openshiftContext) IsOpenshiftCluster() bool {
	return c.isOpenShiftCluster
}

func (c *openshiftContext) IsHypershift() bool {
	return c.openshiftFlavor == OpenshiftFlavorHypershift
}

func (c *openshiftContext) OpenshiftDrainNode(ctx context.Context, node *corev1.Node) (bool, error) {
	// if it's not an openshift cluster we just return true that the operator manage to drain the node
	if !c.IsOpenshiftCluster() {
		return true, nil
	}

	// if the operator is running on hypershift variation of openshift there is no machine config operator
	// just return true here
	if c.IsHypershift() {
		return true, nil
	}

	// get the machine pool name for the requested node
	mcpName, err := c.GetNodeMachinePoolName(ctx, node)
	if err != nil {
		return false, err
	}

	// lock critical section where we check if the machine config pool is already paused or not
	// then we act base on that
	c.mcpPauseMutex.Lock()
	defer c.mcpPauseMutex.Unlock()

	// get the machine config pool that handle the specific node we want to drain
	mcp := &mcv1.MachineConfigPool{}
	err = c.kubeClient.Get(ctx, client.ObjectKey{Name: mcpName}, mcp)
	if err != nil {
		return false, err
	}

	// check if the machine config pool was already paused by the operator
	if utils.ObjectHasAnnotation(mcp,
		consts.MachineConfigPoolPausedAnnotation,
		consts.MachineConfigPoolPausedAnnotationPaused) {
		// check if the machine config pool is really paused
		// if not then we need to check if the machine config operator is doing something for this pool
		if !mcp.Spec.Paused {
			// if the machine config pool needs to update then we return false
			// if they are equal we can pause the pool
			if mcp.Spec.Configuration.Name != mcp.Status.Configuration.Name {
				return false, err
			} else {
				err = c.ChangeMachineConfigPoolPause(ctx, mcp, true)
				if err != nil {
					return false, err
				}
			}
		}
		return true, nil
	}

	// check if the machine config operator is doing something
	// to be sure we can just check that the desired and requested configuration are the same
	if mcp.Spec.Configuration.Name != mcp.Status.Configuration.Name {
		// return false as the machine config operator is applying stuff
		return false, nil
	}

	// now we are going to label the machine config with paused and then pause the machine config
	// we do it in that order to avoid any edge cases where we pause but didn't add our label
	err = utils.AnnotateObject(mcp,
		consts.MachineConfigPoolPausedAnnotation,
		consts.MachineConfigPoolPausedAnnotationPaused,
		c.kubeClient)
	if err != nil {
		return false, err
	}

	err = c.ChangeMachineConfigPoolPause(ctx, mcp, true)
	if err != nil {
		return false, err
	}

	// re-fetch the object to see if we don't need to revert the pause
	mcp = &mcv1.MachineConfigPool{}
	err = c.kubeClient.Get(ctx, client.ObjectKey{Name: mcpName}, mcp)
	if err != nil {
		return false, err
	}

	// machine config operator start updating the nodes, so we just remove the pause
	if mcp.Spec.Configuration.Name != mcp.Status.Configuration.Name {
		err = c.ChangeMachineConfigPoolPause(ctx, mcp, false)
		if err != nil {
			return false, err
		}

		// after we remove the pause we change the label
		err = utils.AnnotateObject(mcp, consts.MachineConfigPoolPausedAnnotation, consts.MachineConfigPoolPausedAnnotationIdle, c.kubeClient)
		if err != nil {
			return false, err
		}

		return false, nil
	}

	// manage to pause the requested machine config pool
	return true, nil
}

func (c *openshiftContext) OpenshiftCompleteDrainNode(ctx context.Context, node *corev1.Node) (bool, error) {
	// if it's not an openshift cluster we just return true that the operator manage to drain the node
	if !c.IsOpenshiftCluster() {
		return true, nil
	}

	// if the operator is running on hypershift variation of openshift there is no machine config operator
	// just return true here
	if c.IsHypershift() {
		return true, nil
	}

	// get the machine pool name for the requested node
	mcpName, err := c.GetNodeMachinePoolName(ctx, node)
	if err != nil {
		return false, err
	}

	// lock critical section where we check if the machine config pool is already paused or not
	// then we act base on that
	c.mcpPauseMutex.Lock()
	defer c.mcpPauseMutex.Unlock()

	// get the machine config pool that handle the specific node we want to drain
	mcp := &mcv1.MachineConfigPool{}
	err = c.kubeClient.Get(ctx, client.ObjectKey{Name: mcpName}, mcp)
	if err != nil {
		return false, err
	}

	// get all the nodes that belong to this machine config pool to validate this is the last node
	// request to complete the drain
	nodesInPool := &corev1.NodeList{}
	selector, err := metav1.LabelSelectorAsSelector(mcp.Spec.NodeSelector)
	if err != nil {
		return false, err
	}

	err = c.kubeClient.List(ctx, nodesInPool, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		return false, err
	}

	for _, nodeInPool := range nodesInPool.Items {
		// we skip our node
		if nodeInPool.GetName() == node.Name {
			continue
		}

		// if there is an annotation here we check if it's something else then idle
		if utils.ObjectHasAnnotationKey(&nodeInPool, consts.NodeDrainAnnotation) &&
			nodeInPool.GetAnnotations()[consts.NodeDrainAnnotation] != consts.DrainIdle {
			// there are other nodes from the machine config pool that are also under configuration, so we just return
			// only the last node in the machine config pool that finish the drain should remove the pause
			return true, nil
		}
	}

	// if we get here this means we are the last node from this machine config pool that complete the drain,
	// so we unpause the pool and remove the label in that order to avoid any race issues
	err = c.ChangeMachineConfigPoolPause(ctx, mcp, false)
	if err != nil {
		return false, err
	}

	// remove the label now that we unpause the machine config pool
	err = utils.AnnotateObject(mcp, consts.MachineConfigPoolPausedAnnotation, consts.MachineConfigPoolPausedAnnotationIdle, c.kubeClient)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (c *openshiftContext) GetNodeMachinePoolName(ctx context.Context, node *corev1.Node) (string, error) {
	desiredConfig, ok := node.Annotations[mcoconsts.DesiredMachineConfigAnnotationKey]
	if !ok {
		log.Log.Error(nil, "getNodeMachinePool(): Failed to find the the desiredConfig Annotation")
		return "", fmt.Errorf("getNodeMachinePool(): Failed to find the the desiredConfig Annotation")
	}

	mc := &mcv1.MachineConfig{}
	err := c.kubeClient.Get(ctx, client.ObjectKey{Name: desiredConfig}, mc)
	if err != nil {
		log.Log.Error(err, "getNodeMachinePool(): Failed to get the desired Machine Config")
		return "", err
	}
	for _, owner := range mc.OwnerReferences {
		if owner.Kind == "MachineConfigPool" {
			return owner.Name, nil
		}
	}

	log.Log.Error(nil, "getNodeMachinePool(): Failed to find the MCP of the node")
	return "", fmt.Errorf("getNodeMachinePool(): Failed to find the MCP of the node")
}

func (c *openshiftContext) ChangeMachineConfigPoolPause(ctx context.Context, mcp *mcv1.MachineConfigPool, pause bool) error {
	log.Log.Info("ChangeMachineConfigPoolPause:()")

	patchString := []byte(fmt.Sprintf(`{"spec":{"paused":%t}}`, pause))
	patch := client.RawPatch(types.MergePatchType, patchString)
	err := c.kubeClient.Patch(ctx, mcp, patch)
	if err != nil {
		return err
	}

	return nil
}
