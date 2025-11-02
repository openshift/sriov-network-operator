package openshift

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	mcv1 "github.com/openshift/api/machineconfiguration/v1"
	mcoconsts "github.com/openshift/machine-config-operator/pkg/daemon/constants"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	// default Infrastructure resource name for Openshift
	infraResourceName = "cluster"
)

// OpenshiftOrchestrator implements the orchestrator.Interface for OpenShift clusters.
// It contains metadata and structs utilized to interact with OpenShift clusters,
// including MachineConfigPool management for safe node draining.
type OpenshiftOrchestrator struct {
	// openshiftFlavor holds metadata about the type of Openshift environment the operator is in.
	openshiftFlavor consts.ClusterFlavor

	// kubeClient is a generic client
	kubeClient client.Client

	// mcpPauseMutex holds the mutex to change machine config pause state
	mcpPauseMutex sync.Mutex
}

// New creates a new OpenshiftOrchestrator orchestrator instance.
// Detects whether the cluster is Hypershift or standard OpenShift by checking the Infrastructure resource.
// Returns a configured OpenshiftOrchestrator or an error if initialization fails.
func New() (*OpenshiftOrchestrator, error) {
	kubeClient, err := client.New(vars.Config, client.Options{Scheme: vars.Scheme})
	if err != nil {
		return nil, err
	}

	openshiftFlavor := consts.ClusterFlavorDefault
	isHypershift, err := isExternalControlPlaneCluster(kubeClient)
	if err != nil {
		return nil, err
	}

	if isHypershift {
		openshiftFlavor = consts.ClusterFlavorHypershift
	}

	return &OpenshiftOrchestrator{openshiftFlavor, kubeClient, sync.Mutex{}}, nil
}

// Name returns the name of the OpenShift orchestrator.
func (c *OpenshiftOrchestrator) Name() string {
	return "OpenShift"
}

// ClusterType returns the cluster type for OpenShift.
func (c *OpenshiftOrchestrator) ClusterType() consts.ClusterType {
	return consts.ClusterTypeOpenshift
}

// Flavor returns the OpenShift cluster flavor (standard or Hypershift).
func (c *OpenshiftOrchestrator) Flavor() consts.ClusterFlavor {
	return c.openshiftFlavor
}

// BeforeDrainNode pauses the node's MachineConfigPool before draining to prevent automatic reboots.
// For Hypershift clusters, returns true immediately as there is no MachineConfigOperator.
// Returns true if the MCP was successfully paused and drain can proceed, false if more time is needed.
func (c *OpenshiftOrchestrator) BeforeDrainNode(ctx context.Context, node *corev1.Node) (bool, error) {
	// if the operator is running on hypershift variation of openshift there is no machine config operator
	// just return true here
	if c.Flavor() == consts.ClusterFlavorHypershift {
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
			if mcp.Spec.Configuration.Name == "" || mcp.Status.Configuration.Name == "" ||
				mcp.Spec.Configuration.Name != mcp.Status.Configuration.Name {
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
	err = utils.AnnotateObject(ctx, mcp,
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
		err = utils.AnnotateObject(ctx, mcp, consts.MachineConfigPoolPausedAnnotation, consts.MachineConfigPoolPausedAnnotationIdle, c.kubeClient)
		if err != nil {
			return false, err
		}

		return false, nil
	}

	// manage to pause the requested machine config pool
	return true, nil
}

// AfterCompleteDrainNode unpauses the node's MachineConfigPool after drain is complete.
// For Hypershift clusters, returns true immediately as there is no MachineConfigOperator.
// Only unpauses the MCP if this is the last node in the pool completing the drain.
// Returns true if the MCP was successfully unpaused or if no unpause was needed, false if more time is needed.
func (c *OpenshiftOrchestrator) AfterCompleteDrainNode(ctx context.Context, node *corev1.Node) (bool, error) {
	// if the operator is running on hypershift variation of openshift, there is no machine config operator
	// just return true here
	if c.Flavor() == consts.ClusterFlavorHypershift {
		return true, nil
	}

	// get the machine pool name for the requested node
	mcpName, err := c.GetNodeMachinePoolName(ctx, node)
	if err != nil {
		return false, err
	}

	// lock critical section where we check if the machine config pool is already paused, or not
	// then we act base on that
	c.mcpPauseMutex.Lock()
	defer c.mcpPauseMutex.Unlock()

	// get the machine config pool that handles the specific node we want to drain
	mcp := &mcv1.MachineConfigPool{}
	err = c.kubeClient.Get(ctx, client.ObjectKey{Name: mcpName}, mcp)
	if err != nil {
		return false, err
	}

	value, exist := mcp.Annotations[consts.MachineConfigPoolPausedAnnotation]
	// if the label doesn't exist we just return true here
	// this can be a case where the node was moved to another MCP in the time we start the drain
	if !exist {
		return true, nil
	}
	// check if the sriov annotation on mcp is idle
	// if the value is idle we just return here
	if value == consts.MachineConfigPoolPausedAnnotationIdle {
		return true, nil
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

	// if we get here, this means we are the last node from this machine config pool that completes the drain,
	// so we unpause the pool and remove the label in that order to avoid any race issues
	err = c.ChangeMachineConfigPoolPause(ctx, mcp, false)
	if err != nil {
		return false, err
	}

	// remove the label now that we unpause the machine config pool
	err = utils.AnnotateObject(ctx, mcp, consts.MachineConfigPoolPausedAnnotation, consts.MachineConfigPoolPausedAnnotationIdle, c.kubeClient)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (c *OpenshiftOrchestrator) GetNodeMachinePoolName(ctx context.Context, node *corev1.Node) (string, error) {
	// hyperShift cluster doesn't have a machine config
	if c.Flavor() == consts.ClusterFlavorHypershift {
		return "", fmt.Errorf("hypershift doesn't have machineConfig")
	}

	desiredConfig, ok := node.Annotations[mcoconsts.DesiredMachineConfigAnnotationKey]
	if !ok {
		return "", fmt.Errorf("failed to find the the annotation [%s] on node [%s]", mcoconsts.DesiredMachineConfigAnnotationKey, node.Name)
	}

	mc := &mcv1.MachineConfig{}
	err := c.kubeClient.Get(ctx, client.ObjectKey{Name: desiredConfig}, mc)
	if err != nil {
		return "", fmt.Errorf("failed to get the desired MachineConfig [%s] for node [%s]: %w", desiredConfig, node.Name, err)
	}
	for _, owner := range mc.OwnerReferences {
		if owner.Kind == "MachineConfigPool" {
			return owner.Name, nil
		}
	}

	return "", fmt.Errorf("failed to find the MCP of the node")
}

func (c *OpenshiftOrchestrator) ChangeMachineConfigPoolPause(ctx context.Context, mcp *mcv1.MachineConfigPool, pause bool) error {
	logger := ctx.Value("logger").(logr.Logger).WithName("ChangeMachineConfigPoolPause")
	logger.Info("change machine config pool state", "pause", pause, "mcp", mcp.Name)

	patchString := []byte(fmt.Sprintf(`{"spec":{"paused":%t}}`, pause))
	patch := client.RawPatch(types.MergePatchType, patchString)
	err := c.kubeClient.Patch(ctx, mcp, patch)
	if err != nil {
		return err
	}

	return nil
}

// IsExternalControlPlaneCluster detects control plane location of the cluster.
// On OpenShift, the control plane topology is configured in configv1.Infrastucture struct.
// On kubernetes, it is determined by which node the sriov operator is scheduled on. If operator
// pod is scheduled on worker node, it is considered as an external control plane.
func isExternalControlPlaneCluster(c client.Client) (bool, error) {
	ctx, cancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFunc()

	infra := &configv1.Infrastructure{}
	err := c.Get(ctx, types.NamespacedName{Name: infraResourceName}, infra)
	if err != nil {
		return false, fmt.Errorf("openshiftControlPlaneTopologyStatus(): Failed to get Infrastructure (name: %s): %w", infraResourceName, err)
	}

	if infra.Status.ControlPlaneTopology == "External" {
		return true, nil
	}
	return false, nil
}
