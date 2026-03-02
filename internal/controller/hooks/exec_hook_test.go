package hooks_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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

//nolint:dupl // mirrors TestGetPodsForJobWithLabelSelectorSinglePod; both test single-pod label-selector path for different resource types
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

// ---------------------------------------------------------------------------
// Job helpers
// ---------------------------------------------------------------------------

//nolint:unparam // name and namespace are kept as params to mirror the pattern of getDaemonSet/getStatefulSet
func getJob(name, namespace string, activeCount int32, labels map[string]string) *batchv1.Job {
	if labels == nil {
		labels = map[string]string{"appname": "busybox"}
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test-container", Image: "test-image"},
					},
				},
			},
		},
		Status: batchv1.JobStatus{
			Active: activeCount,
		},
	}
}

//nolint:unparam // namespace is kept as a param to mirror getPodOwnedByDaemonSet/getPodOwnedByStatefulSet
func getPodOwnedByJob(podName, namespace, jobName, jobUID string) *corev1.Pod {
	controllerTrue := true

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels:    map[string]string{"appname": "busybox"},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "batch/v1",
					Kind:       "Job",
					Name:       jobName,
					UID:        types.UID(jobUID),
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

// ---------------------------------------------------------------------------
// CronJob helpers
// ---------------------------------------------------------------------------

//nolint:unparam // name and namespace are kept as params to mirror getDaemonSet/getStatefulSet
func getCronJob(name, namespace string, labels map[string]string) *batchv1.CronJob {
	if labels == nil {
		labels = map[string]string{"appname": "busybox"}
	}

	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/1 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "test-container", Image: "test-image"},
							},
						},
					},
				},
			},
		},
	}
}

//nolint:unparam // namespace is kept as a param for consistency with other owned-resource helpers
func getJobOwnedByCronJob(jobName, namespace, cronJobName, cronJobUID string, activeCount int32) *batchv1.Job {
	controllerTrue := true

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "batch/v1",
					Kind:       "CronJob",
					Name:       cronJobName,
					UID:        types.UID(cronJobUID),
					Controller: &controllerTrue,
				},
			},
		},
		Status: batchv1.JobStatus{
			Active: activeCount,
		},
	}
}

// ---------------------------------------------------------------------------
// Job tests
// ---------------------------------------------------------------------------

//nolint:dupl // mirrors TestGetPodsForStatefulSetWithLabelSelector; both test single-pod label-selector path for different resource types
func TestGetPodsForJobWithLabelSelectorSinglePod(t *testing.T) {
	fakeClient := setup(t)

	job := getJob("test-job", "test-ns", 1, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod := getPodOwnedByJob("test-job-pod", "test-ns", "test-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "job"
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
		PodName:   "test-job-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}, pods[0])
}

func TestGetPodsForJobAllPods(t *testing.T) {
	fakeClient := setup(t)

	job := getJob("test-job", "test-ns", 2, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod1 := getPodOwnedByJob("test-job-pod-1", "test-ns", "test-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod1)
	assert.NoError(t, err)

	pod2 := getPodOwnedByJob("test-job-pod-2", "test-ns", "test-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod2)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "job"
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
	assert.Contains(t, names, "test-job-pod-1")
	assert.Contains(t, names, "test-job-pod-2")
}

func TestGetPodsForJobNoActivePods(t *testing.T) {
	fakeClient := setup(t)

	// Job with Active=0 but pod still Running; pod should be returned since it's actually running.
	// The Job status field may not be updated yet, but if the pod is Running, we should execute on it.
	job := getJob("test-job", "test-ns", 0, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod := getPodOwnedByJob("test-job-pod", "test-ns", "test-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "job"
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
	// Pod is Running, so it should be returned even if Job.Status.Active is 0
	assert.Len(t, pods, 1)
	assert.Equal(t, "test-job-pod", pods[0].PodName)
}

func TestGetPodsForJobWithCompletedPods(t *testing.T) {
	fakeClient := setup(t)

	// Job with Active=0 and pod in Succeeded state; no pods should be returned.
	job := getJob("test-job", "test-ns", 0, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod := getPodOwnedByJob("test-job-pod", "test-ns", "test-job", "job-uid")
	// Set pod to Succeeded state (truly completed)
	pod.Status.Phase = corev1.PodSucceeded
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "job"
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
	// Pod is Succeeded, so it should NOT be returned
	assert.Len(t, pods, 0)
}

func TestExecHookFailsWhenNoPodsFound(t *testing.T) {
	fakeClient := setup(t)

	// Job exists but has no running pods
	job := getJob("test-job", "test-ns", 0, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "job"
	hookSpec.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"appname": "busybox"},
	}
	hookSpec.SinglePodOnly = true
	hookSpec.SkipHookIfNotPresent = false // Should fail when no pods found

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}
	log := zap.New(zap.UseDevMode(true))

	// Execute should fail because no running pods are found
	err = eHook.Execute(log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no running pods found")
}

func TestExecHookSkipsWhenNoPodsFoundAndSkipFlagSet(t *testing.T) {
	fakeClient := setup(t)

	// Job exists but has no running pods
	job := getJob("test-job", "test-ns", 0, map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "job"
	hookSpec.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"appname": "busybox"},
	}
	hookSpec.SinglePodOnly = true
	hookSpec.SkipHookIfNotPresent = true // Should skip when no pods found

	eHook := hooks.ExecHook{
		Hook:   hookSpec,
		Reader: fakeClient,
		Scheme: fakeClient.Scheme(),
	}
	log := zap.New(zap.UseDevMode(true))

	// Execute should succeed (skip) because skipHookIfNotPresent is true
	err = eHook.Execute(log)
	assert.Error(t, err)
}

func TestGetPodsForJobWithNameSelector(t *testing.T) {
	fakeClient := setup(t)

	job := getJob("test-job", "test-ns", 1, nil)
	err := fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod := getPodOwnedByJob("test-job-pod", "test-ns", "test-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "job"
	hookSpec.NameSelector = "test-job"

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
	assert.Equal(t, "test-job-pod", pods[0].PodName)
}

// ---------------------------------------------------------------------------
// CronJob tests
// ---------------------------------------------------------------------------

func TestGetPodsForCronJobWithLabelSelectorSinglePod(t *testing.T) {
	fakeClient := setup(t)

	cj := getCronJob("test-cj", "test-ns", map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), cj)
	assert.NoError(t, err)

	job := getJobOwnedByCronJob("test-cj-job", "test-ns", "test-cj", "cj-uid", 1)
	err = fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod := getPodOwnedByJob("test-cj-pod", "test-ns", "test-cj-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "cronjob"
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
		PodName:   "test-cj-pod",
		Namespace: "test-ns",
		Command:   []string{"echo", "hello"},
		Container: "test-container",
	}, pods[0])
}

func TestGetPodsForCronJobAllPods(t *testing.T) {
	fakeClient := setup(t)

	cj := getCronJob("test-cj", "test-ns", map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), cj)
	assert.NoError(t, err)

	job := getJobOwnedByCronJob("test-cj-job", "test-ns", "test-cj", "cj-uid", 2)
	err = fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod1 := getPodOwnedByJob("test-cj-pod-1", "test-ns", "test-cj-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod1)
	assert.NoError(t, err)

	pod2 := getPodOwnedByJob("test-cj-pod-2", "test-ns", "test-cj-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod2)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "cronjob"
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
	assert.Contains(t, names, "test-cj-pod-1")
	assert.Contains(t, names, "test-cj-pod-2")
}

func TestGetPodsForCronJobNoActiveJobs(t *testing.T) {
	fakeClient := setup(t)

	cj := getCronJob("test-cj", "test-ns", map[string]string{"appname": "busybox"})
	err := fakeClient.Create(context.Background(), cj)
	assert.NoError(t, err)

	// Job owned by the CronJob but with Active=0 (completed).
	job := getJobOwnedByCronJob("test-cj-job", "test-ns", "test-cj", "cj-uid", 0)
	err = fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "cronjob"
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
	assert.Len(t, pods, 0)
}

func TestGetPodsForCronJobWithNameSelector(t *testing.T) {
	fakeClient := setup(t)

	cj := getCronJob("test-cj", "test-ns", nil)
	err := fakeClient.Create(context.Background(), cj)
	assert.NoError(t, err)

	job := getJobOwnedByCronJob("test-cj-job", "test-ns", "test-cj", "cj-uid", 1)
	err = fakeClient.Create(context.Background(), job)
	assert.NoError(t, err)

	pod := getPodOwnedByJob("test-cj-pod", "test-ns", "test-cj-job", "job-uid")
	err = fakeClient.Create(context.Background(), pod)
	assert.NoError(t, err)

	hookSpec := getOpHookSpec()
	hookSpec.SelectResource = "cronjob"
	hookSpec.NameSelector = "test-cj"

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
	assert.Equal(t, "test-cj-pod", pods[0].PodName)
}

func TestIsPodOwnedByJob(t *testing.T) {
	pod := getPodSpec("test-pod")
	pod.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: "batch/v1",
			Kind:       "Job",
			Name:       "test-job",
			UID:        "test-uid",
		},
	}
	pod.Status = corev1.PodStatus{Phase: corev1.PodRunning}

	// Controller flag not set — should be false.
	assert.False(t, hooks.IsPodOwnedByJob(pod, "test-job"))

	isController := true
	pod.OwnerReferences[0].Controller = &isController
	assert.True(t, hooks.IsPodOwnedByJob(pod, "test-job"))

	// Wrong job name — should be false.
	assert.False(t, hooks.IsPodOwnedByJob(pod, "other-job"))
}

func TestIsJobOwnedByCronJob(t *testing.T) {
	job := getJobOwnedByCronJob("test-job", "test-ns", "test-cj", "cj-uid", 1)

	assert.True(t, hooks.IsJobOwnedByCronJob(job, "test-cj"))
	assert.False(t, hooks.IsJobOwnedByCronJob(job, "other-cj"))

	// Controller flag not set — should be false.
	job.OwnerReferences[0].Controller = nil
	assert.False(t, hooks.IsJobOwnedByCronJob(job, "test-cj"))
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func setup(t *testing.T) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = appsv1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = batchv1.AddToScheme(scheme)
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
