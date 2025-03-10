// Copyright Contributors to the Open Cluster Management project

// Package client is an event-driven Go library used when Kubernetes objects need to track when other objects change.
// The API is heavily based on the popular sigs.k8s.io/controller-runtime library.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiWatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/watch"
	"k8s.io/client-go/util/workqueue"
	klog "k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	ErrNotStarted           = errors.New("DynamicWatcher must be started to perform this action")
	ErrCacheDisabled        = errors.New("cannot perform this action because the cache is not enabled")
	ErrInvalidInput         = errors.New("invalid input provided")
	ErrNoVersionedResource  = errors.New("the resource version was not found")
	ErrQueryBatchInProgress = errors.New(
		"cannot perform this action; the query batch for this object ID is in progress",
	)
	ErrQueryBatchNotStarted = errors.New("the query batch for this object ID is not started")
	ErrWatchStopping        = errors.New("the watched object has a watch being stopped")
)

type Reconciler interface {
	// Reconcile is called whenever an object has started being watched (if it exists) as well as when it has changed
	// (added, modified, or deleted). If the watch stops prematurely and is restarted, it may cause a duplicate call to
	// this method. If an error is returned, the request is requeued.
	Reconcile(ctx context.Context, watcher ObjectIdentifier) (reconcile.Result, error)
}

// Options specify the arguments for creating a new DynamicWatcher.
type Options struct {
	// RateLimiter is used to limit how frequently requests may be queued.
	// Defaults to client-go's MaxOfRateLimiter which has both overall and per-item rate limiting. The overall is a
	// token bucket and the per-item is exponential.
	RateLimiter workqueue.TypedRateLimiter[ObjectIdentifier]
	// EnableCache causes the watched objects to be cached and retrievable.
	EnableCache bool
	// DisableInitialReconcile causes the initial reconcile from the list request before the watch to not cause a
	// reconcile. This is useful if you are exclusively using the caching query API.
	DisableInitialReconcile bool
	// Options for how long to cache GVK to GVR conversions, and whether to disable the DeepCopy when retrieving items
	// from the cache.
	ObjectCacheOptions ObjectCacheOptions
}

// ObjectIdentifier identifies an object from the Kubernetes API.
type ObjectIdentifier struct {
	Group     string
	Version   string
	Kind      string
	Namespace string
	Name      string
	Selector  string
}

// String will convert the ObjectIdentifer to a string in a similar format to apimachinery's schema.GroupVersionKind.
func (o ObjectIdentifier) String() string {
	s := fmt.Sprintf(
		"GroupVersion=%v, Kind=%v, Namespace=%v, Name=%v",
		o.GroupVersionKind().GroupVersion(), o.Kind, o.Namespace, o.Name,
	)

	if o.Selector != "" {
		return s + ", Selector=" + o.Selector
	}

	return s
}

// Validate will return a wrapped ErrInvalidInput error when a required field is not set on the ObjectIdentifier.
func (o ObjectIdentifier) Validate() error {
	if o.Version == "" {
		return fmt.Errorf("%w: the ObjectIdentifier (%s) Version must be set", ErrInvalidInput, o)
	}

	if o.Kind == "" {
		return fmt.Errorf("%w: the ObjectIdentifier (%s) Kind must be set", ErrInvalidInput, o)
	}

	if o.Name == "" && o.Selector == "" {
		return fmt.Errorf("%w: the ObjectIdentifier (%s) either the Name or Selector must be set", ErrInvalidInput, o)
	}

	if o.Name != "" && o.Selector != "" {
		return fmt.Errorf("%w: the ObjectIdentifier (%s) only one of Name or Selector can be set", ErrInvalidInput, o)
	}

	if o.Selector != "" {
		if _, err := metav1.ParseToLabelSelector(o.Selector); err != nil {
			return fmt.Errorf("%w: the ObjectIdentifier (%s) has an invalid Selector", ErrInvalidInput, o)
		}
	}

	return nil
}

// GVK returns the GroupVersionKind of the ObjectIdentifier object.
func (o ObjectIdentifier) GroupVersionKind() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: o.Group, Version: o.Version, Kind: o.Kind}
}

// DynamicWatcher implementations enable a consumer to be notified of updates to Kubernetes objects that other
// Kubernetes objects depend on. It also provides a cache to retrieve the watched objects.
type DynamicWatcher interface { //nolint: interfacebloat
	AddWatcher(watcher ObjectIdentifier, watchedObject ObjectIdentifier) error
	// AddOrUpdateWatcher updates the watches for the watcher. When updating, any previously watched objects not
	// specified will stop being watched. If an error occurs, any created watches as part of this method execution will
	// be removed.
	AddOrUpdateWatcher(watcher ObjectIdentifier, watchedObjects ...ObjectIdentifier) error
	// RemoveWatcher removes a watcher and any of its API watches solely referenced by the watcher.
	RemoveWatcher(watcher ObjectIdentifier) error
	// Start will start the DynamicWatcher and block until the input context is canceled.
	Start(ctx context.Context) error
	// GetWatchCount returns the total number of active API watch requests which can be used for metrics.
	GetWatchCount() uint
	// Started returns a channel that is closed when the DynamicWatcher is ready to receive watch requests.
	Started() <-chan struct{}
	// Get will add an additional watch to the started query batch and return the watched object. Note that you must
	// call StartQueryBatch before calling this.
	Get(
		watcher ObjectIdentifier, gvk schema.GroupVersionKind, namespace string, name string,
	) (*unstructured.Unstructured, error)
	// GetFromCache will return the object from the cache. If it's not cached, the ErrNoCacheEntry error will be
	// returned.
	GetFromCache(gvk schema.GroupVersionKind, namespace string, name string) (*unstructured.Unstructured, error)
	// List will add an additional watch to the started query batch and return the watched objects. Note that you must
	// call StartQueryBatch before calling this.
	List(
		watcher ObjectIdentifier, gvk schema.GroupVersionKind, namespace string, selector labels.Selector,
	) ([]unstructured.Unstructured, error)
	// ListFromCache will return the objects from the cache. If it's not cached, the ErrNoCacheEntry error will be
	// returned.
	ListFromCache(
		gvk schema.GroupVersionKind, namespace string, selector labels.Selector,
	) ([]unstructured.Unstructured, error)
	// ListWatchedFromCache will return all watched objects by the watcher in the cache.
	ListWatchedFromCache(watcher ObjectIdentifier) ([]unstructured.Unstructured, error)
	// StartQueryBatch will start a query batch transaction for the watcher. After a series of Get/List calls, calling
	// EndQueryBatch will clean up the non-applicable preexisting watches made from before this query batch.
	StartQueryBatch(watcher ObjectIdentifier) error
	// EndQueryBatch will stop a query batch transaction for the watcher. This will clean up the non-applicable
	// preexisting watches made from before this query batch.
	EndQueryBatch(watcher ObjectIdentifier) error
	// GVKToGVR will convert a GVK to a GVR and cache the result for a default of 10 minutes (configurable) when found,
	// and not cache failed conversions by default (configurable).
	GVKToGVR(gvk schema.GroupVersionKind) (ScopedGVR, error)
}

// New returns an implementation of DynamicWatcher that is ready to be started with the Start method. An error is
// returned if Kubernetes clients can't be instantiated with the input Kubernetes configuration.
func New(config *rest.Config, reconciler Reconciler, options *Options) (DynamicWatcher, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize a dynamic Kubernetes client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize a discovery Kubernetes client: %w", err)
	}

	return NewWithClients(dynamicClient, discoveryClient, reconciler, options), nil
}

// NewWithClients returns an implementation of DynamicWatcher that that is ready to be started with the Start method.
func NewWithClients(
	dynamicClient dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
	reconciler Reconciler,
	options *Options,
) DynamicWatcher {
	if options == nil {
		options = &Options{}
	}

	rateLimiter := options.RateLimiter
	if rateLimiter == nil {
		rateLimiter = workqueue.DefaultTypedControllerRateLimiter[ObjectIdentifier]()
	}

	return &dynamicWatcher{
		dynamicClient:     dynamicClient,
		queryBatches:      sync.Map{},
		objectCache:       NewObjectCache(discoveryClient, options.ObjectCacheOptions),
		options:           *options,
		rateLimiter:       rateLimiter,
		Reconciler:        reconciler,
		startedChan:       make(chan struct{}),
		watchedToWatchers: map[ObjectIdentifier]map[ObjectIdentifier]bool{},
		watcherToWatches:  map[ObjectIdentifier]map[ObjectIdentifier]bool{},
		watches:           map[ObjectIdentifier]*watchWithHandshake{},
	}
}

type queryBatch struct {
	lock              *sync.RWMutex
	previouslyWatched map[ObjectIdentifier]bool
	// This can be updated with a read lock since this itself is concurrency safe.
	newWatched *sync.Map
	complete   bool
}

type ScopedGVR struct {
	schema.GroupVersionResource
	Namespaced bool
}

type watchWithHandshake struct {
	watch          apiWatch.Interface
	requestingStop bool
	stopped        chan ObjectIdentifier
}

// dynamicWatcher implements the DynamicWatcher interface.
type dynamicWatcher struct {
	// dynamicClient is a client-go dynamic client used for the dynamic watches.
	dynamicClient dynamic.Interface
	lock          sync.RWMutex
	Queue         workqueue.TypedRateLimitingInterface[ObjectIdentifier]
	Reconciler
	rateLimiter workqueue.TypedRateLimiter[ObjectIdentifier]
	objectCache ObjectCache
	options     Options
	// queryBatches is a sync.Map where the keys are watcher object identifiers and the values are queryBatch pointers.
	// This gets created after a call to StartQueryBatch and gets removed when EndQueryBatch is called. This will
	// keep track of the watches added in a batch from Get/List calls.
	queryBatches sync.Map
	// started gets set as part of the Start method.
	started bool
	// startedChan is closed when the dynamicWatcher is started. This is exposed to the user through the Started method.
	startedChan chan struct{}
	// watchedToWatchers is a map where the keys are ObjectIdentifier objects representing the watched objects.
	// Each value acts as a set of ObjectIdentifier objects representing the watcher objects.
	watchedToWatchers map[ObjectIdentifier]map[ObjectIdentifier]bool
	// watcherToWatches is a map where the keys are ObjectIdentifier objects representing the watcher objects.
	// Each value acts as a set of ObjectIdentifier objects representing the watched objects.
	watcherToWatches map[ObjectIdentifier]map[ObjectIdentifier]bool
	// watches is a map where the keys are ObjectIdentifier objects representing the watched objects and the values
	// are objects representing the Kubernetes watch API requests.
	watches map[ObjectIdentifier]*watchWithHandshake
}

// Start will start the dynamicWatcher and block until the input context is canceled.
func (d *dynamicWatcher) Start(ctx context.Context) error {
	klog.Info("Starting the dynamic watcher")

	d.Queue = workqueue.NewTypedRateLimitingQueue[ObjectIdentifier](d.rateLimiter)

	go func() {
		<-ctx.Done()

		klog.Info("Shutdown signal received, cleaning up the dynamic watcher")

		d.Queue.ShutDown()
	}()

	d.started = true
	close(d.startedChan)

	//nolint: revive
	for d.processNextWorkItem(ctx) {
	}

	klog.V(2).Infof("Cleaning up the %d leftover watches", len(d.watches))

	for watcher := range d.watcherToWatches {
		if err := d.RemoveWatcher(watcher); err != nil {
			klog.Errorf("Failed to remove a watch for %s: %v", watcher, err)
		}
	}

	d.lock.Lock()
	d.startedChan = make(chan struct{})
	d.started = false
	d.lock.Unlock()

	return nil
}

// Started returns a channel that is closed when the dynamicWatcher is ready to receive watch requests.
func (d *dynamicWatcher) Started() <-chan struct{} {
	return d.startedChan
}

// relayWatchEvents will watch a channel tied to a Kubernetes API watch and then relay changes to the dynamicWatcher
// queue. If the watch stops unintentionally after retries from the client-go RetryWatcher, it will be restarted at the
// latest resourceVersion. This usually happens if the retry watcher tries to start watching again at a resource version
// that is no longer in etcd. Note that if the RetryWatcher's client is forbidden to watch the resource after starting,
// the watch is cleaned up and each watcher will get added to the dynamicWatcher queue.
func (d *dynamicWatcher) relayWatchEvents(
	watchedObject ObjectIdentifier,
	resource dynamic.ResourceInterface,
	sendInitialEvent bool,
	watch *watchWithHandshake,
) {
	for {
		// Send an initial event when the watch is started and the object exists to replicate the list and watch
		// behavior of controller-runtime. A watch restart can also trigger this to account for a lost event.
		if sendInitialEvent {
			d.lock.RLock()

			for watcher := range d.watchedToWatchers[watchedObject] {
				d.Queue.Add(watcher)
			}

			d.lock.RUnlock()

			sendInitialEvent = false
		}

		// This for loop exits when the events channel closes, thus signaling that the RetryWatch stopped.
		for event := range watch.watch.ResultChan() {
			// A watch error is usually from the watch request being explicitly stopped. This is why it's considered a
			// debug log.
			if event.Type == apiWatch.Error {
				klog.V(2).Infof("An error was received from the watch request: %v", event.Object)

				continue
			}

			// This does not need a write lock to avoid a race condition because there is only ever one watch
			// per object.
			d.updateCacheFromWatchEvent(watchedObject, event)

			d.lock.RLock()

			for watcher := range d.watchedToWatchers[watchedObject] {
				d.Queue.Add(watcher)
			}

			d.lock.RUnlock()
		}

		// The RetryWatch stopped, so figure out if this was intentional or due to an error.
		if watch.requestingStop {
			klog.V(2).Infof("A watch channel for the watcher %s was closed", watchedObject)

			if d.options.EnableCache {
				// This does not need a write lock to avoid a race condition because there is only ever one watch
				// per object and a new watch can't be added on this object until this is cleaned up.
				d.objectCache.UncacheFromObjectIdentifier(watchedObject)
			}

			// Let the caller know that watch has cleaned up.
			watch.stopped <- watchedObject
			close(watch.stopped)

			return
		}

		klog.V(1).Infof("Restarting the watch request for %s", watchedObject)

		w, watchedObjects, err := watchLatest(watchedObject, resource)
		if err != nil {
			if k8serrors.IsForbidden(err) || k8serrors.IsUnauthorized(err) {
				klog.Errorf(
					"Could not restart a watch request due to the client no longer having access. "+
						"Cleaning up and starting reconciles for the watchers. Error: %v",
					err,
				)

				d.lock.Lock()
				defer d.lock.Unlock()

				for watcher := range d.watchedToWatchers[watchedObject] {
					delete(d.watcherToWatches[watcher], watchedObject)
					delete(d.watchedToWatchers[watchedObject], watcher)

					d.Queue.Add(watcher)
				}

				delete(d.watches, watchedObject)

				if d.options.EnableCache {
					d.objectCache.UncacheFromObjectIdentifier(watchedObject)
				}

				return
			}

			klog.Errorf(
				"Could not restart a watch request for %s. Trying again in 5 seconds. Error: %v", watchedObject, err,
			)
			time.Sleep(5 * time.Second)

			continue
		}

		if d.options.EnableCache {
			d.objectCache.CacheFromObjectIdentifier(watchedObject, watchedObjects)
		}

		d.lock.Lock()
		// Use the new watch channel which will be used in the next iteration of the loop.
		watch.watch = w
		d.lock.Unlock()

		// Send the initial event in case an action was lost between watch requests.
		sendInitialEvent = true
	}
}

// updateCacheFromWatchEvent will take a watch event and update the cache appropriately. This is meant to be called
// from relayWatchEvent.
func (d *dynamicWatcher) updateCacheFromWatchEvent(watchedObject ObjectIdentifier, watchEvent apiWatch.Event) {
	if !d.options.EnableCache {
		return
	}

	object, ok := watchEvent.Object.(*unstructured.Unstructured)
	if !ok {
		// No object returned from the watch
		return
	}

	cachedObjects, err := d.objectCache.FromObjectIdentifier(watchedObject)
	if err != nil && !errors.Is(err, ErrNoCacheEntry) {
		klog.Errorf("Failed to cache the watched object %s for %s: %v", object, watchedObject, err)

		return
	}

	switch eventType := (watchEvent.Type); eventType { //nolint: exhaustive
	case apiWatch.Added, apiWatch.Modified:
		updated := false

		for i := range cachedObjects {
			if cachedObjects[i].GroupVersionKind() != object.GroupVersionKind() {
				continue
			}

			if cachedObjects[i].GetName() != object.GetName() {
				continue
			}

			if cachedObjects[i].GetNamespace() != object.GetNamespace() {
				continue
			}

			// The object was previously cached and needs to be updated
			cachedObjects[i] = *object
			updated = true

			break
		}

		if !updated {
			cachedObjects = append(cachedObjects, *object)
		}

		d.objectCache.CacheFromObjectIdentifier(watchedObject, cachedObjects)
	case apiWatch.Deleted:
		indexToRemove := -1

		for i := range cachedObjects {
			if cachedObjects[i].GroupVersionKind() != object.GroupVersionKind() {
				continue
			}

			if cachedObjects[i].GetName() != object.GetName() {
				continue
			}

			if cachedObjects[i].GetNamespace() != object.GetNamespace() {
				continue
			}

			// The object was previously cached and needs to be deleted
			indexToRemove = i

			break
		}

		if indexToRemove != -1 {
			// It's important to preserve the order here so that callers get deterministic results
			cachedObjects = append(cachedObjects[:indexToRemove], cachedObjects[indexToRemove+1:]...)
		}

		d.objectCache.CacheFromObjectIdentifier(watchedObject, cachedObjects)
	}
}

// processNextWorkItem will read a single work item off the queue and attempt to process it, by calling the
// reconcileHandler. A bool is returned based on if the queue is shutdown due to the dynamicWatcher shutting down.
func (d *dynamicWatcher) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := d.Queue.Get()
	if shutdown {
		// Tell the caller to stop calling this method once the queue has shutdown.
		return false
	}

	// Let the queue know that item has been handled after the Reconcile method is called.
	defer d.Queue.Done(obj)

	d.reconcileHandler(ctx, obj)

	return true
}

// reconcileHandler takes an object from the queue and calls the user's Reconcile method.
func (d *dynamicWatcher) reconcileHandler(ctx context.Context, watcher ObjectIdentifier) {
	result, err := d.Reconcile(ctx, watcher)

	switch {
	case err != nil:
		d.Queue.AddRateLimited(watcher)
		klog.Errorf("Reconciler error: %v", err)
	case result.RequeueAfter > 0:
		// The result.RequeueAfter request will be lost, if it is returned
		// along with a non-nil error. But this is intended as
		// We need to drive to stable reconcile loops before queuing due
		// to result.RequestAfter
		d.Queue.Forget(watcher)
		d.Queue.AddAfter(watcher, result.RequeueAfter)
	case result.Requeue:
		d.Queue.AddRateLimited(watcher)
	default:
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		d.Queue.Forget(watcher)
	}
}

// AddOrUpdateWatcher updates the watches for the watcher. When updating, any previously watched objects not specified
// will stop being watched. If an error occurs, any created watches as part of this method execution will be removed.
func (d *dynamicWatcher) AddOrUpdateWatcher(watcher ObjectIdentifier, watchedObjects ...ObjectIdentifier) error {
	if !d.started {
		return ErrNotStarted
	}

	if len(watchedObjects) == 0 {
		return fmt.Errorf("%w: at least one watched object must be provided", ErrInvalidInput)
	}

	if err := watcher.Validate(); err != nil {
		return err
	}

	for _, watchedObject := range watchedObjects {
		if err := watchedObject.Validate(); err != nil {
			return err
		}
	}

	_, loaded := d.queryBatches.Load(watcher)
	if loaded {
		return ErrQueryBatchInProgress
	}

	d.lock.Lock()

	existingWatches := make(map[ObjectIdentifier]bool, len(d.watcherToWatches[watcher]))

	for key, val := range d.watcherToWatches[watcher] {
		existingWatches[key] = val
	}

	watchedObjectsSet := make(map[ObjectIdentifier]bool, len(watchedObjects))

	var encounteredErr error

	for i := range watchedObjects {
		encounteredErr = d.addWatcher(watcher, &watchedObjects[i])
		if encounteredErr != nil {
			break
		}

		watchedObjectsSet[watchedObjects[i]] = true
	}

	stoppedWatches := []<-chan ObjectIdentifier{}

	if encounteredErr == nil {
		for existingWatchedObject := range existingWatches {
			if watchedObjectsSet[existingWatchedObject] {
				continue
			}

			stoppedWatches = append(stoppedWatches, d.removeWatch(watcher, existingWatchedObject))
		}

		d.watcherToWatches[watcher] = watchedObjectsSet
	} else {
		// If an error was encountered, remove the watches that were added to revert back to the previous state.
		for _, watchedObject := range watchedObjects {
			// watchedObjectsSet[watchedObject] is only set if an error wasn't encountered creating the watch.
			// We can ignore the ones that failed or weren't processed.
			if watchedObjectsSet[watchedObject] && !existingWatches[watchedObject] {
				stoppedWatches = append(stoppedWatches, d.removeWatch(watcher, watchedObject))
			}
		}

		if len(existingWatches) > 0 {
			d.watcherToWatches[watcher] = existingWatches
		} else {
			delete(d.watcherToWatches, watcher)
		}
	}

	d.lock.Unlock()

	d.waitForStoppedWatches(stoppedWatches)

	return encounteredErr
}

// addWatcher will start a watch for the watcher. Note that it's expected that the lock is already
// acquired by the caller.
func (d *dynamicWatcher) addWatcher(watcher ObjectIdentifier, watchedObject *ObjectIdentifier) error {
	if watch, ok := d.watches[*watchedObject]; ok && watch.requestingStop {
		return ErrWatchStopping
	}

	gvr, err := d.objectCache.GVKToGVR(watchedObject.GroupVersionKind())
	if err != nil {
		klog.Errorf("Could not get the GVR for %s, error: %v", watchedObject, err)

		return err
	}

	if !gvr.Namespaced { // ignore namespaces set on cluster-scoped resources
		watchedObject.Namespace = ""
	}

	if d.watcherToWatches[watcher] == nil {
		d.watcherToWatches[watcher] = map[ObjectIdentifier]bool{}
	}

	// If the object was previously watched, do nothing.
	if d.watcherToWatches[watcher][*watchedObject] {
		return nil
	}

	if len(d.watchedToWatchers[*watchedObject]) == 0 {
		d.watchedToWatchers[*watchedObject] = map[ObjectIdentifier]bool{}
	}

	// If the object is also watched by another object, then do nothing.
	if _, ok := d.watches[*watchedObject]; ok {
		d.watchedToWatchers[*watchedObject][watcher] = true
		d.watcherToWatches[watcher][*watchedObject] = true

		return nil
	}

	var resource dynamic.ResourceInterface = d.dynamicClient.Resource(gvr.GroupVersionResource)
	if watchedObject.Namespace != "" {
		resource = resource.(dynamic.NamespaceableResourceInterface).Namespace(watchedObject.Namespace)
	}

	w, watchedObjects, err := watchLatest(*watchedObject, resource)
	if err != nil {
		if k8serrors.IsForbidden(err) || k8serrors.IsUnauthorized(err) {
			// When the user is forbidden to perform the watch, the original error is just "unknown".
			err = fmt.Errorf(
				"%w: ensure the client has the list and watch permissions for the query: %s", err, watchedObject,
			)
		}

		klog.Errorf("Could not start a watch request for %s, error: %v", watchedObject, err)

		return err
	}

	if d.options.EnableCache {
		d.objectCache.CacheFromObjectIdentifier(*watchedObject, watchedObjects)
	}

	d.watches[*watchedObject] = &watchWithHandshake{watch: w, stopped: make(chan ObjectIdentifier)}
	d.watchedToWatchers[*watchedObject][watcher] = true
	d.watcherToWatches[watcher][*watchedObject] = true

	sendInitialEvent := !d.options.DisableInitialReconcile && len(watchedObjects) != 0

	// Launch a Go routine per watch API request that will feed d.Queue.
	go d.relayWatchEvents(*watchedObject, resource, sendInitialEvent, d.watches[*watchedObject])

	return nil
}

// AddWatcher adds a watch for the watcher.
func (d *dynamicWatcher) AddWatcher(watcher ObjectIdentifier, watchedObject ObjectIdentifier) error {
	if !d.started {
		return ErrNotStarted
	}

	if err := watcher.Validate(); err != nil {
		return err
	}

	if err := watchedObject.Validate(); err != nil {
		return err
	}

	_, loaded := d.queryBatches.Load(watcher)
	if loaded {
		return ErrQueryBatchInProgress
	}

	d.lock.Lock()
	defer d.lock.Unlock()

	return d.addWatcher(watcher, &watchedObject)
}

// watchLatest performs a list with the field selector for the input watched object to get the resource version and then
// starts the watch using the client-go RetryWatcher API. The returned bool indicates that the input watched object
// exists on the cluster.
func watchLatest(
	watchedObject ObjectIdentifier, resource dynamic.ResourceInterface,
) (
	apiWatch.Interface, []unstructured.Unstructured, error,
) {
	timeout := int64(10)
	listOpts := metav1.ListOptions{TimeoutSeconds: &timeout}

	if watchedObject.Name != "" {
		listOpts.FieldSelector = "metadata.name=" + watchedObject.Name
	} else {
		listOpts.LabelSelector = watchedObject.Selector
	}

	listResult, err := resource.List(context.TODO(), listOpts)
	if err != nil {
		return nil, nil, err
	}

	resourceVersion := listResult.GetResourceVersion()

	watchStarted := make(chan error, 1)
	var firstResultSent bool

	watchFunc := func(options metav1.ListOptions) (apiWatch.Interface, error) {
		if watchedObject.Name != "" {
			options.FieldSelector = "metadata.name=" + watchedObject.Name
		} else {
			options.LabelSelector = watchedObject.Selector
		}

		watch, err := resource.Watch(context.Background(), options)

		if !firstResultSent {
			if k8serrors.IsForbidden(err) || k8serrors.IsUnauthorized(err) {
				watchStarted <- err
			} else {
				// Send nil on no errors or recoverable errors
				watchStarted <- nil
			}

			close(watchStarted)

			firstResultSent = true
		}

		return watch, err
	}

	w, err := watch.NewRetryWatcher(resourceVersion, &cache.ListWatch{WatchFunc: watchFunc})
	if err != nil {
		return nil, nil, err
	}

	watchErr := <-watchStarted
	if watchErr != nil {
		// The watcher should already be stopped, but it's safe to call this multiple times in v0.31+.
		w.Stop()

		return nil, nil, watchErr
	}

	return w, listResult.Items, nil
}

// removeWatch will remove a reference to the input watched object. If the references on the watched object become 0,
// the watch API request is stopped. Note that it's expected that the lock is already acquired by the caller.
func (d *dynamicWatcher) removeWatch(watcher ObjectIdentifier, watchedObject ObjectIdentifier) <-chan ObjectIdentifier {
	delete(d.watcherToWatches[watcher], watchedObject)
	delete(d.watchedToWatchers[watchedObject], watcher)

	// Stop the watch API request if the watcher was the only one watching this object.
	if d.watches[watchedObject] != nil && len(d.watchedToWatchers[watchedObject]) == 0 {
		// Tell relayWatchEvents that this is an intentional stop
		d.watches[watchedObject].requestingStop = true
		d.watches[watchedObject].watch.Stop()
		stoppedChan := d.watches[watchedObject].stopped

		delete(d.watchedToWatchers, watchedObject)

		return stoppedChan
	}

	return nil
}

// RemoveWatcher removes a watcher and any of its API watches solely referenced by the watcher.
func (d *dynamicWatcher) RemoveWatcher(watcher ObjectIdentifier) error {
	if !d.started {
		return ErrNotStarted
	}

	if err := watcher.Validate(); err != nil {
		return err
	}

	_, loaded := d.queryBatches.Load(watcher)
	if loaded {
		return ErrQueryBatchInProgress
	}

	d.lock.Lock()

	stoppedWatches := []<-chan ObjectIdentifier{}

	for watchedObject := range d.watcherToWatches[watcher] {
		stoppedWatches = append(stoppedWatches, d.removeWatch(watcher, watchedObject))
	}

	delete(d.watcherToWatches, watcher)

	d.lock.Unlock()

	d.waitForStoppedWatches(stoppedWatches)

	return nil
}

// waitForStoppedWatches will take a slice of channels indicating when a watch has been completely stopped.
// After the watch has stopped, it will cleanup the d.watches map. Note that the lock must be unlocked for this
// method call.
func (d *dynamicWatcher) waitForStoppedWatches(stoppedWatches []<-chan ObjectIdentifier) {
	// Wait for all the watches to stop before returning
	for i := range stoppedWatches {
		if stoppedWatches[i] != nil {
			watchedObject := <-stoppedWatches[i]

			d.lock.Lock()
			delete(d.watches, watchedObject)
			d.lock.Unlock()
		}
	}
}

// GetWatchCount returns the total number of active API watch requests which can be used for metrics.
func (d *dynamicWatcher) GetWatchCount() uint {
	d.lock.RLock()

	count := uint(len(d.watches))

	d.lock.RUnlock()

	return count
}

// StartQueryBatch will start a query batch transaction for the watcher. After a series of Get/List calls, calling
// EndQueryBatch will clean up the non-applicable preexisting watches made from before this query batch.
func (d *dynamicWatcher) StartQueryBatch(watcher ObjectIdentifier) error {
	if !d.started {
		return ErrNotStarted
	}

	if !d.options.EnableCache {
		return ErrCacheDisabled
	}

	if err := watcher.Validate(); err != nil {
		return err
	}

	d.lock.RLock()

	existingWatches := make(map[ObjectIdentifier]bool, len(d.watcherToWatches[watcher]))

	for key := range d.watcherToWatches[watcher] {
		existingWatches[key] = d.watcherToWatches[watcher][key]
	}

	d.lock.RUnlock()

	obj := &queryBatch{
		lock: &sync.RWMutex{}, previouslyWatched: existingWatches, newWatched: &sync.Map{},
	}

	_, loaded := d.queryBatches.LoadOrStore(watcher, obj)

	// If it's already loaded, that means the query batch is already in progress. You cannot have multiple query
	// batches for the same watcher.
	if loaded {
		return fmt.Errorf("%w: %s", ErrQueryBatchInProgress, watcher)
	}

	return nil
}

// fromCache will first query the cache for the watched object(s). If it's not present, a watch is started and the
// method returns the object(s) once cached.
func (d *dynamicWatcher) fromCache(
	watcher ObjectIdentifier, watchedObjID ObjectIdentifier,
) ([]unstructured.Unstructured, error) {
	var batch *queryBatch

	loadedBatch, loaded := d.queryBatches.Load(watcher)
	if !loaded {
		return nil, ErrQueryBatchNotStarted
	}

	// Type assertion checks aren't needed since this is the only type that is stored.
	batch = loadedBatch.(*queryBatch)
	// A read lock is indicating that the batch is still in progress and shouldn't end until the query completes.
	// Because batch.newWatched is concurrency safe, it can be updated with a read lock on the batch.
	batch.lock.RLock()
	defer batch.lock.RUnlock()

	// After the lock is obtained, ensure that the same batch is in progress. This is to account
	// for another goroutine calling EndQueryBatch at the same time.
	if batch.complete {
		return nil, ErrQueryBatchNotStarted
	}

	gvr, err := d.objectCache.GVKToGVR(watchedObjID.GroupVersionKind())
	if err != nil {
		klog.Errorf("Could not get the GVR for %s, error: %v", watchedObjID, err)

		return nil, err
	}

	if !gvr.Namespaced { // ignore namespaces set on cluster-scoped resources
		watchedObjID.Namespace = ""
	}

	batch.newWatched.Store(watchedObjID, nil)

	d.lock.RLock()
	// If the watch already exists for this watcher, just return from the cache.
	if d.watcherToWatches[watcher][watchedObjID] {
		defer d.lock.RUnlock()

		return d.objectCache.FromObjectIdentifier(watchedObjID)
	}

	d.lock.RUnlock()

	// A write lock is needed since this a new watch for this watcher.
	d.lock.Lock()
	// Defer the unlock to prevent some other goroutine clearing the cache after the watch was added.
	defer d.lock.Unlock()

	// addWatcher is idempotent, so if another goroutine held the lock and started the watch for this object,
	// it'll just be a no-op.
	err = d.addWatcher(watcher, &watchedObjID)
	if err != nil {
		return nil, err
	}

	return d.objectCache.FromObjectIdentifier(watchedObjID)
}

// Get will add an additional watch to the started query batch and return the watched object. Note that you must
// call StartQueryBatch before calling this.
func (d *dynamicWatcher) Get(
	watcher ObjectIdentifier, gvk schema.GroupVersionKind, namespace string, name string,
) (*unstructured.Unstructured, error) {
	if !d.started {
		return nil, ErrNotStarted
	}

	if !d.options.EnableCache {
		return nil, ErrCacheDisabled
	}

	watchedObjID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Name:      name,
	}

	result, err := d.fromCache(watcher, watchedObjID)
	if err != nil {
		return nil, err
	}

	if len(result) != 0 {
		return &result[0], nil
	}

	return nil, nil
}

// GetFromCache will return the object from the cache. If it's not cached, the ErrNoCacheEntry error will be returned.
func (d *dynamicWatcher) GetFromCache(
	gvk schema.GroupVersionKind, namespace string, name string,
) (*unstructured.Unstructured, error) {
	if !d.options.EnableCache {
		return nil, ErrCacheDisabled
	}

	return d.objectCache.Get(gvk, namespace, name)
}

// List will add an additional watch to the started query batch and return the watched objects. Note that you must
// call StartQueryBatch before calling this.
func (d *dynamicWatcher) List(
	watcher ObjectIdentifier, gvk schema.GroupVersionKind, namespace string, selector labels.Selector,
) ([]unstructured.Unstructured, error) {
	if !d.started {
		return nil, ErrNotStarted
	}

	if !d.options.EnableCache {
		return nil, ErrCacheDisabled
	}

	if selector == nil {
		selector = labels.NewSelector()
	}

	watchedObjID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Selector:  selector.String(),
	}

	return d.fromCache(watcher, watchedObjID)
}

// ListFromCache will return the objects from the cache. If it's not cached, the ErrNoCacheEntry error will be
// returned.
func (d *dynamicWatcher) ListFromCache(
	gvk schema.GroupVersionKind, namespace string, selector labels.Selector,
) ([]unstructured.Unstructured, error) {
	if !d.options.EnableCache {
		return nil, ErrCacheDisabled
	}

	return d.objectCache.List(gvk, namespace, selector)
}

// ListWatchedFromCache will return all watched objects by the watcher in the cache.
func (d *dynamicWatcher) ListWatchedFromCache(watcher ObjectIdentifier) ([]unstructured.Unstructured, error) {
	if !d.options.EnableCache {
		return nil, ErrCacheDisabled
	}

	d.lock.RLock()
	defer d.lock.RUnlock()

	rv := make([]unstructured.Unstructured, 0, len(d.watcherToWatches[watcher]))

	for watched := range d.watcherToWatches[watcher] {
		cachedList, err := d.objectCache.FromObjectIdentifier(watched)
		if err != nil {
			if errors.Is(err, ErrNoCacheEntry) {
				continue
			}

			return nil, err
		}

		rv = append(rv, cachedList...)
	}

	return rv, nil
}

// EndQueryBatch will stop a query batch transaction for the watcher. This will clean up the non-applicable
// preexisting watches made from before this query batch.
func (d *dynamicWatcher) EndQueryBatch(watcher ObjectIdentifier) error {
	if !d.started {
		return ErrNotStarted
	}

	if !d.options.EnableCache {
		return ErrCacheDisabled
	}

	if err := watcher.Validate(); err != nil {
		return err
	}

	loadedObj, loaded := d.queryBatches.Load(watcher)

	if !loaded {
		return ErrQueryBatchNotStarted
	}

	batch := loadedObj.(*queryBatch)
	batch.lock.Lock()

	defer batch.lock.Unlock()

	if len(batch.previouslyWatched) != 0 {
		// Only lock if previouslyWatched has any values
		d.lock.Lock()
		stoppedWatches := []<-chan ObjectIdentifier{}

		for watchedObject := range batch.previouslyWatched {
			if _, watched := batch.newWatched.Load(watchedObject); !watched {
				stoppedWatches = append(stoppedWatches, d.removeWatch(watcher, watchedObject))
			}
		}

		d.lock.Unlock()

		d.waitForStoppedWatches(stoppedWatches)
	}

	batch.complete = true

	d.queryBatches.Delete(watcher)

	return nil
}

// GVKToGVR will convert a GVK to a GVR and cache the result for a default of 10 minutes (configurable) when found, and
// not cache failed conversions by default (configurable).
func (d *dynamicWatcher) GVKToGVR(gvk schema.GroupVersionKind) (ScopedGVR, error) {
	return d.objectCache.GVKToGVR(gvk)
}
