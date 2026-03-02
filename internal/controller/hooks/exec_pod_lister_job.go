// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// JobPodLister handles pod discovery when SelectResource is "job".
// It finds Jobs and returns pods owned by those Jobs that are currently active (Running).
type JobPodLister struct {
	ExecHook
}

// GetPods returns pods from Jobs matching the selector criteria.
// When SinglePodOnly is true, it returns one active pod per Job.
// When SinglePodOnly is false, it returns all active pods from all matched Jobs.
func (l *JobPodLister) GetPods(log logr.Logger) ([]ExecPodSpec, error) {
	jobs, err := l.getJobs(log)
	if err != nil {
		return nil, err
	}

	if l.Hook.SinglePodOnly {
		return l.getOneActivePodPerJob(jobs, log)
	}

	return l.getAllActivePodsFromJobs(jobs, log)
}

//nolint:dupl // getCronJobs and getStatefulSets mirror this structure; all use label/name selectors for different resource types
func (l *JobPodLister) getJobs(log logr.Logger) ([]batchv1.Job, error) {
	jobList := &batchv1.JobList{}
	jobs := make([]batchv1.Job, 0)

	if l.Hook.LabelSelector != nil {
		err := getResourcesUsingLabelSelector(l.Reader, l.Hook, jobList)
		if err != nil {
			return jobs, err
		}

		jobs = append(jobs, jobList.Items...)

		log.Info("jobs count obtained using label selector", "hook", l.Hook.Name,
			"labelSelector", l.Hook.LabelSelector, "selectResource", l.Hook.SelectResource,
			"jobCount", len(jobs))
	}

	if l.Hook.NameSelector != "" {
		selectorType, objs, err := getResourcesUsingNameSelector(l.Reader, l.Hook, jobList)
		if err != nil {
			return jobs, err
		}

		for _, obj := range objs {
			j, ok := obj.(*batchv1.Job)
			if ok {
				jobs = append(jobs, *j)
			}
		}

		log.Info("jobs count obtained using name selector", "hook", l.Hook.Name,
			"nameSelector", l.Hook.NameSelector, "selectorType", selectorType,
			"selectResource", l.Hook.SelectResource, "jobCount", len(jobs))
	}

	return jobs, nil
}

func (l *JobPodLister) getOneActivePodPerJob(jobs []batchv1.Job, log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	for _, job := range jobs {
		pod, err := l.getFirstActivePodForJob(&job)
		if err != nil {
			return execPods, fmt.Errorf("error getting active pod for job %s: %w", job.Name, err)
		}

		if pod == nil {
			log.Info("no active pod found for job", "job", job.Name, "namespace", job.Namespace)

			continue
		}

		cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
		if err != nil {
			return execPods, fmt.Errorf("error converting command to string array: %w", err)
		}

		execPods = append(execPods, getExecPodSpec(l.Hook.Op.Container, cmd, pod))
	}

	return execPods, nil
}

func (l *JobPodLister) getAllActivePodsFromJobs(jobs []batchv1.Job, log logr.Logger) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
	if err != nil {
		return execPods, fmt.Errorf("error converting command to string array: %w", err)
	}

	log.V(1).Info("getAllActivePodsFromJobs", "jobCount", len(jobs))

	for _, job := range jobs {
		pods, err := listActivePodsForJob(context.Background(), l.Reader, &job, cmd, l.Hook.Op.Container)
		if err != nil {
			return execPods, fmt.Errorf("error listing pods for job %s: %w", job.Name, err)
		}

		execPods = append(execPods, pods...)
	}

	return execPods, nil
}

func (l *JobPodLister) getFirstActivePodForJob(job *batchv1.Job) (*corev1.Pod, error) {
	podList := &corev1.PodList{}

	err := l.Reader.List(context.Background(), podList, client.InNamespace(job.Namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	totalPods := len(podList.Items)
	jobOwnedPods := 0
	runningPods := 0

	for i := range podList.Items {
		pod := &podList.Items[i]

		// Check if pod is owned by this job (for logging purposes)
		for _, ownerRef := range pod.OwnerReferences {
			if isOwnerCorrect(&ownerRef, job.Name, "Job") {
				jobOwnedPods++

				if pod.Status.Phase == corev1.PodRunning {
					runningPods++
				}

				break
			}
		}

		if IsPodOwnedByJob(pod, job.Name) {
			return pod, nil
		}
	}

	// Log details when no pod is found
	log := logr.FromContextOrDiscard(context.Background())
	log.V(1).Info("getFirstActivePodForJob - no running pod found",
		"job", job.Name,
		"namespace", job.Namespace,
		"totalPodsInNamespace", totalPods,
		"jobOwnedPods", jobOwnedPods,
		"runningJobPods", runningPods)

	return nil, nil
}

// listActivePodsForJob lists all Running pods owned by the given Job.
func listActivePodsForJob(
	ctx context.Context,
	reader client.Reader,
	job *batchv1.Job,
	cmd []string,
	container string,
) ([]ExecPodSpec, error) {
	podList := &corev1.PodList{}

	err := reader.List(ctx, podList, client.InNamespace(job.Namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	execPods := make([]ExecPodSpec, 0)
	totalPods := len(podList.Items)
	jobOwnedPods := 0
	runningPods := 0
	completedPods := 0

	for i := range podList.Items {
		pod := &podList.Items[i]

		// Check if pod is owned by this job (regardless of phase)
		isOwnedByJob := false

		for _, ownerRef := range pod.OwnerReferences {
			if isOwnerCorrect(&ownerRef, job.Name, "Job") {
				isOwnedByJob = true
				jobOwnedPods++

				break
			}
		}

		if isOwnedByJob {
			switch pod.Status.Phase {
			case corev1.PodRunning:
				runningPods++

				execPods = append(execPods, getExecPodSpec(container, cmd, pod))
			case corev1.PodSucceeded, corev1.PodFailed:
				completedPods++
			case corev1.PodPending, corev1.PodUnknown:
				// Pod is not ready yet, don't count it
			}
		}
	}

	logJobPodDetails(ctx, job.Name, job.Namespace, totalPods, jobOwnedPods, runningPods, completedPods, len(execPods))

	return execPods, nil
}

// logJobPodDetails logs diagnostic information about job pods.
func logJobPodDetails(
	ctx context.Context, jobName, namespace string,
	totalPods, jobOwnedPods, runningPods, completedPods, execPodsReturned int,
) {
	log := logr.FromContextOrDiscard(ctx)

	// Always log at INFO level when no running pods found to help debugging
	if runningPods == 0 && jobOwnedPods > 0 {
		if completedPods > 0 {
			log.Info("listActivePodsForJob - job completed, no running pods",
				"job", jobName,
				"namespace", namespace,
				"totalPodsInNamespace", totalPods,
				"jobOwnedPods", jobOwnedPods,
				"completedPods", completedPods,
				"runningJobPods", runningPods)
		} else {
			log.Info("listActivePodsForJob - pods exist but not running yet",
				"job", jobName,
				"namespace", namespace,
				"totalPodsInNamespace", totalPods,
				"jobOwnedPods", jobOwnedPods,
				"runningJobPods", runningPods)
		}
	} else {
		log.V(1).Info("listActivePodsForJob details",
			"job", jobName,
			"namespace", namespace,
			"totalPodsInNamespace", totalPods,
			"jobOwnedPods", jobOwnedPods,
			"runningJobPods", runningPods,
			"completedPods", completedPods,
			"execPodsReturned", execPodsReturned)
	}
}

// IsPodOwnedByJob checks if a pod is owned by the specified Job and is running.
func IsPodOwnedByJob(pod *corev1.Pod, jobName string) bool {
	for _, ownerRef := range pod.OwnerReferences {
		if isOwnerCorrect(&ownerRef, jobName, "Job") && pod.Status.Phase == corev1.PodRunning {
			return true
		}
	}

	return false
}

// IsJobOwnedByCronJob checks if a Job is owned by the specified CronJob.
func IsJobOwnedByCronJob(job *batchv1.Job, cronJobName string) bool {
	for _, ownerRef := range job.OwnerReferences {
		if isOwnerCorrect(&ownerRef, cronJobName, "CronJob") {
			return true
		}
	}

	return false
}
