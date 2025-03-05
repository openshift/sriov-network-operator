package daemon

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedv1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type EventRecorder struct {
	client           snclientset.Interface
	eventRecorder    record.EventRecorder
	eventBroadcaster record.EventBroadcaster
}

// NewEventRecorder Create a new EventRecorder
func NewEventRecorder(c snclientset.Interface, kubeclient kubernetes.Interface, s *runtime.Scheme) *EventRecorder {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(4)
	eventBroadcaster.StartRecordingToSink(&typedv1core.EventSinkImpl{Interface: kubeclient.CoreV1().Events("")})
	eventRecorder := eventBroadcaster.NewRecorder(s, corev1.EventSource{Component: "config-daemon"})
	return &EventRecorder{
		client:           c,
		eventRecorder:    eventRecorder,
		eventBroadcaster: eventBroadcaster,
	}
}

// SendEvent Send an Event on the NodeState object
func (e *EventRecorder) SendEvent(ctx context.Context, eventType string, msg string) {
	nodeState, err := e.client.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(ctx, vars.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Log.V(2).Error(err, "SendEvent(): Failed to fetch node state, skip SendEvent", "name", vars.NodeName)
		return
	}
	e.eventRecorder.Event(nodeState, corev1.EventTypeNormal, eventType, msg)
}

// Shutdown Close the EventBroadcaster
func (e *EventRecorder) Shutdown() {
	e.eventBroadcaster.Shutdown()
}
