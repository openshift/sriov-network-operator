package daemon

import (
	"context"
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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// NodeReconciler struct holds various components necessary for reconciling an SR-IOV node.
// It includes a Kubernetes client, SR-IOV client, and other utility interfaces.
// The struct is designed to manage the lifecycle of an SR-IOV devices on a given node.
type NodeReconciler struct {
	client client.Client

	HostHelpers helper.HostHelpersInterface

	platformHelpers platforms.Interface

	eventRecorder *EventRecorder

	featureGate featuregate.FeatureGate

	// list of disabled plugins
	disabledPlugins []string

	loadedPlugins         map[string]plugin.VendorPlugin
	lastAppliedGeneration int64
}

// New creates a new instance of NodeReconciler.
func New(
	client client.Client,
	hostHelpers helper.HostHelpersInterface,
	platformHelper platforms.Interface,
	er *EventRecorder,
	featureGates featuregate.FeatureGate,
	disabledPlugins []string,
) *NodeReconciler {
	return &NodeReconciler{
		client:          client,
		HostHelpers:     hostHelpers,
		platformHelpers: platformHelper,

		lastAppliedGeneration: 0,
		eventRecorder:         er,
		featureGate:           featureGates,
		disabledPlugins:       disabledPlugins,
	}
}

// Init initializes the Sriov Network Operator daemon.
// It enables kernel modules, prepare udev rules and load the host network state
func (dn *NodeReconciler) Init() error {
	funcLog := log.Log.WithName("Init")
	var err error

	if !vars.UsingSystemdMode {
		funcLog.V(0).Info("daemon running in daemon mode")
		_, err = dn.HostHelpers.CheckRDMAEnabled()
		if err != nil {
			funcLog.Error(err, "warning, failed to check RDMA state")
		}
		dn.HostHelpers.TryEnableTun()
		dn.HostHelpers.TryEnableVhostNet()
		err = dn.HostHelpers.CleanSriovFilesFromHost(vars.ClusterType == consts.ClusterTypeOpenshift)
		if err != nil {
			funcLog.Error(err, "failed to remove all the systemd sriov files")
		}
	} else {
		funcLog.V(0).Info("Run(): daemon running in systemd mode")
	}

	if err := dn.prepareNMUdevRule(); err != nil {
		funcLog.Error(err, "failed to prepare udev files to disable network manager on requested VFs")
	}
	if err := dn.HostHelpers.PrepareVFRepUdevRule(); err != nil {
		funcLog.Error(err, "failed to prepare udev files to rename VF representors for requested VFs")
	}

	// init openstack info
	if vars.PlatformType == consts.VirtualOpenStack {
		ns, err := dn.HostHelpers.GetCheckPointNodeState()
		if err != nil {
			return err
		}

		if ns == nil {
			err = dn.platformHelpers.CreateOpenstackDevicesInfo()
			if err != nil {
				return err
			}
		} else {
			dn.platformHelpers.CreateOpenstackDevicesInfoFromNodeStatus(ns)
		}
	}

	// get interfaces
	ns := &sriovnetworkv1.SriovNetworkNodeState{}
	err = dn.updateStatusFromHost(ns)
	if err != nil {
		funcLog.Error(err, "failed to get host network status on init")
		return err
	}

	// init vendor plugins
	dn.loadedPlugins, err = loadPlugins(ns, dn.HostHelpers, dn.disabledPlugins)
	if err != nil {
		funcLog.Error(err, "failed to load vendor plugins")
		return err
	}

	// save init state
	err = dn.HostHelpers.WriteCheckpointFile(ns)
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
		// drain is still in progress we don't need to re-queue the request as the operator will update the annotation
		if drainInProcess {
			return ctrl.Result{}, nil
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
	reqReboot := false
	reqDrain := false

	// check if any of the plugins required to drain or reboot the node
	for k, p := range dn.loadedPlugins {
		d, r, err := p.OnNodeStateChange(desiredNodeState)
		if err != nil {
			funcLog.Error(err, "OnNodeStateChange plugin error", "plugin-name", k)
			return false, false, err
		}
		funcLog.V(0).Info("OnNodeStateChange result",
			"plugin", k,
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
	serviceEnabled, err := dn.HostHelpers.IsServiceEnabled(consts.SriovServicePath)
	if err != nil {
		funcLog.Error(err, "failed to check if sriov-config service exist on host")
		return nil, false, err
	}
	postNetworkServiceEnabled, err := dn.HostHelpers.IsServiceEnabled(consts.SriovPostNetworkServicePath)
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
		sriovResult, exist, err = dn.HostHelpers.ReadSriovResult()
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
	// apply the vendor plugins after we are done with drain if needed
	for k, p := range dn.loadedPlugins {
		// Skip both the general and virtual plugin apply them last
		if k != GenericPluginName && k != VirtualPluginName {
			err := p.Apply()
			if err != nil {
				reqLogger.Error(err, "plugin Apply failed", "plugin-name", k)
				return ctrl.Result{}, err
			}
		}
	}

	// if we don't need to reboot, or we are not doing the configuration in systemd
	// we apply the generic plugin
	if !reqReboot && !vars.UsingSystemdMode {
		// For BareMetal machines apply the generic plugin
		selectedPlugin, ok := dn.loadedPlugins[GenericPluginName]
		if ok {
			// Apply generic plugin last
			err := selectedPlugin.Apply()
			if err != nil {
				reqLogger.Error(err, "generic plugin fail to apply")
				return ctrl.Result{}, err
			}
		}

		// For Virtual machines apply the virtual plugin
		selectedPlugin, ok = dn.loadedPlugins[VirtualPluginName]
		if ok {
			// Apply virtual plugin last
			err := selectedPlugin.Apply()
			if err != nil {
				reqLogger.Error(err, "virtual plugin failed to apply")
				return ctrl.Result{}, err
			}
		}
	}

	if reqReboot {
		reqLogger.Info("reboot node")
		dn.eventRecorder.SendEvent(ctx, "RebootNode", "Reboot node has been initiated")
		return ctrl.Result{}, dn.rebootNode()
	}

	if err := dn.restartDevicePluginPod(ctx); err != nil {
		reqLogger.Error(err, "failed to restart device plugin on the node")
		return ctrl.Result{}, err
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

// checkHostStateDrift returns true if the node state drifted from the nodeState policy
// Check if there is a change in the host network interfaces that require a reconfiguration by the daemon
func (dn *NodeReconciler) checkHostStateDrift(ctx context.Context, desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	funcLog := log.Log.WithName("checkHostStateDrift()")

	// Skip when SriovNetworkNodeState object has just been created.
	if desiredNodeState.GetGeneration() == 1 && len(desiredNodeState.Spec.Interfaces) == 0 {
		err := dn.HostHelpers.ClearPCIAddressFolder()
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
	for _, p := range dn.loadedPlugins {
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
	systemdConfModified, err := dn.HostHelpers.WriteConfFile(desiredNodeState)
	if err != nil {
		funcLog.Error(err, "failed to write configuration file for systemd mode")
		return false, err
	}
	if systemdConfModified {
		// remove existing result file to make sure that we will not use outdated result, e.g. in case if
		// systemd service was not triggered for some reason
		err = dn.HostHelpers.RemoveSriovResult()
		if err != nil {
			funcLog.Error(err, "failed to remove result file for systemd mode")
			return false, err
		}
	}

	err = dn.HostHelpers.WriteSriovSupportedNics()
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

// restartDevicePluginPod restarts the device plugin pod on the specified node.
//
// The function checks if the pod exists, deletes it if found, and waits for it to be deleted successfully.
func (dn *NodeReconciler) restartDevicePluginPod(ctx context.Context) error {
	log.Log.V(2).Info("restartDevicePluginPod(): try to restart device plugin pod")
	pods := &corev1.PodList{}
	err := dn.client.List(ctx, pods, &client.ListOptions{
		Namespace: vars.Namespace, Raw: &metav1.ListOptions{
			LabelSelector:   "app=sriov-device-plugin",
			FieldSelector:   "spec.nodeName=" + vars.NodeName,
			ResourceVersion: "0",
		}})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Log.Info("restartDevicePluginPod(): device plugin pod exited")
			return nil
		}
		log.Log.Error(err, "restartDevicePluginPod(): Failed to list device plugin pod, retrying")
		return err
	}

	if len(pods.Items) == 0 {
		log.Log.Info("restartDevicePluginPod(): device plugin pod exited")
		return nil
	}

	for _, pod := range pods.Items {
		log.Log.V(2).Info("restartDevicePluginPod(): Found device plugin pod, deleting it", "pod-name", pod.Name)
		err = dn.client.Delete(ctx, &pod)
		if errors.IsNotFound(err) {
			log.Log.Info("restartDevicePluginPod(): pod to delete not found")
			continue
		}
		if err != nil {
			log.Log.Error(err, "restartDevicePluginPod(): Failed to delete device plugin pod, retrying")
			return err
		}

		tmpPod := &corev1.Pod{}
		if err := wait.PollUntilContextCancel(ctx, 3*time.Second, true, func(ctx context.Context) (bool, error) {
			err := dn.client.Get(ctx, client.ObjectKeyFromObject(&pod), tmpPod)
			if errors.IsNotFound(err) {
				log.Log.Info("restartDevicePluginPod(): device plugin pod exited")
				return true, nil
			}

			if err != nil {
				log.Log.Error(err, "restartDevicePluginPod(): Failed to check for device plugin exit, retrying")
			} else {
				log.Log.Info("restartDevicePluginPod(): waiting for device plugin pod to exit", "pod-name", pod.Name)
			}
			return false, nil
		}); err != nil {
			log.Log.Error(err, "restartDevicePluginPod(): failed to wait for checking pod deletion")
			return err
		}
	}

	return nil
}

// rebootNode Reboots the node by executing a systemd-run command
func (dn *NodeReconciler) rebootNode() error {
	funcLog := log.Log.WithName("rebootNode")
	funcLog.Info("trigger node reboot")
	exit, err := dn.HostHelpers.Chroot(consts.Host)
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
	stdOut, StdErr, err := dn.HostHelpers.RunCommand("systemd-run", "--unit", "sriov-network-config-daemon-reboot",
		"--description", "sriov-network-config-daemon reboot node", "/bin/sh", "-c", "systemctl stop kubelet.service; reboot")

	if err != nil {
		funcLog.Error(err, "failed to reboot node", "stdOut", stdOut, "StdErr", StdErr)
		return err
	}
	return nil
}

// prepareNMUdevRule prepares/validate the status of the config-daemon custom udev rules needed to control
// the virtual functions by the operator only.
func (dn *NodeReconciler) prepareNMUdevRule() error {
	// we need to remove the Red Hat Virtio network device from the udev rule configuration
	// if we don't remove it when running the config-daemon on a virtual node it will disconnect the node after a reboot
	// even that the operator should not be installed on virtual environments that are not openstack
	// we should not destroy the cluster if the operator is installed there
	supportedVfIds := []string{}
	for _, vfID := range sriovnetworkv1.GetSupportedVfIds() {
		if vfID == "0x1000" || vfID == "0x1041" {
			continue
		}
		supportedVfIds = append(supportedVfIds, vfID)
	}

	return dn.HostHelpers.PrepareNMUdevRule(supportedVfIds)
}

// isDrainCompleted returns true if the current-state annotation is drain completed
func (dn *NodeReconciler) isDrainCompleted(reqDrain bool, desiredNodeState *sriovnetworkv1.SriovNetworkNodeState) bool {
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
	err := utils.AnnotateNode(ctx, desiredNodeState.Name, consts.NodeDrainAnnotation, annotationState, dn.client)
	if err != nil {
		log.Log.Error(err, "Failed to annotate node")
		return err
	}

	funcLog.Info(fmt.Sprintf("apply '%s' annotation for nodeState", annotationState))
	if err := utils.AnnotateObject(context.Background(), desiredNodeState,
		consts.NodeStateDrainAnnotation,
		annotationState, dn.client); err != nil {
		return err
	}

	// the node was annotated we need to wait for the operator to finish the drain
	return nil
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
