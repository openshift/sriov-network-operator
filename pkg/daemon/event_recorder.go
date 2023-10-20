package daemon

import (
	"context"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedv1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"

	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
)

type EventRecorder struct {
	client           snclientset.Interface
	node             string
	eventRecorder    record.EventRecorder
	eventBroadcaster record.EventBroadcaster
}

// NewEventRecorder Create a new EventRecorder
func NewEventRecorder(c snclientset.Interface, n string, kubeclient kubernetes.Interface) *EventRecorder {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(4)
	eventBroadcaster.StartRecordingToSink(&typedv1core.EventSinkImpl{Interface: kubeclient.CoreV1().Events("")})
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "config-daemon"})
	return &EventRecorder{
		client:           c,
		node:             n,
		eventRecorder:    eventRecorder,
		eventBroadcaster: eventBroadcaster,
	}
}

// SendEvent Send an Event on the NodeState object
func (e *EventRecorder) SendEvent(eventType string, msg string) {
	nodeState, err := e.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(context.Background(), e.node, metav1.GetOptions{})
	if err != nil {
		glog.Warningf("SendEvent(): Failed to fetch node state %s (%v); skip SendEvent", e.node, err)
		return
	}
	e.eventRecorder.Event(nodeState, corev1.EventTypeNormal, eventType, msg)
}

// Shutdown Close the EventBroadcaster
func (e *EventRecorder) Shutdown() {
	e.eventBroadcaster.Shutdown()
}
