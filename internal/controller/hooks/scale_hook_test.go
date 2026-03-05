package hooks_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
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

// Helper to create resource in fake client
func createResource(t *testing.T, fakeClient client.Client, resource hooks.Resource) {
	t.Helper()

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
}

// Helper to get resource from fake client
func getResource(
	t *testing.T,
	fakeClient client.Client,
	objKey client.ObjectKey,
	resource hooks.Resource,
) client.Object {
	t.Helper()

	var got client.Object

	switch resource.(type) {
	case hooks.DeploymentResource:
		got = &appsv1.Deployment{}
	case hooks.StatefulSetResource:
		got = &appsv1.StatefulSet{}
	default:
		t.Fatalf("unsupported resource type for get")
	}

	err := fakeClient.Get(context.Background(), objKey, got)
	assert.NoError(t, err)

	return got
}

// Table-driven test for ScaleDown
func TestScaleDown(t *testing.T) {
	tests := []struct {
		name               string
		resourceType       string
		createFunc         func() hooks.Resource
		getObj             func() client.Object
		expectedReplicas   int32
		expectedAnnotation string
	}{
		{
			name:         "Deployment",
			resourceType: "deployment",
			createFunc: func() hooks.Resource {
				d := newDeployment("scale-deployment", 3, nil)

				return hooks.DeploymentResource{d}
			},
			getObj:             func() client.Object { return &appsv1.Deployment{} },
			expectedReplicas:   0,
			expectedAnnotation: "3",
		},
		{
			name:         "StatefulSet",
			resourceType: "statefulset",
			createFunc: func() hooks.Resource {
				s := newStatefulSet("scale-statefulset", 3, nil)

				return hooks.StatefulSetResource{s}
			},
			getObj:             func() client.Object { return &appsv1.StatefulSet{} },
			expectedReplicas:   0,
			expectedAnnotation: "3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := setupFakeClientScaleHook(t)
			resource := tt.createFunc()
			createResource(t, fakeClient, resource)

			hookSpec := &kubeobjects.HookSpec{
				Name:           "test-hook",
				Scale:          kubeobjects.ScaleSpec{Operation: "down"},
				SelectResource: tt.resourceType,
				Namespace:      "test-ns",
			}
			scaleHook := hooks.ScaleHook{Client: fakeClient, Hook: hookSpec}
			log := zap.New(zap.UseDevMode(true))

			err := scaleHook.ScaleDownResource(resource, log)
			assert.NoError(t, err)

			got := tt.getObj()
			err = fakeClient.Get(context.Background(),
				client.ObjectKey{Name: resource.GetObjectMeta().GetName(), Namespace: "test-ns"}, got)
			assert.NoError(t, err)

			switch obj := got.(type) {
			case *appsv1.Deployment:
				assert.Equal(t, tt.expectedReplicas, *obj.Spec.Replicas)
				assert.Equal(t, tt.expectedAnnotation, obj.Annotations["ramendr.io/scale-hook-replicas-count"])
			case *appsv1.StatefulSet:
				assert.Equal(t, tt.expectedReplicas, *obj.Spec.Replicas)
				assert.Equal(t, tt.expectedAnnotation, obj.Annotations["ramendr.io/scale-hook-replicas-count"])
			}
		})
	}
}

// Existing tests for ScaleUp missing annotation and happy paths, updated with hookSpec initialized

func TestScaleUpDeploymentMissingAnnotation(t *testing.T) {
	fakeClient := setupFakeClientScaleHook(t)
	deployment := newDeployment("scale-deployment", 0, nil)
	err := fakeClient.Create(context.Background(), deployment)
	assert.NoError(t, err)

	resource := hooks.DeploymentResource{deployment}
	hookSpec := &kubeobjects.HookSpec{
		Name:           "test-hook",
		Scale:          kubeobjects.ScaleSpec{Operation: "up"},
		SelectResource: "deployment",
		Namespace:      "test-ns",
	}

	scaleHook := hooks.ScaleHook{
		Client: fakeClient,
		Hook:   hookSpec,
	}

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
	hookSpec := &kubeobjects.HookSpec{
		Name:           "test-hook",
		Scale:          kubeobjects.ScaleSpec{Operation: "up"},
		SelectResource: "statefulset",
		Namespace:      "test-ns",
	}

	scaleHook := hooks.ScaleHook{
		Client: fakeClient,
		Hook:   hookSpec,
	}

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

func runScaleUpResourceTest(t *testing.T, resource hooks.Resource, objKey client.ObjectKey) {
	t.Helper()

	fakeClient := setupFakeClientScaleHook(t)

	createResource(t, fakeClient, resource)

	selectResource := ""

	switch resource.(type) {
	case hooks.DeploymentResource:
		selectResource = "deployment"
	case hooks.StatefulSetResource:
		selectResource = "statefulset"
	}

	hookSpec := &kubeobjects.HookSpec{
		Name:           "test-hook",
		Scale:          kubeobjects.ScaleSpec{Operation: "up"},
		SelectResource: selectResource,
		Namespace:      objKey.Namespace,
	}

	scaleHook := hooks.ScaleHook{
		Client: fakeClient,
		Hook:   hookSpec,
	}
	log := zap.New(zap.UseDevMode(true))

	err := scaleHook.ScaleUpResource(resource, log)
	assert.NoError(t, err)

	got := getResource(t, fakeClient, objKey, resource)

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
