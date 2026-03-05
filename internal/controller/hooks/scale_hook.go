package hooks

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
)

const (
	ScaleUp                 = "up"
	ScaleDown               = "down"
	ScaleSync               = "sync"
	replicasCountAnnotation = "ramendr.io/scale-hook-replicas-count"
	pollInterval            = 5
)

type Resource interface {
	GetReplicasFromSpec() *int32
	SetReplicas(*int32)
	GetReplicasFromStatus() *int32
	GetAnnotations() map[string]string
	SetAnnotations(map[string]string)
	GetObjectMeta() metav1.Object
	Update(ctx context.Context, client client.Client) error
}

type DeploymentResource struct {
	*appsv1.Deployment
}

func (d DeploymentResource) GetReplicasFromSpec() *int32 {
	return d.Deployment.Spec.Replicas
}

func (d DeploymentResource) SetReplicas(replicas *int32) {
	d.Deployment.Spec.Replicas = replicas
}

func (d DeploymentResource) GetReplicasFromStatus() *int32 {
	return &d.Deployment.Status.ReadyReplicas
}

func (d DeploymentResource) GetAnnotations() map[string]string {
	return d.Deployment.Annotations
}

func (d DeploymentResource) SetAnnotations(annotation map[string]string) {
	d.Deployment.Annotations = annotation
}

func (d DeploymentResource) GetObjectMeta() metav1.Object {
	return &d.Deployment.ObjectMeta
}

func (d DeploymentResource) Update(ctx context.Context, c client.Client) error {
	return c.Update(ctx, d.Deployment)
}

type StatefulSetResource struct {
	*appsv1.StatefulSet
}

func (s StatefulSetResource) GetReplicasFromSpec() *int32 {
	return s.StatefulSet.Spec.Replicas
}

func (s StatefulSetResource) SetReplicas(replicas *int32) {
	s.StatefulSet.Spec.Replicas = replicas
}

func (s StatefulSetResource) GetReplicasFromStatus() *int32 {
	return &s.StatefulSet.Status.ReadyReplicas
}

func (s StatefulSetResource) GetAnnotations() map[string]string {
	return s.StatefulSet.Annotations
}

func (s StatefulSetResource) SetAnnotations(annotation map[string]string) {
	s.StatefulSet.Annotations = annotation
}

func (s StatefulSetResource) GetObjectMeta() metav1.Object {
	return &s.StatefulSet.ObjectMeta
}

func (s StatefulSetResource) Update(ctx context.Context, c client.Client) error {
	return c.Update(ctx, s.StatefulSet)
}

type ScaleHook struct {
	Hook   *kubeobjects.HookSpec
	Reader client.Reader
	Client client.Client
}

func (s ScaleHook) Execute(log logr.Logger) error {
	log.Info("Executing scale hook operation",
		"hook", s.Hook.Name,
		"namespace", s.Hook.Namespace,
		"operation", s.Hook.Scale.Operation,
		"selectResource", s.Hook.SelectResource,
		"nameSelector", s.Hook.NameSelector,
		"labelSelector", s.Hook.LabelSelector,
	)

	resources, err := s.getResourcesToScale()
	if err != nil {
		return err
	}

	scaleOp := s.Hook.Scale.Operation

	return s.processResources(resources, scaleOp, log)
}

func (s ScaleHook) processResources(resources []client.Object, scaleOp string, log logr.Logger) error {
	var lastErr error

	for _, obj := range resources {
		var err error

		switch res := obj.(type) {
		case *appsv1.Deployment:
			err = s.scaleResource(DeploymentResource{res}, scaleOp, log)
		case *appsv1.StatefulSet:
			err = s.scaleResource(StatefulSetResource{res}, scaleOp, log)
		default:
			log.Info("Unsupported resource type for scaling",
				"hook", s.Hook.Name,
				"namespace", s.Hook.Namespace,
				"operation", scaleOp,
				"type", reflect.TypeOf(obj),
			)

			continue
		}

		if err != nil {
			if s.Hook.OnError == "continue" {
				lastErr = err

				continue
			}

			return err
		}
	}

	if lastErr != nil && s.Hook.OnError == "continue" {
		return lastErr
	}

	return nil
}

func (s ScaleHook) getResourcesToScale() ([]client.Object, error) {
	objList, err := s.getResourceListForType()
	if err != nil {
		return nil, err
	}

	resources, err := s.getResourcesBySelector(objList)
	if err != nil {
		return nil, err
	}

	if len(resources) == 0 {
		return nil, fmt.Errorf(
			"no resources found to scale: hook=%s, namespace=%s, selectResource=%s",
			s.Hook.Name, s.Hook.Namespace, s.Hook.SelectResource)
	}

	return resources, nil
}

func (s ScaleHook) scaleResource(resource Resource, operation string, log logr.Logger) error {
	switch operation {
	case ScaleDown:
		return s.ScaleDownResource(resource, log)
	case ScaleUp:
		return s.ScaleUpResource(resource, log)
	case ScaleSync:
		return s.SyncResource(resource, log)
	default:
		return fmt.Errorf("unsupported scale operation: hook=%s, namespace=%s, operation=%s, resource=%s",
			s.Hook.Name,
			s.Hook.Namespace,
			s.Hook.Scale.Operation,
			resource.GetObjectMeta().GetName(),
		)
	}
}

func (s ScaleHook) ScaleDownResource(resource Resource, log logr.Logger) error {
	if resource.GetReplicasFromSpec() == nil || *resource.GetReplicasFromSpec() == 0 {
		log.Info("Already scaled down",
			"hook", s.Hook.Name,
			"namespace", s.Hook.Namespace,
			"operation", s.Hook.Scale.Operation,
			"resource", resource.GetObjectMeta().GetName(),
		)

		return nil
	}

	replicasCount := *resource.GetReplicasFromSpec()

	annotations := resource.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[replicasCountAnnotation] = fmt.Sprintf("%d", replicasCount)
	resource.SetAnnotations(annotations)

	zero := int32(0)
	resource.SetReplicas(&zero)

	log.Info("Scaling down with annotation",
		"hook", s.Hook.Name,
		"namespace", s.Hook.Namespace,
		"operation", s.Hook.Scale.Operation,
		"resource", resource.GetObjectMeta().GetName(),
		"replicasCount", replicasCount,
	)

	if err := resource.Update(context.Background(), s.Client); err != nil {
		log.Error(err, "Failed to update resource during scale down",
			"hook", s.Hook.Name,
			"namespace", s.Hook.Namespace,
			"operation", s.Hook.Scale.Operation,
			"resource", resource.GetObjectMeta().GetName(),
		)

		return err
	}

	return nil
}

func (s ScaleHook) ScaleUpResource(resource Resource, log logr.Logger) error {
	resourceName := resource.GetObjectMeta().GetName()
	resourceNamespace := resource.GetObjectMeta().GetNamespace()

	annotations := resource.GetAnnotations()
	if annotations == nil {
		return fmt.Errorf("no annotations found to restore replicas for resource %s/%s hook: %s",
			resourceNamespace, resourceName, s.Hook.Name)
	}

	origStr, ok := annotations[replicasCountAnnotation]
	if !ok {
		return fmt.Errorf("original replicas annotation not found for resource %s/%s hook: %s",
			resourceNamespace, resourceName, s.Hook.Name)
	}

	replicaCount, err := strconv.ParseInt(origStr, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid original replicas annotation value %s on resource %s/%s hook: %s: %w",
			origStr, resourceNamespace, resourceName, s.Hook.Name, err)
	}

	if replicaCount < 0 || replicaCount > math.MaxInt32 {
		return fmt.Errorf("original replicas annotation value %d out of int32 range on resource %s/%s hook: %s",
			replicaCount, resourceNamespace, resourceName, s.Hook.Name)
	}

	replicaCount32 := int32(replicaCount)
	resource.SetReplicas(&replicaCount32)

	log.Info("Scaling up from annotation",
		"hook", s.Hook.Name,
		"namespace", resourceNamespace,
		"operation", s.Hook.Scale.Operation,
		"resource", resourceName,
		"replicas", replicaCount32,
	)

	delete(annotations, replicasCountAnnotation)
	resource.SetAnnotations(annotations)

	if err := resource.Update(context.Background(), s.Client); err != nil {
		log.Error(err, "Failed to update resource during scale up",
			"hook", s.Hook.Name,
			"namespace", resourceNamespace,
			"operation", s.Hook.Scale.Operation,
			"resource", resourceName,
		)

		return err
	}

	return nil
}

// SyncResource waits until the resource reflects its target replica count
func (s ScaleHook) SyncResource(resource Resource, log logr.Logger) error {
	timeout := getHookTimeoutValue(s.Hook)

	name := resource.GetObjectMeta().GetName()
	namespace := resource.GetObjectMeta().GetNamespace()

	targetPtr := resource.GetReplicasFromSpec()
	if targetPtr == nil {
		return fmt.Errorf("sync: .Spec.Replicas is nil for resource %s", name)
	}

	targetReplicas := *targetPtr

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	ticker := time.NewTicker(time.Duration(pollInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("sync timeout: resource %s replicas did not reach %d within %d seconds",
				name, targetReplicas, timeout)

		case <-ticker.C:
			refreshed, err := s.refreshResource(s.Reader, resource)
			if err != nil {
				log.Info("Error refreshing resource during sync",
					"hook", s.Hook.Name,
					"namespace", s.Hook.Namespace,
					"resource", name,
					"error", err)

				return err
			}

			resource = refreshed

			actualReplicas := *resource.GetReplicasFromStatus()

			if actualReplicas == targetReplicas {
				log.Info("Sync: target replica count reached",
					"hook", s.Hook.Name,
					"namespace", s.Hook.Namespace,
					"resource", name,
					"replicas", targetReplicas)

				return nil
			}

			log.Info("Sync: waiting for target replicas",
				"hook", s.Hook.Name,
				"namespace", namespace,
				"resource", name,
				"actualReplicas", actualReplicas,
				"targetReplicas", targetReplicas,
			)
		}
	}
}

func (s ScaleHook) getResourcesBySelector(objList client.ObjectList) ([]client.Object, error) {
	var result []client.Object

	hook := s.Hook
	r := s.Reader

	if hook.NameSelector != "" {
		nsType, objs, err := getResourcesUsingNameSelector(r, hook, objList)
		if err != nil {
			return nil, fmt.Errorf(
				"error during nameSelector resource lookup: %w, hook=%s, namespace=%s, operation=%s, "+
					"selectResource=%s, nameSelector=%s",
				err, hook.Name, hook.Namespace, hook.Scale.Operation, hook.SelectResource, hook.NameSelector,
			)
		}

		if nsType == InvalidNameSelector {
			return nil, fmt.Errorf(
				"invalid nameSelector: %s , hook=%s, namespace=%s, operation=%s, selectResource=%s",
				hook.NameSelector, hook.Name, hook.Namespace, hook.Scale.Operation, hook.SelectResource,
			)
		}

		result = append(result, objs...)
	}

	if hook.LabelSelector != nil {
		if err := getResourcesUsingLabelSelector(r, hook, objList); err != nil {
			return nil, fmt.Errorf(
				"error during labelSelector resource lookup: %w, hook=%s, namespace=%s, operation=%s, "+
					"selectResource=%s, labelSelector=%v",
				err, hook.Name, hook.Namespace, hook.Scale.Operation, hook.SelectResource, hook.LabelSelector,
			)
		}

		result = append(result, getObjectsBasedOnType(objList)...)
	}

	return result, nil
}

func (s ScaleHook) getResourceListForType() (client.ObjectList, error) {
	switch s.Hook.SelectResource {
	case deploymentType:
		return &appsv1.DeploymentList{}, nil
	case statefulsetType:
		return &appsv1.StatefulSetList{}, nil
	default:
		return nil, fmt.Errorf(
			"Unsupported resource type for scale hook: hook=%s, namespace=%s, operation=%s, selectResource=%s",
			s.Hook.Name,
			s.Hook.Namespace,
			s.Hook.Scale.Operation,
			s.Hook.SelectResource,
		)
	}
}

func (s ScaleHook) refreshResource(reader client.Reader, resource Resource) (Resource, error) {
	namespace := resource.GetObjectMeta().GetNamespace()
	name := resource.GetObjectMeta().GetName()

	switch resource.(type) {
	case DeploymentResource:
		deployment := &appsv1.Deployment{}

		err := reader.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, deployment)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get Deployment resource %s/%s for hook %s: %w",
				namespace, name, s.Hook.Name, err)
		}

		return DeploymentResource{deployment}, nil

	case StatefulSetResource:
		statefulset := &appsv1.StatefulSet{}

		err := reader.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, statefulset)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get StatefulSet resource %s/%s for hook %s: %w",
				namespace, name, s.Hook.Name, err)
		}

		return StatefulSetResource{statefulset}, nil

	default:
		return nil, fmt.Errorf("unsupported resource type for hook %s when fetching resource %s/%s",
			s.Hook.Name, namespace, name)
	}
}

func getHookTimeoutValue(hook *kubeobjects.HookSpec) int {
	if hook.Timeout != 0 {
		return hook.Timeout
	}
	// 300s is the default value for timeout
	return defaultTimeoutValue
}
