package daemon

import (
	"context"
	stdErrors "errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	hosttypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// NodeReconciler struct holds various components necessary for reconciling an SR-IOV node.
// It includes a Kubernetes client, SR-IOV client, and other utility interfaces.
// The struct is designed to manage the lifecycle of an SR-IOV devices on a given node.
type NodeReconciler struct {
	client client.Client

	hostHelpers       helper.HostHelpersInterface
	platformInterface platform.Interface

	eventRecorder *EventRecorder

	featureGate featuregate.FeatureGate

	additionalPlugins []plugin.VendorPlugin
	mainPlugin        plugin.VendorPlugin

	lastAppliedGeneration int64
}

// New creates a new instance of NodeReconciler.
func New(
	client client.Client,
	hostHelpers helper.HostHelpersInterface,
	platformInterface platform.Interface,
	er *EventRecorder,
	featureGates featuregate.FeatureGate,
) *NodeReconciler {
	return &NodeReconciler{
		client:            client,
		hostHelpers:       hostHelpers,
		platformInterface: platformInterface,

		lastAppliedGeneration: 0,
		eventRecorder:         er,
		featureGate:           featureGates,
	}
}

// Init initializes the Sriov Network Operator daemon.
// It enables kernel modules, prepare udev rules and load the host network state
func (dn *NodeReconciler) Init(disabledPlugins []string) error {
	funcLog := log.Log.WithName("Init")

	if !vars.UsingSystemdMode {
		funcLog.V(0).Info("daemon running in daemon mode")
		_, err := dn.hostHelpers.CheckRDMAEnabled()
		if err != nil {
			funcLog.Error(err, "warning, failed to check RDMA state")
		}
		dn.hostHelpers.TryEnableTun()
		dn.hostHelpers.TryEnableVhostNet()
		err = dn.hostHelpers.CleanSriovFilesFromHost(vars.ClusterType == consts.ClusterTypeOpenshift)
		if err != nil {
			funcLog.Error(err, "failed to remove all the systemd sriov files")
		}
	} else {
		funcLog.V(0).Info("Run(): daemon running in systemd mode")
	}

	if err := dn.hostHelpers.PrepareNMUdevRule(); err != nil {
		funcLog.Error(err, "failed to prepare udev files to disable network manager on requested VFs")
	}
	if err := dn.hostHelpers.PrepareVFRepUdevRule(); err != nil {
		funcLog.Error(err, "failed to prepare udev files to rename VF representors for requested VFs")
	}

	// init hypervisor info
	err := dn.platformInterface.Init()
	if err != nil {
		return err
	}

	// get interfaces
	ns := &sriovnetworkv1.SriovNetworkNodeState{}
	err = dn.updateStatusFromHost(ns)
	if err != nil {
		funcLog.Error(err, "failed to get host network status on init")
		return err
	}

	// init vendor plugins
	err = dn.loadPlugins(ns, disabledPlugins)
	if err != nil {
		funcLog.Error(err, "failed to load vendor plugins")
		return err
	}

	// save init state
	err = dn.hostHelpers.WriteCheckpointFile(ns)
	if err != nil {
		funcLog.Error(err, "failed to write checkpoint file on host")
	}
	return err
}

// Reconcile Reconciles the nodeState object by performing the following steps:
// 1. Retrieves the latest NodeState from the API server.
// 2. Checks if the object has the required drain controller annotations for the current generation.
// 3. Updates the nodeState Status object with the existing network state (interfaces, bridges, and RDMA status).
// 4. If running in systemd mode, checks the sriov result from the config-daemon that runs in systemd.
// 5. Compares the latest generation with the last applied generation to determine if a refresh on NICs is needed.
// 6. Checks for drift between the host state and the nodeState status.
// 7. Updates the sync state of the nodeState object as per the current requirements.
// 8. Determines if a drain is required based on the current state of the nodeState.
// 9. Handles the drain if necessary, ensuring that it does not conflict with other drain requests.
// 10. Applies the changes to the nodeState if there are no issues and updates the sync status accordingly.
// 11. If a reboot is required after applying the changes, returns a result to trigger a reboot.
//
// Returns a Result indicating whether or not the controller should requeue the request for further processing.
func (dn *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx).WithName("Reconcile")
	// Get the latest NodeState
	desiredNodeState := &sriovnetworkv1.SriovNetworkNodeState{}
	err := dn.client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, desiredNodeState)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("NodeState doesn't exist")
			return ctrl.Result{}, nil
		}
		reqLogger.Error(err, "Failed to fetch node state", "name", vars.NodeName)
		return ctrl.Result{}, err
	}

	// add/remove external drainer annotation in case external drainer
	changed, err := dn.addRemoveExternalDrainerAnnotation(ctx, desiredNodeState)
	if err != nil {
		reqLogger.Error(err, "failed to add/remove external drainer annotation")
		return ctrl.Result{}, err
	}
	if changed {
		reqLogger.Info("external drainer annotation changed, requeue to tigger reconcile")
		return ctrl.Result{}, nil
	}

	// Check the object as the drain controller annotations
	// if not just wait for the drain controller to add them before we start taking care of the nodeState
	if !utils.ObjectHasAnnotationKey(desiredNodeState, consts.NodeStateDrainAnnotationCurrent) ||
		!utils.ObjectHasAnnotationKey(desiredNodeState, consts.NodeStateDrainAnnotation) {
		reqLogger.V(2).Info("NodeState doesn't have the current drain annotation")
		return ctrl.Result{}, nil
	}

	latest := desiredNodeState.GetGeneration()
	current := desiredNodeState.DeepCopy()
	reqLogger.V(0).Info("new generation", "generation", latest)

	// Update the nodeState Status object with the existing network state (interfaces bridges and rdma status)
	err = dn.updateStatusFromHost(desiredNodeState)
	if err != nil {
		reqLogger.Error(err, "failed to get host network status")
		return ctrl.Result{}, err
	}

	// if we are running in systemd mode we want to get the sriov result from the config-daemon that runs in systemd
	sriovResult, sriovResultExists, err := dn.checkSystemdStatus()
	//TODO: in the case we need to think what to do if we try to apply again or not
	if err != nil {
		reqLogger.Error(err, "failed to check systemd status unexpected error")
		err = dn.updateSyncState(ctx, desiredNodeState, consts.SyncStatusFailed, "unexpected error")
		if err != nil {
			reqLogger.Error(err, "failed to update nodeState status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// if we are on the latest generation make a refresh on the nics
	if dn.lastAppliedGeneration == latest {
		isDrifted, err := dn.checkHostStateDrift(ctx, desiredNodeState)
		if err != nil {
			reqLogger.Error(err, "failed to refresh host state")
			return ctrl.Result{}, err
		}

		// if there are no host state drift changes, and we are on the latest applied policy
		// we check if we need to publish a new nodeState status if not we requeue
		if !isDrifted {
			shouldUpdate := dn.shouldUpdateStatus(current, desiredNodeState)
			if shouldUpdate {
				reqLogger.Info("updating nodeState with new host status")
				err = dn.updateSyncState(ctx, desiredNodeState, desiredNodeState.Status.SyncStatus, desiredNodeState.Status.LastSyncError)
				if err != nil {
					reqLogger.Error(err, "failed to update nodeState new host status")
					return ctrl.Result{}, err
				}
			}
			// periodically ensure device plugin is unblocked,
			// this is required to ensure that device plugin can start in case if it is restarted for some reason
			if vars.FeatureGate.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate) &&
				len(desiredNodeState.Spec.Interfaces) > 0 {
				devicePluginPods, err := dn.getDevicePluginPodsForNode(ctx)
				if err != nil {
					reqLogger.Error(err, "failed to get device plugin pods")
					return ctrl.Result{}, err
				}
				if err := dn.tryUnblockDevicePlugin(ctx, desiredNodeState, devicePluginPods); err != nil {
					reqLogger.Error(err, "failed to unblock device plugin")
					return ctrl.Result{}, err
				}
			}

			return ctrl.Result{RequeueAfter: consts.DaemonRequeueTime}, nil
		}
	}

	// set sync state to inProgress, but we don't clear the failed status
	err = dn.updateSyncState(ctx, desiredNodeState, consts.SyncStatusInProgress, desiredNodeState.Status.LastSyncError)
	if err != nil {
		reqLogger.Error(err, "failed to update sync status to inProgress")
		return ctrl.Result{}, err
	}

	reqReboot, reqDrain, err := dn.checkOnNodeStateChange(desiredNodeState)
	if err != nil {
		return ctrl.Result{}, err
	}

	if vars.UsingSystemdMode {
		// When running using systemd check if the applied configuration is the latest one
		// or there is a new config we need to apply
		// When using systemd configuration we write the file
		systemdConfModified, err := dn.writeSystemdConfigFile(desiredNodeState)
		if err != nil {
			reqLogger.Error(err, "failed to write systemd config file")
			return ctrl.Result{}, err
		}
		reqDrain = reqDrain || systemdConfModified || !sriovResultExists
		// require reboot if drain needed for systemd mode
		reqReboot = reqReboot || reqDrain
	}

	reqLogger.V(0).Info("aggregated daemon node state requirement",
		"drain-required", reqDrain, "reboot-required", reqReboot, "disable-drain", vars.DisableDrain)

	// handle drain only if the plugins request drain, or we are already in a draining request state
	if reqDrain ||
		!utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainIdle) {
		drainInProcess, err := dn.handleDrain(ctx, desiredNodeState, reqReboot)
		if err != nil {
			reqLogger.Error(err, "failed to handle drain")
			return ctrl.Result{}, err
		}

		// TODO: remove this after we stop using the node annotation
		// drain is still in progress we will still requeue the request in case there is an un-expect state in the draining
		// this will allow the daemon to try again.
		if drainInProcess {
			reqLogger.Info("node drain still in progress, requeue")
			return ctrl.Result{RequeueAfter: consts.DaemonRequeueTime}, nil
		}
	}

	// if we finish the drain we should run apply here
	if dn.isDrainCompleted(reqDrain, desiredNodeState) {
		return dn.apply(ctx, desiredNodeState, reqReboot, sriovResult)
	}

	return ctrl.Result{}, nil
}

// checkOnNodeStateChange checks the state change required for the node based on the desired SriovNetworkNodeState.
// The function iterates over all loaded plugins and calls their OnNodeStateChange method with the desired state.
// It returns two boolean values indicating whether a reboot or drain operation is required.
func (dn *NodeReconciler) checkOnNodeStateChange(desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error) {
	funcLog := log.Log.WithName("checkOnNodeStateChange")
	// Check the main plugin for changes
	reqDrain, reqReboot, err := dn.mainPlugin.OnNodeStateChange(desiredNodeState)
	if err != nil {
		funcLog.Error(err, "OnNodeStateChange plugin error", "mainPluginName", dn.mainPlugin.Name())
		return false, false, err
	}
	funcLog.V(0).Info("OnNodeStateChange result",
		"main plugin name", dn.mainPlugin.Name(),
		"drain-required", reqDrain,
		"reboot-required", reqReboot)

	// check if any of the plugins required to drain or reboot the node
	for _, p := range dn.additionalPlugins {
		d, r, err := p.OnNodeStateChange(desiredNodeState)
		if err != nil {
			funcLog.Error(err, "OnNodeStateChange plugin error", "pluginName", p.Name())
			return false, false, err
		}
		funcLog.V(0).Info("OnNodeStateChange result",
			"pluginName", p.Name(),
			"drain-required", d,
			"reboot-required", r)
		reqDrain = reqDrain || d
		reqReboot = reqReboot || r
	}

	return reqReboot, reqDrain, nil
}

// checkSystemdStatus Checks the status of systemd services on the host node.
// return the sriovResult struct a boolean if the result file exist on the node
func (dn *NodeReconciler) checkSystemdStatus() (*hosttypes.SriovResult, bool, error) {
	if !vars.UsingSystemdMode {
		return nil, false, nil
	}

	funcLog := log.Log.WithName("checkSystemdStatus")
	serviceEnabled, err := dn.hostHelpers.IsServiceEnabled(consts.SriovServicePath)
	if err != nil {
		funcLog.Error(err, "failed to check if sriov-config service exist on host")
		return nil, false, err
	}
	postNetworkServiceEnabled, err := dn.hostHelpers.IsServiceEnabled(consts.SriovPostNetworkServicePath)
	if err != nil {
		funcLog.Error(err, "failed to check if sriov-config-post-network service exist on host")
		return nil, false, err
	}

	// if the service doesn't exist we should continue to let the k8s plugin to create the service files
	// this is only for k8s base environments, for openshift the sriov-operator creates a machine config to will apply
	// the system service and reboot the node the config-daemon doesn't need to do anything.
	sriovResult := &hosttypes.SriovResult{SyncStatus: consts.SyncStatusFailed,
		LastSyncError: fmt.Sprintf("some sriov systemd services are not available on node: "+
			"sriov-config available:%t, sriov-config-post-network available:%t", serviceEnabled, postNetworkServiceEnabled)}
	exist := false

	// check if the service exist
	if serviceEnabled && postNetworkServiceEnabled {
		exist = true
		sriovResult, err = dn.hostHelpers.ReadSriovResult()
		if err != nil {
			funcLog.Error(err, "failed to load sriov result file from host")
			return nil, false, err
		}
	}
	return sriovResult, exist, nil
}

// apply applies the desired state of the node by:
// 1. Applying vendor plugins that have been loaded.
// 2. Depending on whether a reboot is required or if the configuration is being done via systemd, it applies the generic or virtual plugin(s).
// 3. Rebooting the node if necessary and sending an event.
// 4. Restarting the device plugin pod on the node.
// 5. Requesting annotation updates for draining the idle state of the node.
// 6. Synchronizing with the host network status and updating the sync status of the node in the nodeState object.
// 7. Updating the lastAppliedGeneration to the current generation.
func (dn *NodeReconciler) apply(ctx context.Context, desiredNodeState *sriovnetworkv1.SriovNetworkNodeState, reqReboot bool, sriovResult *hosttypes.SriovResult) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx).WithName("Apply")

	// Restart the device plugin *before* applying configuration if the
	// BlockDevicePluginUntilConfiguredFeatureGate feature is enabled.
	// With this gate enabled, the device plugin will remain blocked until it is
	// explicitly unblocked after configuration (see waitForDevicePluginPodAndTryUnblock).
	// If the feature gate is not enabled, preserve legacy behavior by
	// restarting the device plugin *after* configuration is applied.
	if vars.FeatureGate.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate) {
		if err := dn.restartDevicePluginPod(ctx); err != nil {
			reqLogger.Error(err, "failed to restart device plugin on the node")
			return ctrl.Result{}, err
		}
	}

	// apply the additional plugins after we are done with drain if needed
	for _, p := range dn.additionalPlugins {
		err := p.Apply()
		if err != nil {
			reqLogger.Error(err, "plugin Apply failed", "plugin-name", p.Name())
			return ctrl.Result{}, err
		}
	}

	// if we don't need to reboot, or we are not doing the configuration in systemd
	// we apply the main plugin
	if !reqReboot && !vars.UsingSystemdMode && dn.mainPlugin != nil {
		err := dn.mainPlugin.Apply()
		if err != nil {
			reqLogger.Error(err, "plugin Apply failed", "plugin-name", dn.mainPlugin.Name())
			return ctrl.Result{}, err
		}
	}

	if reqReboot {
		reqLogger.Info("reboot node")
		dn.eventRecorder.SendEvent(ctx, "RebootNode", "Reboot node has been initiated")
		return ctrl.Result{}, dn.rebootNode()
	}

	if vars.FeatureGate.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate) {
		if len(desiredNodeState.Spec.Interfaces) == 0 {
			reqLogger.Info("no interfaces in desired state, skipping device plugin wait as device plugin won't be deployed")
		} else {
			if err := dn.waitForDevicePluginPodAndTryUnblock(ctx, desiredNodeState); err != nil {
				reqLogger.Error(err, "failed to wait for device plugin pod to start and try to unblock it")
				return ctrl.Result{}, err
			}
		}
	} else {
		// if the feature gate is not enabled we preserver the old behavior
		// and restart device plugin after configuration is applied
		if err := dn.restartDevicePluginPod(ctx); err != nil {
			reqLogger.Error(err, "failed to restart device plugin on the node")
			return ctrl.Result{}, err
		}
	}

	err := dn.annotate(ctx, desiredNodeState, consts.DrainIdle)
	if err != nil {
		reqLogger.Error(err, "failed to request annotation update to idle")
		return ctrl.Result{}, err
	}

	reqLogger.Info("sync succeeded")
	syncStatus := consts.SyncStatusSucceeded
	lastSyncError := ""
	if vars.UsingSystemdMode {
		syncStatus = sriovResult.SyncStatus
		lastSyncError = sriovResult.LastSyncError
	}

	// Update the nodeState Status object with the existing network interfaces
	err = dn.updateStatusFromHost(desiredNodeState)
	if err != nil {
		reqLogger.Error(err, "failed to get host network status")
		return ctrl.Result{}, err
	}

	err = dn.updateSyncState(ctx, desiredNodeState, syncStatus, lastSyncError)
	if err != nil {
		reqLogger.Error(err, "failed to update sync status")
		return ctrl.Result{}, err
	}

	// update the lastAppliedGeneration
	dn.lastAppliedGeneration = desiredNodeState.Generation

	return ctrl.Result{RequeueAfter: consts.DaemonRequeueTime}, nil
}

// tryUnblockDevicePlugin checks if the device plugin can be unblocked
func (dn *NodeReconciler) tryUnblockDevicePlugin(ctx context.Context,
	desiredNodeState *sriovnetworkv1.SriovNetworkNodeState, devicePluginPods []corev1.Pod) error {
	funcLog := log.Log.WithName("tryUnblockDevicePlugin")
	funcLog.Info("check if we need to remove the wait-for-config annotation")
	// we want to unblock the device plugin only if the desired state contains configuration for the interfaces
	if len(desiredNodeState.Spec.Interfaces) == 0 {
		funcLog.Info("desired node state has no interfaces, keep the wait-for-config annotation")
		return nil
	}
	for _, pod := range devicePluginPods {
		if err := utils.RemoveAnnotationFromObject(ctx, &pod,
			consts.DevicePluginWaitConfigAnnotation, dn.client); err != nil {
			return fmt.Errorf("failed to remove %s annotation from pod: %w", consts.DevicePluginWaitConfigAnnotation, err)
		}
	}
	return nil
}

// checkHostStateDrift returns true if the node state drifted from the nodeState policy
// Check if there is a change in the host network interfaces that require a reconfiguration by the daemon
func (dn *NodeReconciler) checkHostStateDrift(ctx context.Context, desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	funcLog := log.Log.WithName("checkHostStateDrift()")
	// Skip when SriovNetworkNodeState object has just been created.
	if desiredNodeState.GetGeneration() == 1 && len(desiredNodeState.Spec.Interfaces) == 0 {
		err := dn.hostHelpers.ClearPCIAddressFolder()
		if err != nil {
			funcLog.Error(err, "failed to clear the PCI address configuration")
			return false, err
		}

		funcLog.V(0).Info("interface policy spec not yet set by controller for sriovNetworkNodeState",
			"name", desiredNodeState.Name)
		if desiredNodeState.Status.SyncStatus != consts.SyncStatusSucceeded ||
			desiredNodeState.Status.LastSyncError != "" {
			err = dn.updateSyncState(ctx, desiredNodeState, consts.SyncStatusSucceeded, "")
		}
		return false, err
	}

	// Verify changes in the status of the SriovNetworkNodeState CR.
	log.Log.V(0).Info("verifying interfaces status change")
	if dn.mainPlugin != nil {
		log.Log.V(2).Info("verifying status change for plugin", "pluginName", dn.mainPlugin.Name())
		changed, err := dn.mainPlugin.CheckStatusChanges(desiredNodeState)
		if err != nil {
			return false, err
		}
		if changed {
			log.Log.V(0).Info("plugin require change", "pluginName", dn.mainPlugin.Name())
			return true, nil
		}
	}

	for _, p := range dn.additionalPlugins {
		// Verify changes in the status of the SriovNetworkNodeState CR.
		log.Log.V(2).Info("verifying status change for plugin", "pluginName", p.Name())
		changed, err := p.CheckStatusChanges(desiredNodeState)
		if err != nil {
			return false, err
		}
		if changed {
			log.Log.V(0).Info("plugin require change", "pluginName", p.Name())
			return true, nil
		}
	}

	log.Log.V(0).Info("Interfaces not changed")
	return false, nil
}

// writeSystemdConfigFile Writes the systemd configuration file for the node
// and handles any necessary actions such as removing an existing result file and writing supported NIC IDs.
//
//	The function first attempts to write the systemd configuration file based on the desired node state.
//	If successful, it checks if the configuration file was modified. If so, it removes the existing result file (if present) to ensure that outdated results are not used.
//	After writing the configuration file and potentially removing the old one, it writes a file containing supported NIC IDs.
func (dn *NodeReconciler) writeSystemdConfigFile(desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	funcLog := log.Log.WithName("writeSystemdConfigFile()")
	funcLog.V(0).Info("writing systemd config file to host")
	systemdConfModified, err := dn.hostHelpers.WriteConfFile(desiredNodeState)
	if err != nil {
		funcLog.Error(err, "failed to write configuration file for systemd mode")
		return false, err
	}
	if systemdConfModified {
		// remove existing result file to make sure that we will not use outdated result, e.g. in case if
		// systemd service was not triggered for some reason
		err = dn.hostHelpers.RemoveSriovResult()
		if err != nil {
			funcLog.Error(err, "failed to remove result file for systemd mode")
			return false, err
		}
	}

	err = dn.hostHelpers.WriteSriovSupportedNics()
	if err != nil {
		funcLog.Error(err, "failed to write supported nic ids file for systemd mode")
		return false, err
	}

	funcLog.V(0).Info("systemd mode WriteConfFile results",
		"drain-required", systemdConfModified, "reboot-required", systemdConfModified)
	return systemdConfModified, nil
}

// handleDrain: adds the right annotation to the node and nodeState object
// returns true if we need to finish the reconcile loop and wait for a new object
func (dn *NodeReconciler) handleDrain(ctx context.Context, desiredNodeState *sriovnetworkv1.SriovNetworkNodeState, reqReboot bool) (bool, error) {
	funcLog := log.Log.WithName("handleDrain")
	// done with the drain we can continue with the configuration
	if utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainComplete) {
		funcLog.Info("the node complete the draining")
		return false, nil
	}

	// the operator is still draining the node so we reconcile
	if utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.Draining) {
		funcLog.Info("the node is still draining")
		return true, nil
	}

	// drain is disabled we continue with the configuration
	if vars.DisableDrain {
		funcLog.Info("drain is disabled in sriovOperatorConfig")
		return false, nil
	}

	// annotate both node and node state with drain or reboot
	annotation := consts.DrainRequired
	if reqReboot {
		annotation = consts.RebootRequired
	}
	return true, dn.annotate(ctx, desiredNodeState, annotation)
}

// getDevicePluginPods returns the device plugin pods running on this node
func (dn *NodeReconciler) getDevicePluginPodsForNode(ctx context.Context) ([]corev1.Pod, error) {
	funcLog := log.Log.WithName("getDevicePluginPodsForNode")
	pods := &corev1.PodList{}
	err := dn.client.List(ctx, pods, &client.ListOptions{
		Namespace: vars.Namespace, Raw: &metav1.ListOptions{
			LabelSelector: "app=sriov-device-plugin",
			FieldSelector: "spec.nodeName=" + vars.NodeName,
		}})
	if err != nil {
		funcLog.Error(err, "failed to list device plugin pods")
		return []corev1.Pod{}, err
	}
	if len(pods.Items) == 0 {
		return []corev1.Pod{}, nil
	}
	return pods.Items, nil
}

// restartDevicePluginPod restarts the device plugin pod on the specified node.
//
// The function checks if the pod exists, deletes it if found, and waits for it to be deleted successfully.
func (dn *NodeReconciler) restartDevicePluginPod(ctx context.Context) error {
	funcLog := log.Log.WithName("restartDevicePluginPod")
	funcLog.V(2).Info("try to restart device plugin pod")
	devicePluginPods, err := dn.getDevicePluginPodsForNode(ctx)
	if err != nil {
		return err
	}
	if len(devicePluginPods) == 0 {
		funcLog.V(2).Info("no device plugin pods found during restart attempt")
		return nil
	}
	for _, pod := range devicePluginPods {
		podUID := pod.UID
		funcLog.V(2).Info("Found device plugin pod, deleting it",
			"pod-name", pod.Name, "pod-uid", podUID)
		err = dn.client.Delete(ctx, &pod)
		if errors.IsNotFound(err) {
			funcLog.Info("pod to delete not found")
			continue
		}
		if err != nil {
			funcLog.Error(err, "Failed to delete device plugin pod")
			return err
		}
		newPod := &corev1.Pod{}
		if err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
			err := dn.client.Get(ctx, client.ObjectKeyFromObject(&pod), newPod)
			if errors.IsNotFound(err) {
				funcLog.Info("device plugin pod exited")
				return true, nil
			}
			if err != nil {
				return false, fmt.Errorf("failed to get device plugin pod: %w", err)
			}
			// Check if the pod was recreated (different UID means it's a new pod)
			if newPod.UID != podUID {
				funcLog.Info("device plugin pod was recreated",
					"old-uid", podUID, "new-uid", newPod.UID)
				return true, nil
			}
			funcLog.Info("waiting for device plugin pod to exit",
				"pod-name", pod.Name, "pod-uid", newPod.UID)
			return false, nil
		}); err != nil {
			funcLog.Error(err, "failed to wait device plugin pod to exit")
			return err
		}
	}
	return nil
}

// waitForDevicePluginPodAndTryUnblock waits for the new device plugin pod to start and set the wait-for-config annotation. This allows us to unblock
// the new device plugin instance within the same reconciliation loop without needing to wait
// for the periodic check. We expect to have at least one device plugin pod for the node.
func (dn *NodeReconciler) waitForDevicePluginPodAndTryUnblock(ctx context.Context, desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	funcLog := log.Log.WithName("waitForDevicePluginPodAndTryUnblock")
	funcLog.Info("waiting for device plugin to set wait-for-config annotation", "annotation", consts.DevicePluginWaitConfigAnnotation)
	var devicePluginPods []corev1.Pod
	err := wait.PollUntilContextTimeout(ctx, time.Second, 2*time.Minute, true,
		func(ctx context.Context) (bool, error) {
			var err error
			devicePluginPods, err = dn.getDevicePluginPodsForNode(ctx)
			if err != nil {
				funcLog.Error(err, "failed to get device plugin pod while waiting for a new pod to start")
				return false, err
			}
			if len(devicePluginPods) == 0 {
				funcLog.V(2).Info("no device plugin pods found while waiting for a new pod to start")
				return false, nil
			}
			for _, pod := range devicePluginPods {
				// Wait for at least one device plugin pod to have the wait-for-config annotation.
				// Usually there's only one device plugin pod per node, but unmanaged (by the operator) pods
				// may also match our selector. Since unmanaged pods won't have this annotation,
				// we only require one pod (the managed one) to have it.
				if utils.ObjectHasAnnotationKey(&pod, consts.DevicePluginWaitConfigAnnotation) {
					funcLog.Info("wait-for-config annotation found on pod",
						"pod", pod.Name)
					return true, nil
				}
			}
			funcLog.V(2).Info("waiting for new device plugin pod to have wait-for-config annotation")
			return false, nil
		})
	if err != nil {
		if !stdErrors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// If annotation is not found within the timeout, log a warning and proceed.
		// The device plugin pod will be unblocked by the periodic check logic in tryUnblockDevicePlugin.
		funcLog.Info("WARNING: device plugin pod with wait-for-config annotation not found within timeout")
		return nil
	}
	if len(devicePluginPods) > 0 {
		// try to unblock all device plugin pods we retrieved with the latest loop iteration
		if err := dn.tryUnblockDevicePlugin(ctx, desiredNodeState, devicePluginPods); err != nil {
			return err
		}
	}
	return nil
}

// rebootNode Reboots the node by executing a systemd-run command
func (dn *NodeReconciler) rebootNode() error {
	funcLog := log.Log.WithName("rebootNode")
	funcLog.Info("trigger node reboot")
	exit, err := dn.hostHelpers.Chroot(consts.Host)
	if err != nil {
		funcLog.Error(err, "chroot command failed")
		return err
	}
	defer exit()
	// creates a new transient systemd unit to reboot the system.
	// We explictily try to stop kubelet.service first, before anything else; this
	// way we ensure the rest of system stays running, because kubelet may need
	// to do "graceful" shutdown by e.g. de-registering with a load balancer.
	// However note we use `;` instead of `&&` so we keep rebooting even
	// if kubelet failed to shutdown - that way the machine will still eventually reboot
	// as systemd will time out the stop invocation.
	stdOut, StdErr, err := dn.hostHelpers.RunCommand("systemd-run", "--unit", "sriov-network-config-daemon-reboot",
		"--description", "sriov-network-config-daemon reboot node", "/bin/sh", "-c", "systemctl stop kubelet.service; reboot")

	if err != nil {
		funcLog.Error(err, "failed to reboot node", "stdOut", stdOut, "StdErr", StdErr)
		return err
	}
	return nil
}

// isDrainCompleted returns true if the current-state annotation is drain completed
func (dn *NodeReconciler) isDrainCompleted(reqDrain bool, desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) bool {
	if vars.DisableDrain {
		return true
	}

	// if we need to drain check the drain status
	if reqDrain {
		return utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainComplete)
	}

	// check in case a reboot was requested and the second run doesn't require a drain
	if !utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotation, consts.DrainIdle) {
		return utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainComplete)
	}

	// if we don't need to drain at all just return true so we can apply the configuration
	return true
}

// annotate annotates the nodeState object with specified annotation.
func (dn *NodeReconciler) annotate(
	ctx context.Context,
	desiredNodeState *sriovnetworkv1.SriovNetworkNodeState,
	annotationState string) error {
	funcLog := log.Log.WithName("annotate")

	funcLog.Info(fmt.Sprintf("apply '%s' annotation for node", annotationState))
	if err := utils.AnnotateNode(ctx,
		desiredNodeState.Name,
		consts.NodeDrainAnnotation,
		annotationState, dn.client); err != nil {
		funcLog.Error(err, "Failed to annotate node")
		return err
	}

	funcLog.Info(fmt.Sprintf("apply '%s' annotation for nodeState", annotationState))
	if err := utils.AnnotateObject(ctx, desiredNodeState,
		consts.NodeStateDrainAnnotation,
		annotationState, dn.client); err != nil {
		funcLog.Error(err, "Failed to annotate nodeState")
		return err
	}

	// the node was annotated we need to wait for the operator to finish the drain
	return nil
}

// manages addition/removal of external drainer annotation upon node state objects
func (dn *NodeReconciler) addRemoveExternalDrainerAnnotation(ctx context.Context,
	desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	funcLog := log.FromContext(ctx).WithName("addRemoveExternalDrainerAnnotation")

	var changedAnnotation bool
	// external drainer annotation will be added/removed only when both desired/current node state are in 'Idle' state
	// or while neither current nor desired node state annotations exist, indicating that drain-controller has
	// yet to add them on nodeState object
	neitherExists := !utils.ObjectHasAnnotationKey(desiredNodeState, consts.NodeStateDrainAnnotationCurrent) &&
		!utils.ObjectHasAnnotationKey(desiredNodeState, consts.NodeStateDrainAnnotation)
	bothIdle := utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainIdle) &&
		utils.ObjectHasAnnotation(desiredNodeState, consts.NodeStateDrainAnnotation, consts.DrainIdle)

	if !bothIdle && !neitherExists {
		return false, nil
	}

	annotations := desiredNodeState.GetAnnotations()
	if !vars.UseExternalDrainer {
		// remove external drainer nodestate annotation if exists
		if _, ok := annotations[consts.NodeStateExternalDrainerAnnotation]; ok {
			funcLog.Info("remove external drainer nodestate annotation", "annotation", consts.NodeStateExternalDrainerAnnotation)
			original := desiredNodeState.DeepCopy()
			delete(annotations, consts.NodeStateExternalDrainerAnnotation)
			// Patch only the annotations
			if err := dn.client.Patch(ctx, desiredNodeState, client.MergeFrom(original)); err != nil {
				funcLog.Error(err, "failed to patch nodestate after removing external drainer annotation")
				return false, err
			}
			changedAnnotation = true
		}
		return changedAnnotation, nil
	}

	if _, ok := annotations[consts.NodeStateExternalDrainerAnnotation]; !ok {
		// add external drainer nodestate annotation
		funcLog.Info("add external drainer nodestate annotation", "annotation", consts.NodeStateExternalDrainerAnnotation)
		err := utils.AnnotateObject(ctx, desiredNodeState,
			consts.NodeStateExternalDrainerAnnotation, "true", dn.client)
		if err != nil {
			funcLog.Error(err, "failed to add node external drainer annotation")
			return false, err
		}
		changedAnnotation = true
	}

	return changedAnnotation, nil
}

// SetupWithManager sets up the controller with the Manager.
func (dn *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sriovnetworkv1.SriovNetworkNodeState{}).
		WithEventFilter(predicate.Or(predicate.AnnotationChangedPredicate{}, predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(dn)
}

// -------------------------------------
// ---- unit tests helper function -----
// -------------------------------------

func (dn *NodeReconciler) GetLastAppliedGeneration() int64 {
	return dn.lastAppliedGeneration
}
