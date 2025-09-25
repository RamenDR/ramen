package hooks_test

import (
	"context"
	"testing"

	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func setupFakeClientScaleHook(t *testing.T) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	err := appsv1.AddToScheme(scheme)
	assert.NoError(t, err)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&appsv1.Deployment{}, "metadata.name", func(obj client.Object) []string {
			return []string{obj.(*appsv1.Deployment).Name} //nolint:errcheck
		}).Build()
	assert.NotNil(t, fakeClient)

	return fakeClient
}

func newDeployment(name string, replicas int32, annotations map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "test-ns",
			Annotations: annotations,
		},
		Spec:   appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{ReadyReplicas: replicas},
	}
}

func newStatefulSet(name string, replicas int32, annotations map[string]string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "test-ns",
			Annotations: annotations,
		},
		Spec:   appsv1.StatefulSetSpec{Replicas: &replicas},
		Status: appsv1.StatefulSetStatus{ReadyReplicas: replicas},
	}
}

func TestScaleDownDeployment(t *testing.T) {
	fakeClient := setupFakeClientScaleHook(t)
	deployment := newDeployment("scale-deployment", 3, nil)
	err := fakeClient.Create(context.Background(), deployment)
	assert.NoError(t, err)

	resource := hooks.DeploymentResource{deployment}
	scaleHook := hooks.ScaleHook{Client: fakeClient}

	log := zap.New(zap.UseDevMode(true))
	err = scaleHook.ScaleDownResource(resource, log)
	assert.NoError(t, err)

	got := &appsv1.Deployment{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: "scale-deployment"}, got)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), *got.Spec.Replicas)
	assert.Equal(t, "3", got.Annotations["ramendr.io/scale-hook-replicas-count"])
}

func TestScaleDownStatefulSet(t *testing.T) {
	fakeClient := setupFakeClientScaleHook(t)
	statefulset := newStatefulSet("scale-statefulset", 3, nil)
	err := fakeClient.Create(context.Background(), statefulset)
	assert.NoError(t, err)

	resource := hooks.StatefulSetResource{statefulset}
	scaleHook := hooks.ScaleHook{Client: fakeClient}

	log := zap.New(zap.UseDevMode(true))
	err = scaleHook.ScaleDownResource(resource, log)
	assert.NoError(t, err)

	got := &appsv1.StatefulSet{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: "scale-statefulset"}, got)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), *got.Spec.Replicas)
	assert.Equal(t, "3", got.Annotations["ramendr.io/scale-hook-replicas-count"])
}

func TestScaleUpDeploymentMissingAnnotation(t *testing.T) {
	fakeClient := setupFakeClientScaleHook(t)
	deployment := newDeployment("scale-deployment", 0, nil)
	err := fakeClient.Create(context.Background(), deployment)
	assert.NoError(t, err)

	resource := hooks.DeploymentResource{deployment}
	scaleHook := hooks.ScaleHook{Client: fakeClient}
	log := zap.New(zap.UseDevMode(true))

	err = scaleHook.ScaleUpResource(resource, log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no annotations found to restore replicas for resource")
}

func TestScaleUpStatefulSetMissingAnnotation(t *testing.T) {
	fakeClient := setupFakeClientScaleHook(t)
	statefulset := newStatefulSet("scale-statefulset", 3, nil)
	err := fakeClient.Create(context.Background(), statefulset)
	assert.NoError(t, err)

	resource := hooks.StatefulSetResource{statefulset}
	scaleHook := hooks.ScaleHook{Client: fakeClient}
	log := zap.New(zap.UseDevMode(true))

	err = scaleHook.ScaleUpResource(resource, log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no annotations found to restore replicas for resource")
}

func TestScaleUpDeployment(t *testing.T) {
	ann := map[string]string{"ramendr.io/scale-hook-replicas-count": "5"}
	deployment := newDeployment("scale-deployment", 0, ann)
	resource := hooks.DeploymentResource{deployment}
	objKey := client.ObjectKey{Name: "scale-deployment", Namespace: "test-ns"}

	runScaleUpResourceTest(t, resource, objKey)
}

func TestScaleUpStatefulSet(t *testing.T) {
	ann := map[string]string{"ramendr.io/scale-hook-replicas-count": "5"}
	statefulset := newStatefulSet("scale-statefulset", 0, ann)
	resource := hooks.StatefulSetResource{statefulset}
	objKey := client.ObjectKey{Name: "scale-statefulset", Namespace: "test-ns"}

	runScaleUpResourceTest(t, resource, objKey)
}

func runScaleUpResourceTest(t *testing.T, resource hooks.ScaleResource, objKey client.ObjectKey) {
	t.Helper()

	fakeClient := setupFakeClientScaleHook(t)

	var err error

	switch r := resource.(type) {
	case hooks.DeploymentResource:
		err = fakeClient.Create(context.Background(), r.Deployment)
	case hooks.StatefulSetResource:
		err = fakeClient.Create(context.Background(), r.StatefulSet)
	default:
		t.Fatalf("unsupported resource type for creation")
	}

	assert.NoError(t, err)

	scaleHook := hooks.ScaleHook{Client: fakeClient}
	log := zap.New(zap.UseDevMode(true))

	err = scaleHook.ScaleUpResource(resource, log)
	assert.NoError(t, err)

	var got client.Object

	switch resource.(type) {
	case hooks.DeploymentResource:
		got = &appsv1.Deployment{}
	case hooks.StatefulSetResource:
		got = &appsv1.StatefulSet{}
	default:
		t.Fatalf("unsupported resource type for get")
	}

	err = fakeClient.Get(context.Background(), objKey, got)
	assert.NoError(t, err)

	var replicas *int32

	var annotations map[string]string

	switch obj := got.(type) {
	case *appsv1.Deployment:
		replicas = obj.Spec.Replicas
		annotations = obj.Annotations
	case *appsv1.StatefulSet:
		replicas = obj.Spec.Replicas
		annotations = obj.Annotations
	}

	assert.Equal(t, int32(5), *replicas)

	_, annotExists := annotations["ramendr.io/scale-hook-replicas-count"]
	assert.False(t, annotExists)
}
