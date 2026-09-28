package daemon

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// EventRecorder wraps the events.k8s.io EventRecorder to send events on the
// SriovNetworkNodeState object for this node.
type EventRecorder struct {
	client   client.Client
	recorder events.EventRecorder
}

// NewEventRecorder creates a new EventRecorder using the events.k8s.io API.
func NewEventRecorder(c client.Client, recorder events.EventRecorder) *EventRecorder {
	return &EventRecorder{
		client:   c,
		recorder: recorder,
	}
}

// SendEvent sends an Event on the NodeState object for the current node.
func (e *EventRecorder) SendEvent(ctx context.Context, eventType string, msg string) {
	nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
	err := e.client.Get(ctx, client.ObjectKey{Namespace: vars.Namespace, Name: vars.NodeName}, nodeState)
	if err != nil {
		log.Log.V(2).Error(err, "SendEvent(): Failed to fetch node state, skip SendEvent", "name", vars.NodeName)
		return
	}
	e.recorder.Eventf(nodeState, nil, corev1.EventTypeNormal, eventType, "DaemonEvent", "%s", msg)
}
