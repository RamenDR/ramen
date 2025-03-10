// Copyright Contributors to the Open Cluster Management project
package client

import (
	"errors"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	klog "k8s.io/klog/v2"
)

var ErrNoCacheEntry = errors.New("there was no populated cache entry")

// ObjectCache provides a concurrency safe cache of unstructured objects. Additionally, it's able to convert GVKs to
// GVRs and cache the results.
type ObjectCache interface { //nolint: interfacebloat
	// Get returns the object from the cache. A nil value can be returned to indicate a not found is cached. The error
	// ErrNoCacheEntry is returned if there is no cache entry at all.
	Get(gvk schema.GroupVersionKind, namespace string, name string) (*unstructured.Unstructured, error)
	// List returns the objects from the cache, which can be an empty list. The error ErrNoCacheEntry is returned if
	// there is no cache entry.
	List(gvk schema.GroupVersionKind, namespace string, selector labels.Selector) ([]unstructured.Unstructured, error)
	// FromObjectIdentifier returns the objects from the cache, which can be an empty list, based on the input object
	// identifier. The error ErrNoCacheEntry is returned if there is no cache entry at all.
	FromObjectIdentifier(objID ObjectIdentifier) ([]unstructured.Unstructured, error)
	// CacheList will cache a list of objects for the list query.
	CacheList(
		gvk schema.GroupVersionKind, namespace string, selector labels.Selector, objects []unstructured.Unstructured,
	)
	// CacheObject allows to cache an object. The input object can be nil to indicate a cached not found result.
	CacheObject(gvk schema.GroupVersionKind, namespace string, name string, object *unstructured.Unstructured)
	// CacheFromObjectIdentifier will cache a list of objects for the input object identifier.
	CacheFromObjectIdentifier(objID ObjectIdentifier, objects []unstructured.Unstructured)
	// UncacheObject will entirely remove the cache entry of the object.
	UncacheObject(gvk schema.GroupVersionKind, namespace string, name string)
	// UncacheObject will entirely remove the cache entries for the list query.
	UncacheList(gvk schema.GroupVersionKind, namespace string, selector labels.Selector)
	// UncacheObject will entirely remove the cache entries for the object identifier.
	UncacheFromObjectIdentifier(objID ObjectIdentifier)
	// GVKToGVR will convert a GVK to a GVR and cache the result for a default of 10 minutes (configurable) when found,
	// and not cache failed conversions by default (configurable).
	GVKToGVR(gvk schema.GroupVersionKind) (ScopedGVR, error)
	// Clear will entirely clear the cache.
	Clear()
}

type lockedGVR struct {
	lock    *sync.RWMutex
	gvr     *ScopedGVR
	expires time.Time
}

func (l *lockedGVR) isExpired() bool {
	return time.Now().After(l.expires)
}

type objectCache struct {
	cache           *sync.Map
	discoveryClient discovery.DiscoveryInterface
	// gvkToGVRCache is used as a cache of GVK to GVR mappings. The cache entries automatically expire after 10 minutes.
	gvkToGVRCache *sync.Map
	options       ObjectCacheOptions
}

type ObjectCacheOptions struct {
	// The time for a GVK to GVR conversion cache entry to be considered fresh (not expired). This excludes missing API
	// resources, which is configured by MissingAPIResourceCacheTTL. The default value is 10 minutes.
	GVKToGVRCacheTTL time.Duration
	// The time for a failed GVK to GVR conversion to not be retried. The default behavior is to not cache failures.
	// Setting this can be useful if you don't want to continuously query the Kubernetes API if a CRD is missing.
	MissingAPIResourceCacheTTL time.Duration
	// Whether to *skip* the DeepCopy when retrieving an object from the cache.
	// Be careful when disabling deep copy: it makes it possible to mutate objects inside the cache.
	UnsafeDisableDeepCopy bool
}

// NewObjectCache will create an object cache with the input discovery client.
func NewObjectCache(discoveryClient discovery.DiscoveryInterface, options ObjectCacheOptions) ObjectCache {
	if options.GVKToGVRCacheTTL == 0 {
		options.GVKToGVRCacheTTL = 10 * time.Minute
	}

	return &objectCache{
		cache:           &sync.Map{},
		gvkToGVRCache:   &sync.Map{},
		discoveryClient: discoveryClient,
		options:         options,
	}
}

// Get returns the object from the cache. A nil value can be returned to indicate a not found is cached. The error
// ErrNoCacheEntry is returned if there is no cache entry at all.
func (o *objectCache) Get(
	gvk schema.GroupVersionKind, namespace string, name string,
) (*unstructured.Unstructured, error) {
	objID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Name:      name,
	}

	result, err := o.FromObjectIdentifier(objID)
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return nil, nil
	}

	return &result[0], nil
}

// List returns the objects from the cache, which can be an empty list. The error ErrNoCacheEntry is returned if
// there is no cache entry.
func (o *objectCache) List(
	gvk schema.GroupVersionKind, namespace string, selector labels.Selector,
) ([]unstructured.Unstructured, error) {
	if selector == nil {
		selector = labels.NewSelector()
	}

	objID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Selector:  selector.String(),
	}

	return o.FromObjectIdentifier(objID)
}

// CacheList will cache a list of objects for the list query.
func (o *objectCache) CacheList(
	gvk schema.GroupVersionKind, namespace string, selector labels.Selector, objects []unstructured.Unstructured,
) {
	if selector == nil {
		selector = labels.NewSelector()
	}

	objID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Selector:  selector.String(),
	}

	o.CacheFromObjectIdentifier(objID, objects)
}

// FromObjectIdentifier returns the objects from the cache, which can be an empty list, based on the input object
// identifier. The error ErrNoCacheEntry is returned if there is no cache entry at all.
func (o *objectCache) FromObjectIdentifier(objID ObjectIdentifier) ([]unstructured.Unstructured, error) {
	loadedResult, loaded := o.cache.Load(objID)
	if !loaded {
		return nil, fmt.Errorf("%w for %s", ErrNoCacheEntry, objID)
	}

	// Type assertion checks aren't needed since this is the only method that stores data.
	uncopiedResult := loadedResult.([]unstructured.Unstructured)

	if o.options.UnsafeDisableDeepCopy {
		return uncopiedResult, nil
	}

	result := make([]unstructured.Unstructured, 0, len(uncopiedResult))
	for _, obj := range uncopiedResult {
		result = append(result, *obj.DeepCopy())
	}

	return result, nil
}

// CacheObject allows to cache an object. The input object can be nil to indicate a cached not found result.
func (o *objectCache) CacheObject(
	gvk schema.GroupVersionKind, namespace string, name string, object *unstructured.Unstructured,
) {
	objID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Name:      name,
	}

	o.CacheFromObjectIdentifier(objID, []unstructured.Unstructured{*object})
}

// CacheFromObjectIdentifier will cache a list of objects for the input object identifier. The metadata.managedFields
// and metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"] values are automatically removed to
// save on memory.
func (o *objectCache) CacheFromObjectIdentifier(objID ObjectIdentifier, objects []unstructured.Unstructured) {
	for i := range objects {
		unstructured.RemoveNestedField(objects[i].Object, "metadata", "managedFields")
		unstructured.RemoveNestedField(
			objects[i].Object, "metadata", "annotations", "kubectl.kubernetes.io/last-applied-configuration",
		)
	}

	o.cache.Store(objID, objects)
}

// UncacheObject will entirely remove the cache entry of the object.
func (o *objectCache) UncacheObject(gvk schema.GroupVersionKind, namespace string, name string) {
	objID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Name:      name,
	}

	o.UncacheFromObjectIdentifier(objID)
}

// UncacheObject will entirely remove the cache entries for the list query.
func (o *objectCache) UncacheList(gvk schema.GroupVersionKind, namespace string, selector labels.Selector) {
	if selector == nil {
		selector = labels.NewSelector()
	}

	objID := ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: namespace,
		Selector:  selector.String(),
	}

	o.UncacheFromObjectIdentifier(objID)
}

// UncacheObject will entirely remove the cache entries for the object identifier.
func (o *objectCache) UncacheFromObjectIdentifier(objID ObjectIdentifier) {
	o.cache.Delete(objID)
}

// GVKToGVR will convert a GVK to a GVR and cache the result for a default of 10 minutes (configurable) when found, and
// not cache failed conversions by default (configurable).
func (o *objectCache) GVKToGVR(gvk schema.GroupVersionKind) (ScopedGVR, error) {
	now := time.Now()

	lockedGVRObj := &lockedGVR{lock: &sync.RWMutex{}, expires: now.Add(o.options.GVKToGVRCacheTTL)}
	lockedGVRObj.lock.Lock()

	loadedLockedGVRObj, loaded := o.gvkToGVRCache.LoadOrStore(gvk, lockedGVRObj)
	if loaded {
		// If the value was loaded (not stored), there means there is a cached value or it is being populated.
		lockedGVRObj = loadedLockedGVRObj.(*lockedGVR)
		lockedGVRObj.lock.RLock()

		if !lockedGVRObj.isExpired() {
			lockedGVRObj.lock.RUnlock()
			// If stored but nil, this means the previous call failed.
			if lockedGVRObj.gvr == nil {
				return ScopedGVR{}, ErrNoVersionedResource
			}

			return *lockedGVRObj.gvr, nil
		}

		// The cache is expired, get a write lock to update the entry.
		lockedGVRObj.lock.RUnlock()
		lockedGVRObj.lock.Lock()
	}

	defer lockedGVRObj.lock.Unlock()

	groupVersion := gvk.GroupVersion().String()

	resources, err := o.discoveryClient.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("The group version was not found: %s", groupVersion)

			lockedGVRObj.expires = now.Add(o.options.MissingAPIResourceCacheTTL)

			return ScopedGVR{}, ErrNoVersionedResource
		}

		return ScopedGVR{}, err
	}

	for _, apiRes := range resources.APIResources {
		if apiRes.Kind == gvk.Kind {
			klog.V(2).Infof("Found the API resource: %v", apiRes)

			gv := gvk.GroupVersion()

			gvr := ScopedGVR{
				GroupVersionResource: schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: apiRes.Name,
				},
				Namespaced: apiRes.Namespaced,
			}

			lockedGVRObj.gvr = &gvr

			return gvr, nil
		}
	}

	klog.V(2).Infof("The APIResource for the GVK wasn't found: %v", gvk)

	lockedGVRObj.expires = now.Add(o.options.MissingAPIResourceCacheTTL)

	return ScopedGVR{}, ErrNoVersionedResource
}

// Clear will entirely clear the cache.
func (o *objectCache) Clear() {
	o.cache.Range(func(key, value any) bool {
		o.cache.Delete(key)

		return true
	})

	o.gvkToGVRCache.Range(func(key, value any) bool {
		o.gvkToGVRCache.Delete(key)

		return true
	})
}
