package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubectl/pkg/drain"
	"math/rand"
	"time"
)

const (
	annoKey      = "sriovnetwork.openshift.io/state"
	annoIdle     = "Idle"
	annoDraining = "Draining"
)

// TODO: Changes this manager to use leader election
type DrainManager struct {
	// name is the node name.
	name string

	kubeClient *kubernetes.Clientset

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	drainer *drain.Helper

	node *corev1.Node

	drainable bool

	nodeLister listerv1.NodeLister
}

// writer implements io.Writer interface as a pass-through for klog.
type writer struct {
	logFunc func(args ...interface{})
}

// Write passes string(p) into writer's logFunc and always returns len(p)
func (w writer) Write(p []byte) (n int, err error) {
	w.logFunc(string(p))
	return len(p), nil
}

func NewDrainManager(kubeClient *kubernetes.Clientset, nodeName string, stopCh <-chan struct{}) *DrainManager {
	return &DrainManager{
		name:       nodeName,
		kubeClient: kubeClient,
		drainable:  true,
		stopCh:     stopCh,
		drainer: &drain.Helper{
			Client:              kubeClient,
			Force:               true,
			IgnoreAllDaemonSets: true,
			DeleteLocalData:     true,
			GracePeriodSeconds:  -1,
			Timeout:             90 * time.Second,
			OnPodDeletedOrEvicted: func(pod *corev1.Pod, usingEviction bool) {
				verbStr := "Deleted"
				if usingEviction {
					verbStr = "Evicted"
				}
				glog.Info(fmt.Sprintf("%s pod from Node", verbStr),
					"pod", fmt.Sprintf("%s/%s", pod.Name, pod.Namespace))
			},
			Out:    writer{glog.Info},
			ErrOut: writer{glog.Error},
		},
	}
}

func (dm *DrainManager) Run(errCh chan<- error) {
	glog.V(0).Info("Run(): start drain manager")
	rand.Seed(time.Now().UnixNano())
	nodeInformerFactory := informers.NewSharedInformerFactory(dm.kubeClient,
		time.Second*15,
	)
	dm.nodeLister = nodeInformerFactory.Core().V1().Nodes().Lister()
	nodeInformer := nodeInformerFactory.Core().V1().Nodes().Informer()
	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    dm.nodeAddHandler,
		UpdateFunc: dm.nodeUpdateHandler,
	})

	go nodeInformer.Run(dm.stopCh)
	if ok := cache.WaitForCacheSync(dm.stopCh, nodeInformer.HasSynced); !ok {
		errCh <- fmt.Errorf("failed to wait for drain manager node cache to sync")
		return
	}

	// First update
	dm.nodeUpdateHandler(nil, nil)
}

func (dm *DrainManager) nodeAddHandler(obj interface{}) {
	dm.nodeUpdateHandler(nil, obj)
}

func (dm *DrainManager) nodeUpdateHandler(old, new interface{}) {
	node, err := dm.nodeLister.Get(dm.name)
	if errors.IsNotFound(err) {
		glog.V(2).Infof("nodeUpdateHandler(): node %v has been deleted", dm.name)
		return
	}

	dm.node = node.DeepCopy()
	nodes, err := dm.nodeLister.List(labels.Everything())
	if err != nil {
		return
	}
	for _, node := range nodes {
		if node.GetName() != dm.name && node.Annotations[annoKey] == annoDraining {
			glog.V(2).Infof("nodeUpdateHandler(): node %s is draining", node.Name)
			dm.drainable = false
			return
		}
	}

	glog.V(2).Infof("nodeUpdateHandler(): no other node is draining")
	dm.drainable = true
}

func (dm *DrainManager) CompleteDrain() error {
	var lastErr error
	err := wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
		if lastErr = drain.RunCordonOrUncordon(dm.drainer, dm.node, false); lastErr != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		glog.Errorf("completeDrain(): fail to uncordon node: %v: %v", err, lastErr)
		return err
	}
	if err = dm.annotateNode(dm.name, annoIdle); err != nil {
		glog.Errorf("completeDrain(): failed to annotate node: %v", err)
		return err
	}
	return nil
}

func (dm *DrainManager) annotateNode(node, value string) error {
	glog.Infof("annotateNode(): Annotate node %s with: %s", node, value)

	err := wait.PollImmediate(10*time.Second, 2*time.Minute, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		oldNode, err := dm.kubeClient.CoreV1().Nodes().Get(ctx, dm.name, metav1.GetOptions{})
		if err != nil {
			glog.Infof("annotateNode(): Failed to get node %s %v, retrying", node, err)
			return false, nil
		}
		oldData, err := json.Marshal(oldNode)
		if err != nil {
			return false, err
		}

		newNode := oldNode.DeepCopy()
		if newNode.Annotations == nil {
			newNode.Annotations = map[string]string{}
		}
		if newNode.Annotations[annoKey] != value {
			newNode.Annotations[annoKey] = value
			newData, err := json.Marshal(newNode)
			if err != nil {
				return false, err
			}
			patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, corev1.Node{})
			if err != nil {
				return false, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = dm.kubeClient.CoreV1().Nodes().Patch(ctx,
				dm.name,
				types.StrategicMergePatchType,
				patchBytes,
				metav1.PatchOptions{})
			if err != nil {
				glog.Infof("annotateNode(): Failed to patch node %s %v, retrying", node, err)
				return false, nil
			}
		}
		return true, nil
	})
	return err
}

func (dm *DrainManager) DrainNode() error {
	glog.Info("DrainNode(): Update prepared")
	var err error
	// wait a random time to avoid all the nodes drain at the same time
	wait.PollUntil(time.Duration(rand.Intn(15)+1)*time.Second, func() (bool, error) {
		if !dm.drainable {
			glog.Info("drainNode(): other node is draining, waiting...")
		}
		return dm.drainable, nil
	}, dm.stopCh)

	err = dm.annotateNode(dm.name, annoDraining)
	if err != nil {
		glog.Errorf("drainNode(): Failed to annotate node: %v", err)
		return err
	}

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 10 * time.Second,
		Factor:   2,
	}
	var lastErr error

	glog.Info("drainNode(): Start draining")
	if err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := drain.RunCordonOrUncordon(dm.drainer, dm.node, true)
		if err != nil {
			lastErr = err
			glog.Infof("Cordon failed with: %v, retrying", err)
			return false, nil
		}
		err = drain.RunNodeDrain(dm.drainer, dm.name)
		if err == nil {
			return true, nil
		}
		lastErr = err
		glog.Infof("Draining failed with: %v, retrying", err)
		return false, nil
	}); err != nil {
		if err == wait.ErrWaitTimeout {
			glog.Errorf("drainNode(): failed to drain node (%d tries): %v :%v", backoff.Steps, err, lastErr)
		}
		glog.Errorf("drainNode(): failed to drain node: %v", err)
		return err
	}
	glog.Info("drainNode(): drain complete")
	return nil
}

func (dm *DrainManager) AnnotateNode() error {
	if anno, ok := dm.node.Annotations[annoKey]; ok && anno == annoDraining {
		if err := dm.CompleteDrain(); err != nil {
			glog.Errorf("AnnotateNode(): failed to complete draining: %v", err)
			return err
		}
	} else if !ok {
		if err := dm.annotateNode(dm.name, annoIdle); err != nil {
			glog.Errorf("AnnotateNode(): failed to annotate node: %v", err)
			return err
		}
	}

	return nil
}
