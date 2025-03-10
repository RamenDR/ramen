// Copyright Contributors to the Open Cluster Management project

package client

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// defaultBufferSize is the size of the underlying channel used when integrating with controller-runtime.
const defaultBufferSize = 1024

// NewControllerRuntimeSource returns a reconciler for DynamicWatcher that sends events to a controller-runtime
// source.Channel. This source.Channel can be used in the controller-runtime builder.Builder.WatchesRawSource method.
// This source.Channel will only send event.GenericEvent typed events, so any handlers specified in the
// builder.Builder.WatchesRawSource method will need to handle that.
func NewControllerRuntimeSource() (*ControllerRuntimeSourceReconciler, source.Source) {
	eventChan := make(chan event.GenericEvent, defaultBufferSize)
	sourceChan := source.Channel(eventChan, &handler.EnqueueRequestForObject{})

	return &ControllerRuntimeSourceReconciler{eventChan}, sourceChan
}

// ControllerRuntimeSourceReconciler is a reconciler to integrate with controller-runtime. See
// NewControllerRuntimeSource.
type ControllerRuntimeSourceReconciler struct {
	eventChan chan<- event.GenericEvent
}

// Reconcile will convert the input ObjectIdentifier and send a controller-runtime GenericEvent on
// ControllerRuntimeSourceReconciler's eventChan channel.
func (t *ControllerRuntimeSourceReconciler) Reconcile(
	_ context.Context, watcher ObjectIdentifier,
) (
	reconcile.Result, error,
) {
	watcherObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": watcher.GroupVersionKind().GroupVersion().String(),
			"kind":       watcher.Kind,
			"metadata": map[string]interface{}{
				"name":      watcher.Name,
				"namespace": watcher.Namespace,
			},
		},
	}

	t.eventChan <- event.GenericEvent{Object: watcherObj}

	return reconcile.Result{}, nil
}
