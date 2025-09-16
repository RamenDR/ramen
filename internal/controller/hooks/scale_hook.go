package hooks

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	client "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ScaleUp                 = "up"
	ScaleDown               = "down"
	ScaleSync               = "sync"
	replicasCountAnnotation = "ramendr.io/scale-hook-replicas-count"
)

type ScaleResource interface {
	GetReplicasFromSpec() *int32
	SetReplicas(*int32)
	GetReplicasFromStatus() *int32
	GetAnnotations() map[string]string
	SetAnnotations(map[string]string)
	GetObjectMeta() metav1.Object
	Update(ctx context.Context, writer client.Writer) error
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
	return d.Deployment
}

func (d DeploymentResource) Update(ctx context.Context, w client.Writer) error {
	return w.Update(ctx, d.Deployment)
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
	return s.StatefulSet
}

func (s StatefulSetResource) Update(ctx context.Context, w client.Writer) error {
	return w.Update(ctx, s.StatefulSet)
}

type ScaleHook struct {
	Hook   *kubeobjects.HookSpec
	Reader client.Reader
	Client client.Client
}

func (s ScaleHook) Execute(log logr.Logger) error {
	log.Info("Inside scale hook execute")
	log.Info("Hook details", "hook", s.Hook)

	objList, err := getResourceListForType(s.Hook.SelectResource)
	if err != nil {
		return err
	}

	resources, err := getResourcesBySelector(s.Client, s.Hook, objList)
	if err != nil {
		return err
	}

	if len(resources) == 0 {
		return fmt.Errorf("no resources found to scale")
	}

	scaleOp := s.Hook.Scale.Operation
	log.Info("ScaleHook operation", "operation", scaleOp)

	for _, obj := range resources {
		switch res := obj.(type) {
		case *appsv1.Deployment:
			deployment := DeploymentResource{res}
			if err := s.scaleResource(deployment, scaleOp, log); err != nil {
				return err
			}
		case *appsv1.StatefulSet:
			statefulset := StatefulSetResource{res}
			if err := s.scaleResource(statefulset, scaleOp, log); err != nil {
				return err
			}
		default:
			log.Info("Unsupported resource type for scaling", "type", reflect.TypeOf(obj))
		}
	}

	return nil
}

func (s ScaleHook) scaleResource(resource ScaleResource, operation string, log logr.Logger) error {
	switch operation {
	case ScaleDown:
		return s.scaleDownResource(resource, log)
	case ScaleUp:
		return s.scaleUpResource(resource, log)
	case ScaleSync:
		return s.syncResource(resource, log)
	default:
		return fmt.Errorf("unsupported scale operation: %s", operation)
	}
}

func (s ScaleHook) scaleDownResource(resource ScaleResource, log logr.Logger) error {
	if resource.GetReplicasFromSpec() == nil || *resource.GetReplicasFromSpec() == 0 {
		log.Info("Already scaled down", "resource", resource.GetObjectMeta().GetName())

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

	log.Info("Scaling down with annotation", "resource", resource.GetObjectMeta().GetName(),
		"replicas count", replicasCount)

	return resource.Update(context.Background(), s.Client)
}

func (s ScaleHook) scaleUpResource(resource ScaleResource, log logr.Logger) error {
	annotations := resource.GetAnnotations()
	if annotations == nil {
		return fmt.Errorf("no annotations found to restore replicas for resource %s", resource.GetObjectMeta().GetName())
	}

	origStr, ok := annotations[replicasCountAnnotation]
	if !ok {
		return fmt.Errorf("original replicas annotation not found for resource %s", resource.GetObjectMeta().GetName())
	}

	replicaCount, err := strconv.ParseInt(origStr, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid original replicas annotation value %s on resource %s in %s: %w",
			origStr, resource.GetObjectMeta().GetName(), resource.GetObjectMeta().GetNamespace(), err)
	}

	if replicaCount < 0 || replicaCount > math.MaxInt32 {
		return fmt.Errorf("original replicas annotation value %d out of int32 range on resource %s in %s",
			replicaCount, resource.GetObjectMeta().GetName(), resource.GetObjectMeta().GetNamespace())
	}

	replicaCount32 := int32(replicaCount)
	resource.SetReplicas(&replicaCount32)

	log.Info("Scaling up from annotation", "resource", resource.GetObjectMeta().GetName(), "replicas", replicaCount32)

	delete(annotations, replicasCountAnnotation)
	resource.SetAnnotations(annotations)

	return resource.Update(context.Background(), s.Client)
}

// SyncResource waits until the resource reflects its target replica count
func (s ScaleHook) syncResource(resource ScaleResource, log logr.Logger) error {
	const (
		timeout      = 300 // seconds total timeout
		pollInterval = 5   // seconds between polls
	)

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
			refreshed, err := refreshResource(s.Reader, resource, namespace, name)
			if err != nil {
				log.Info("Error refreshing resource during sync", "resource", name, "error", err)

				return err
			}

			resource = refreshed

			actualReplicas := *resource.GetReplicasFromStatus()

			if actualReplicas == targetReplicas {
				log.Info("Sync: target replica count reached", "resource", name, "replicas", targetReplicas)

				return nil
			}

			log.Info("Sync: waiting for target replicas",
				"resource", name,
				"actualReplicas", actualReplicas,
				"targetReplicas", targetReplicas,
			)
		}
	}
}

func getResourcesBySelector(
	r client.Reader,
	hook *kubeobjects.HookSpec,
	objList client.ObjectList,
) ([]client.Object, error) {
	var result []client.Object

	if hook.NameSelector != "" {
		nsType, objs, err := getResourcesUsingNameSelector(r, hook, objList)
		if err != nil {
			return nil, fmt.Errorf("error during nameSelector resource lookup: %w", err)
		}

		if nsType == InvalidNameSelector {
			return nil, fmt.Errorf("invalid nameSelector: %s", hook.NameSelector)
		}

		result = append(result, objs...)
	}

	if hook.LabelSelector != nil {
		if err := getResourcesUsingLabelSelector(r, hook, objList); err != nil {
			return nil, fmt.Errorf("error during labelSelector resource lookup: %w", err)
		}

		result = append(result, getObjectsBasedOnType(objList)...)
	}

	return result, nil
}

func getResourceListForType(kind string) (client.ObjectList, error) {
	switch kind {
	case deploymentType:
		return &appsv1.DeploymentList{}, nil
	case statefulsetType:
		return &appsv1.StatefulSetList{}, nil
	default:
		return nil, fmt.Errorf("unsupported resource type for scale hook: %s", kind)
	}
}

func refreshResource(reader client.Reader, resource ScaleResource, namespace, name string) (ScaleResource, error) {
	switch resource.(type) {
	case DeploymentResource:
		deployment := &appsv1.Deployment{}

		err := reader.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, deployment)
		if err != nil {
			return nil, err
		}

		return DeploymentResource{deployment}, nil
	case StatefulSetResource:
		statefulset := &appsv1.StatefulSet{}

		err := reader.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, statefulset)
		if err != nil {
			return nil, err
		}

		return StatefulSetResource{statefulset}, nil
	default:
		return nil, fmt.Errorf("unsupported resource type")
	}
}
