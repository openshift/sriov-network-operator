package drain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	draconsts "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/consts"

	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	DrainTimeOut = 90 * time.Second
)

//go:generate ../../bin/mockgen -destination mock/mock_drain.go -source drainer.go

// writer implements io.Writer interface as a pass-through for log.Log.
type writer struct {
	logFunc func(msg string, keysAndValues ...interface{})
}

// Write passes string(p) into writer's logFunc and always returns len(p)
func (w writer) Write(p []byte) (n int, err error) {
	w.logFunc(string(p))
	return len(p), nil
}

// DrainErrorCallback is a callback function that is called when a drain error occurs.
// This allows the caller to be notified of errors immediately as they happen.
// Must not be nil — use DrainErrorNoop when no action is needed.
type DrainErrorCallback func(err error)

// DrainErrorNoop is a no-op DrainErrorCallback for callers that do not need
// to react to drain errors (e.g. the uncordon path in CompleteDrainNode).
func DrainErrorNoop(_ error) {}

type DrainInterface interface {
	DrainNode(context.Context, *corev1.Node, bool, bool, DrainErrorCallback) (bool, error)
	CompleteDrainNode(context.Context, *corev1.Node) (bool, error)
}

type Drainer struct {
	kubeClient   kubernetes.Interface
	orchestrator orchestrator.Interface
}

func NewDrainer(orchestrator orchestrator.Interface) (DrainInterface, error) {
	kclient, err := kubernetes.NewForConfig(vars.Config)
	if err != nil {
		return nil, err
	}

	return &Drainer{
		kubeClient:   kclient,
		orchestrator: orchestrator,
	}, err
}

// DrainNode the function cordon a node and drain pods from it
// if fullNodeDrain true all the pods on the system will get drained
// for openshift system we also pause the machine config pool this machine is part of it
// onError callback is called immediately when drain errors occur (e.g., pod eviction failures)
func (d *Drainer) DrainNode(ctx context.Context, node *corev1.Node, fullNodeDrain, singleNode bool, onError DrainErrorCallback) (bool, error) {
	reqLogger := ctx.Value(constants.LoggerContextKey).(logr.Logger).WithName("drainNode")
	reqLogger.Info("Node drain requested")

	completed, err := d.orchestrator.BeforeDrainNode(ctx, node)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to run BeforeDrainNode for orchestrator %s on node %s: %w",
			d.orchestrator.ClusterType(), node.Name, err)
		reqLogger.Error(wrappedErr, "BeforeDrainNode failed")
		onError(wrappedErr)
		return false, wrappedErr
	}

	if !completed {
		reqLogger.Info("OpenshiftDrainNode did not finish re queue the node request")
		return false, nil
	}

	// Check if we are on a single node, and we require a reboot/full-drain we just return
	if fullNodeDrain && singleNode {
		return true, nil
	}

	drainHelper := createDrainHelper(d.kubeClient, ctx, fullNodeDrain, onError)

	reqLogger.Info("drainNode(): Start draining")

	// Cordon the node first
	if err = drain.RunCordonOrUncordon(drainHelper, node, true); err != nil {
		wrappedErr := fmt.Errorf("failed to cordon node %s: %w", node.Name, err)
		reqLogger.Error(wrappedErr, "drainNode(): Cordon failed")
		onError(wrappedErr)
		return false, wrappedErr
	}

	// Run the drain - RunNodeDrain has its own 90 second timeout for retrying evictions
	if err = drain.RunNodeDrain(drainHelper, node.Name); err != nil {
		wrappedErr := fmt.Errorf("failed to drain node %s: %w", node.Name, err)
		reqLogger.Error(wrappedErr, "drainNode(): Drain failed")
		return false, wrappedErr
	}

	reqLogger.Info("drainNode(): Drain completed")
	return true, nil
}

// CompleteDrainNode run un-cordon for the requested node
// for openshift system we also remove the pause from the machine config pool this node is part of
// only if we are the last draining node on that pool
func (d *Drainer) CompleteDrainNode(ctx context.Context, node *corev1.Node) (bool, error) {
	logger := ctx.Value(constants.LoggerContextKey).(logr.Logger).WithName("CompleteDrainNode")

	// Create drain helper object
	// full drain is not important here, onError callback not needed for uncordon
	drainHelper := createDrainHelper(d.kubeClient, ctx, false, DrainErrorNoop)

	// run the un cordon function on the node
	if err := drain.RunCordonOrUncordon(drainHelper, node, false); err != nil {
		logger.Error(err, "failed to un-cordon the node")
		return false, err
	}

	// call the openshift complete drain to unpause the MCP
	// only if we are the last draining node in the pool
	completed, err := d.orchestrator.AfterCompleteDrainNode(ctx, node)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to run AfterCompleteDrainNode for orchestrator %s", d.orchestrator.ClusterType()))
		return false, err
	}

	logger.V(2).Info("CompleteDrainNode:()", "drainCompleted", completed)
	return completed, nil
}

// createDrainHelper function to create a drain helper
// if fullDrain is false we only remove pods with SR-IOV DRA allocations (by driver) or the resourcePrefix
// if not we remove all the pods in the node
// onError callback is called immediately when pod eviction/deletion errors occur
func createDrainHelper(kubeClient kubernetes.Interface, ctx context.Context, fullDrain bool, onError DrainErrorCallback) *drain.Helper {
	logger := ctx.Value(constants.LoggerContextKey).(logr.Logger).WithName("createDrainHelper")

	drainer := &drain.Helper{
		Client:              kubeClient,
		Force:               true,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		GracePeriodSeconds:  -1,
		Timeout:             DrainTimeOut,
		OnPodDeletionOrEvictionFinished: func(pod *corev1.Pod, usingEviction bool, err error) {
			if err != nil {
				verbStr := constants.DrainDelete
				if usingEviction {
					verbStr = constants.DrainEvict
				}
				logger.Error(err, fmt.Sprintf("failed to %s pod %s/%s from node", verbStr, pod.Namespace, pod.Name))
				return
			}
			verbStr := constants.DrainDeleted
			if usingEviction {
				verbStr = constants.DrainEvicted
			}
			logger.Info(fmt.Sprintf("%s pod %s/%s from node", verbStr, pod.Namespace, pod.Name))
		},
		Ctx: ctx,
		Out: writer{func(msg string, kv ...interface{}) { logger.Info(strings.ReplaceAll(msg, "\n", "")) }},
		// ErrOut receives progress and warning lines from k8s.io/kubectl/pkg/drain.
		// Terminal failures are returned by RunNodeDrain; see IsInformationalDrainErrOutMessage.
		ErrOut: writer{func(msg string, kv ...interface{}) {
			cleanMsg := strings.ReplaceAll(msg, "\n", "")
			if IsInformationalDrainErrOutMessage(cleanMsg) {
				logger.Info(cleanMsg, kv...)
			} else {
				logger.Error(nil, cleanMsg, kv...)
			}
			// Retry progress is informational but still recorded so the condition
			// names the pods blocking the drain. Empty and WARNING: lines are not.
			if isIgnoredDrainErrOutMessage(cleanMsg) {
				return
			}
			onError(fmt.Errorf("%s", cleanMsg))
		}},
	}

	// Without a reboot, drain device consumers while preserving ordinary workloads.
	if !fullDrain {
		deleteFunction := func(p corev1.Pod) drain.PodDeleteStatus {
			// Keep this check independent of the DRA feature gate: disabling it
			// switches the deployed provider (DP vs DRA) but it does not ensure existing SR-IOV
			// DRA consumers have terminated. E.g.:
			// An admin deletes the SR-IOV policy.
			// 		The operator starts reconciling the change, which requires draining Pods with SR-IOV VFs.
			// The admin disables DRA before that drain finishes.
			// 		The mode-change validation permits this once the policies are removed;
			// 		it does not verify that their SR-IOV VF consumers have terminated.
			usesSRIOV, err := podUsesSRIOVDRA(ctx, kubeClient, &p)
			if err != nil {
				onError(err)
				return drain.MakePodDeleteStatusWithError(err.Error())
			}
			if usesSRIOV {
				return drain.PodDeleteStatus{
					Delete:  true,
					Reason:  "pod uses SR-IOV DRA resources",
					Message: "SR-IOV network operator draining the node",
				}
			}
			for _, c := range p.Spec.Containers {
				if c.Resources.Requests != nil {
					for r := range c.Resources.Requests {
						if strings.HasPrefix(r.String(), vars.ResourcePrefix) {
							return drain.PodDeleteStatus{
								Delete:  true,
								Reason:  "pod contain SR-IOV device",
								Message: "SR-IOV network operator draining the node",
							}
						}
					}
				}
			}
			return drain.PodDeleteStatus{Delete: false}
		}

		drainer.AdditionalFilters = []drain.PodFilter{deleteFunction}
	}

	return drainer
}

// podUsesSRIOVDRA checks allocated drivers for sriovnetwork.k8snetworkplumbingwg.io.
func podUsesSRIOVDRA(ctx context.Context, kubeClient kubernetes.Interface, pod *corev1.Pod) (bool, error) {
	claimNames := make(map[string]struct{})
	for _, claim := range pod.Spec.ResourceClaims {
		if claim.ResourceClaimName != nil {
			claimNames[*claim.ResourceClaimName] = struct{}{}
		} else if claim.ResourceClaimTemplateName != nil {
			for _, status := range pod.Status.ResourceClaimStatuses {
				if status.Name == claim.Name && status.ResourceClaimName != nil {
					claimNames[*status.ResourceClaimName] = struct{}{}
					break
				}
			}
		}
	}
	if status := pod.Status.ExtendedResourceClaimStatus; status != nil && status.ResourceClaimName != "" {
		claimNames[status.ResourceClaimName] = struct{}{}
	}

	for name := range claimNames {
		claim, err := kubeClient.ResourceV1().ResourceClaims(pod.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("resolve ResourceClaim %s/%s for pod %s/%s: %w", pod.Namespace, name, pod.Namespace, pod.Name, err)
		}
		// Without an allocation, the pod cannot have prepared any DRA devices.
		if claim.Status.Allocation == nil {
			continue
		}
		for _, device := range claim.Status.Allocation.Devices.Results {
			if device.Driver == draconsts.DriverName {
				return true, nil
			}
		}
	}
	return false, nil
}

// isIgnoredDrainErrOutMessage reports ErrOut lines that should not be recorded as
// drain errors, such as empty output and kubectl WARNING: progress (e.g. DaemonSet pods).
func isIgnoredDrainErrOutMessage(msg string) bool {
	cleanMsg := strings.TrimSpace(strings.ReplaceAll(msg, "\n", ""))
	return cleanMsg == "" || strings.HasPrefix(cleanMsg, "WARNING:")
}

// IsInformationalDrainErrOutMessage reports whether msg is a non-terminal progress or
// warning line from k8s.io/kubectl/pkg/drain (v0.37.1). Terminal drain failures are
// returned from RunNodeDrain and are not written to ErrOut.
//
// Known ErrOut patterns:
//   - default.go RunNodeDrain: "WARNING: %s" for pods that will not be deleted
//   - drain.go evictPods (TooManyRequests): "error when evicting pods/... (will retry after ...)"
//   - drain.go evictPods (namespace terminating): "error when evicting pod ... from terminating namespace ... (will retry after ...)"
func IsInformationalDrainErrOutMessage(msg string) bool {
	if isIgnoredDrainErrOutMessage(msg) {
		return true
	}
	cleanMsg := strings.TrimSpace(strings.ReplaceAll(msg, "\n", ""))
	return strings.Contains(cleanMsg, "(will retry after")
}
