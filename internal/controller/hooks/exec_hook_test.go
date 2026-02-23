package hooks_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
)

func setup(t *testing.T) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = appsv1.AddToScheme(scheme)
	assert.NoError(t, err)

	// nolint:errcheck
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Pod{}, "metadata.name", func(obj client.Object) []string {
			return []string{obj.(*corev1.Pod).Name}
		}).Build()
	assert.NotNil(t, fakeClient)

	return fakeClient
}

func getPodSpec(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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

func getRS() *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rs",
			Namespace: "test-ns",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "test-deployment",
					UID:        "test-uid",
				},
			},
		},
	}
}

func getDaemonSet(name, namespace string, labels map[string]string) *appsv1.DaemonSet {
	if labels == nil {
		labels = map[string]string{"appname": "busybox"}
	}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
		},
		Status: appsv1.DaemonSetStatus{
			NumberReady: 1,
		},
	}
}

func getPodOwnedByDaemonSet(podName, namespace, daemonSetName, daemonSetUID string) *corev1.Pod {
	controllerTrue := true

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels:    map[string]string{"appname": "busybox"},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       daemonSetName,
					UID:        types.UID(daemonSetUID),
					Controller: &controllerTrue,
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

func getStatefulSet(name, namespace string, replicas int32, labels map[string]string) *appsv1.StatefulSet {
	if labels == nil {
		labels = map[string]string{"appname": "busybox"}
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: replicas,
		},
	}
}

func getPodOwnedByStatefulSet(podName, namespace, statefulSetName, statefulSetUID string) *corev1.Pod {
	controllerTrue := true

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels:    map[string]string{"appname": "busybox"},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "StatefulSet",
					Name:       statefulSetName,
					UID:        types.UID(statefulSetUID),
					Controller: &controllerTrue,
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

func TestExecuteWithNoSelector(t *testing.T) {
	fakeClient := setup(t)
	pod := getPodSpec("test-pod")

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

func TestGetPodsUsingLabelSelector(t *testing.T) {
	fakeClient := setup(t)
	pod := getPodSpec("test-pod")

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

	lister := hooks.NewPodLister(eHook)
	pods, err := lister.GetPods(log)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(pods))

	expectedPodSpec := hooks.ExecPodSpec{
		PodName:   "test-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}
	assert.Equal(t, expectedPodSpec, pods[0])
}

func TestGetPodsUsingNameSelector(t *testing.T) {
	fakeClient := setup(t)
	pod := getPodSpec("test-pod")

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

	lister := hooks.NewPodLister(eHook)
	pods, err := lister.GetPods(log)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(pods))

	expectedPodSpec := hooks.ExecPodSpec{
		PodName:   "test-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}
	assert.Equal(t, expectedPodSpec, pods[0])
}

func TestGetPodsForSinglePodOnly(t *testing.T) {
	fakeClient := setup(t)
	pod := getPodSpec("test-pod")

	err := fakeClient.Create(context.TODO(), pod)
	assert.NoError(t, err)

	pod = getPodSpec("test-pod-2")
	err = fakeClient.Create(context.TODO(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.NameSelector = "test-pod.*"
	hookSpec.SinglePodOnly = true

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}

	log := zap.New(zap.UseDevMode(true))

	lister := hooks.NewPodLister(eHook)
	pods, err := lister.GetPods(log)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(pods))

	expectedPodSpec := hooks.ExecPodSpec{
		PodName:   "test-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}
	assert.Equal(t, expectedPodSpec, pods[0])
}

func TestIsPodOwnedByRS(t *testing.T) {
	pod := getPodSpec("test-pod")
	pod.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Kind:       "ReplicaSet",
			Name:       "test-rs",
			UID:        "test-uid",
		},
	}

	assert.False(t, hooks.IsPodOwnedByRS(pod, "test-rs"))

	pod.Status = corev1.PodStatus{
		Phase: corev1.PodRunning,
	}

	assert.False(t, hooks.IsPodOwnedByRS(pod, "test-rs"))

	isController := true
	pod.OwnerReferences[0].Controller = &isController
	assert.True(t, hooks.IsPodOwnedByRS(pod, "test-rs"))
}

func TestIsRSOwnedByDeployment(t *testing.T) {
	rs := getRS()
	assert.False(t, hooks.IsRSOwnedByDeployment(rs, "test-deployment"))

	rs.OwnerReferences[0].Controller = nil
	assert.False(t, hooks.IsRSOwnedByDeployment(rs, "test-deployment"))

	isController := true
	rs.OwnerReferences[0].Controller = &isController
	rs.Status = appsv1.ReplicaSetStatus{
		Replicas:      1,
		ReadyReplicas: 1,
	}
	assert.True(t, hooks.IsRSOwnedByDeployment(rs, "test-deployment"))
}

func TestGetPodsForDaemonSetWithLabelSelector(t *testing.T) {
	fakeClient := setup(t)
	ds := getDaemonSet("test-ds", "test-ns", map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), ds)
	assert.NoError(t, err)

	pod := getPodOwnedByDaemonSet("test-ds-pod", "test-ns", "test-ds", "ds-uid")
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "daemonset"
	hookSpec.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"appname": "busybox"},
	}
	hookSpec.SinglePodOnly = true

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}
	log := zap.New(zap.UseDevMode(true))

	lister := hooks.NewPodLister(eHook)
	pods, err := lister.GetPods(log)
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.Equal(t, hooks.ExecPodSpec{
		PodName:   "test-ds-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}, pods[0])
}

func TestGetPodsForDaemonSetAllPods(t *testing.T) {
	fakeClient := setup(t)
	ds := getDaemonSet("test-ds", "test-ns", map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), ds)
	assert.NoError(t, err)

	pod1 := getPodOwnedByDaemonSet("test-ds-pod-1", "test-ns", "test-ds", "ds-uid")
	err = fakeClient.Create(context.Background(), pod1)
	assert.NoError(t, err)

	pod2 := getPodOwnedByDaemonSet("test-ds-pod-2", "test-ns", "test-ds", "ds-uid")
	err = fakeClient.Create(context.Background(), pod2)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "daemonset"
	hookSpec.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"appname": "busybox"},
	}
	hookSpec.SinglePodOnly = false

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}
	log := zap.New(zap.UseDevMode(true))

	lister := hooks.NewPodLister(eHook)
	pods, err := lister.GetPods(log)
	assert.NoError(t, err)
	assert.Len(t, pods, 2)
	names := []string{pods[0].PodName, pods[1].PodName}
	assert.Contains(t, names, "test-ds-pod-1")
	assert.Contains(t, names, "test-ds-pod-2")
}

func TestGetPodsForStatefulSetWithLabelSelector(t *testing.T) {
	fakeClient := setup(t)
	ss := getStatefulSet("test-ss", "test-ns", 1, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), ss)
	assert.NoError(t, err)

	// StatefulSet single-pod path uses Get for name "statefulset-0"
	pod := getPodOwnedByStatefulSet("test-ss-0", "test-ns", "test-ss", "ss-uid")
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "statefulset"
	hookSpec.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"appname": "busybox"},
	}
	hookSpec.SinglePodOnly = true

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}
	log := zap.New(zap.UseDevMode(true))

	lister := hooks.NewPodLister(eHook)
	pods, err := lister.GetPods(log)
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.Equal(t, hooks.ExecPodSpec{
		PodName:   "test-ss-0",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}, pods[0])
}

func TestGetPodsForStatefulSetAllPods(t *testing.T) {
	fakeClient := setup(t)
	ss := getStatefulSet("test-ss", "test-ns", 2, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), ss)
	assert.NoError(t, err)

	pod0 := getPodOwnedByStatefulSet("test-ss-0", "test-ns", "test-ss", "ss-uid")
	err = fakeClient.Create(context.Background(), pod0)
	assert.NoError(t, err)

	pod1 := getPodOwnedByStatefulSet("test-ss-1", "test-ns", "test-ss", "ss-uid")
	err = fakeClient.Create(context.Background(), pod1)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "statefulset"
	hookSpec.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"appname": "busybox"},
	}
	hookSpec.SinglePodOnly = false

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}
	log := zap.New(zap.UseDevMode(true))

	lister := hooks.NewPodLister(eHook)
	pods, err := lister.GetPods(log)
	assert.NoError(t, err)
	assert.Len(t, pods, 2)
	names := []string{pods[0].PodName, pods[1].PodName}
	assert.Contains(t, names, "test-ss-0")
	assert.Contains(t, names, "test-ss-1")
}
