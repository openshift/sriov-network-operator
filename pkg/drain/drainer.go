package drain

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
	"sigs.k8s.io/controller-runtime/pkg/log"

	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// writer implements io.Writer interface as a pass-through for log.Log.
type writer struct {
	logFunc func(msg string, keysAndValues ...interface{})
}

// Write passes string(p) into writer's logFunc and always returns len(p)
func (w writer) Write(p []byte) (n int, err error) {
	w.logFunc(string(p))
	return len(p), nil
}

type DrainInterface interface {
	DrainNode(context.Context, *corev1.Node, bool, bool) (bool, error)
	CompleteDrainNode(context.Context, *corev1.Node) (bool, error)
}

type Drainer struct {
	kubeClient      kubernetes.Interface
	platformHelpers platforms.Interface
}

func NewDrainer(platformHelpers platforms.Interface) (DrainInterface, error) {
	kclient, err := kubernetes.NewForConfig(vars.Config)
	if err != nil {
		return nil, err
	}

	return &Drainer{
		kubeClient:      kclient,
		platformHelpers: platformHelpers,
	}, err
}

// DrainNode the function cordon a node and drain pods from it
// if fullNodeDrain true all the pods on the system will get drained
// for openshift system we also pause the machine config pool this machine is part of it
func (d *Drainer) DrainNode(ctx context.Context, node *corev1.Node, fullNodeDrain, singleNode bool) (bool, error) {
	reqLogger := log.FromContext(ctx).WithValues("drain node", node.Name)
	reqLogger.Info("drainNode(): Node drain requested", "node", node.Name)

	completed, err := d.platformHelpers.OpenshiftBeforeDrainNode(ctx, node)
	if err != nil {
		reqLogger.Error(err, "error running OpenshiftDrainNode")
		return false, err
	}

	if !completed {
		reqLogger.Info("OpenshiftDrainNode did not finish re queue the node request")
		return false, nil
	}

	// Check if we are on a single node, and we require a reboot/full-drain we just return
	if fullNodeDrain && singleNode {
		return true, nil
	}

	drainHelper := createDrainHelper(d.kubeClient, ctx, fullNodeDrain)
	backoff := wait.Backoff{
		Steps:    5,
		Duration: 10 * time.Second,
		Factor:   2,
	}
	var lastErr error

	reqLogger.Info("drainNode(): Start draining")
	if err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := drain.RunCordonOrUncordon(drainHelper, node, true)
		if err != nil {
			lastErr = err
			reqLogger.Info("drainNode(): Cordon failed, retrying", "error", err)
			return false, nil
		}
		err = drain.RunNodeDrain(drainHelper, node.Name)
		if err == nil {
			return true, nil
		}
		lastErr = err
		reqLogger.Info("drainNode(): Draining failed, retrying", "error", err)
		return false, nil
	}); err != nil {
		if wait.Interrupted(err) {
			reqLogger.Info("drainNode(): failed to drain node", "steps", backoff.Steps, "error", lastErr)
		}
		reqLogger.Info("drainNode(): failed to drain node", "error", err)
		return false, err
	}
	reqLogger.Info("drainNode(): Drain completed")
	return true, nil
}

// CompleteDrainNode run un-cordon for the requested node
// for openshift system we also remove the pause from the machine config pool this node is part of
// only if we are the last draining node on that pool
func (d *Drainer) CompleteDrainNode(ctx context.Context, node *corev1.Node) (bool, error) {
	logger := log.FromContext(ctx)
	logger.Info("CompleteDrainNode:()")

	// Create drain helper object
	// full drain is not important here
	drainHelper := createDrainHelper(d.kubeClient, ctx, false)

	// run the un cordon function on the node
	if err := drain.RunCordonOrUncordon(drainHelper, node, false); err != nil {
		logger.Error(err, "failed to un-cordon the node")
		return false, err
	}

	// call the openshift complete drain to unpause the MCP
	// only if we are the last draining node in the pool
	completed, err := d.platformHelpers.OpenshiftAfterCompleteDrainNode(ctx, node)
	if err != nil {
		logger.Error(err, "failed to complete openshift draining")
		return false, err
	}

	logger.V(2).Info("CompleteDrainNode:()", "drainCompleted", completed)
	return completed, nil
}

// createDrainHelper function to create a drain helper
// if fullDrain is false we only remove pods that have the resourcePrefix
// if not we remove all the pods in the node
func createDrainHelper(kubeClient kubernetes.Interface, ctx context.Context, fullDrain bool) *drain.Helper {
	logger := log.FromContext(ctx)
	drainer := &drain.Helper{
		Client:              kubeClient,
		Force:               true,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		GracePeriodSeconds:  -1,
		Timeout:             90 * time.Second,
		OnPodDeletedOrEvicted: func(pod *corev1.Pod, usingEviction bool) {
			verbStr := constants.DrainDeleted
			if usingEviction {
				verbStr = constants.DrainEvicted
			}
			log.Log.Info(fmt.Sprintf("%s pod from Node %s/%s", verbStr, pod.Namespace, pod.Name))
		},
		Ctx:    ctx,
		Out:    writer{logger.Info},
		ErrOut: writer{func(msg string, kv ...interface{}) { logger.Error(nil, msg, kv...) }},
	}

	// when we just want to drain and not reboot we can only remove the pods using sriov devices
	if !fullDrain {
		deleteFunction := func(p corev1.Pod) drain.PodDeleteStatus {
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
