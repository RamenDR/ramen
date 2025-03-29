package hooks_test

import (
	"context"
	"testing"

	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func setup(t *testing.T) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// nolint:errcheck
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Pod{}, "metadata.name", func(obj client.Object) []string {
			return []string{obj.(*corev1.Pod).Name}
		}).Build()
	assert.NotNil(t, fakeClient)

	return fakeClient
}

func getPodSpec() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				"appname": "busybox",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "test-image",
				},
			},
		},
	}
}

func getOpHookSpec() *kubeobjects.HookSpec {
	return &kubeobjects.HookSpec{
		Name:      "test",
		Namespace: "test-ns",
		Type:      "exec",
		Op: kubeobjects.Operation{
			Command: "echo hello",
			Name:    "exec-hook",
		},
	}
}

func TestExecuteWithNoSelector(t *testing.T) {
	fakeClient := setup(t)
	pod := getPodSpec()

	err := fakeClient.Create(context.TODO(), pod)
	assert.NoError(t, err)

	eHook := hooks.ExecHook{
		Hook:   getOpHookSpec(),
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}

	log := zap.New(zap.UseDevMode(true))

	err = eHook.Execute(log)
	assert.Error(t, err, "either nameSelector or labelSelector should be provided to get resources")
}

func TestGetPodsToExecuteCommandsUsingLabelSelector(t *testing.T) {
	fakeClient := setup(t)
	pod := getPodSpec()

	err := fakeClient.Create(context.TODO(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"appname": "busybox"},
	}

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}

	log := zap.New(zap.UseDevMode(true))

	pods := eHook.GetPodsToExecuteCommands(log)
	assert.Equal(t, 1, len(pods))

	expectedPodSpec := hooks.ExecPodSpec{
		PodName:   "test-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}
	assert.Equal(t, expectedPodSpec, pods[0])
}

func TestGetPodsToExecuteCommandsUsingNameSelector(t *testing.T) {
	fakeClient := setup(t)
	pod := getPodSpec()

	err := fakeClient.Create(context.TODO(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.NameSelector = "test-pod"

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}

	log := zap.New(zap.UseDevMode(true))

	pods := eHook.GetPodsToExecuteCommands(log)
	assert.Equal(t, 1, len(pods))

	expectedPodSpec := hooks.ExecPodSpec{
		PodName:   "test-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}
	assert.Equal(t, expectedPodSpec, pods[0])
}
