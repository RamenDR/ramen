// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CronJobPodLister handles pod discovery when SelectResource is "cronjob".
// It finds CronJobs, then their active child Jobs, then the pods owned by those Jobs.
type CronJobPodLister struct {
	ExecHook
}

// GetPods returns pods from CronJobs matching the selector criteria.
// When SinglePodOnly is true, it returns one active pod per CronJob (from its first active Job).
// When SinglePodOnly is false, it returns all active pods from all active Jobs of all matched CronJobs.
func (l *CronJobPodLister) GetPods(log logr.Logger) ([]ExecPodSpec, error) {
	cronJobs, err := l.getCronJobs(log)
	if err != nil {
		return nil, err
	}

	if l.Hook.SinglePodOnly {
		return l.getOneActivePodPerCronJob(cronJobs, log)
	}

	return l.getAllActivePodsFromCronJobs(cronJobs, log)
}

//nolint:dupl // getJobs mirrors this structure; both use label/name selectors.
func (l *CronJobPodLister) getCronJobs(log logr.Logger) ([]batchv1.CronJob, error) {
	cronJobList := &batchv1.CronJobList{}
	cronJobs := make([]batchv1.CronJob, 0)

	if l.Hook.LabelSelector != nil {
		err := getResourcesUsingLabelSelector(l.Reader, l.Hook, cronJobList)
		if err != nil {
			return cronJobs, err
		}

		cronJobs = append(cronJobs, cronJobList.Items...)

		log.Info("cronjobs count obtained using label selector", "hook", l.Hook.Name,
			"labelSelector", l.Hook.LabelSelector, "selectResource", l.Hook.SelectResource,
			"cronJobCount", len(cronJobs))
	}

	if l.Hook.NameSelector != "" {
		selectorType, objs, err := getResourcesUsingNameSelector(l.Reader, l.Hook, cronJobList)
		if err != nil {
			return cronJobs, err
		}

		for _, obj := range objs {
			cj, ok := obj.(*batchv1.CronJob)
			if ok {
				cronJobs = append(cronJobs, *cj)
			}
		}

		log.Info("cronjobs count obtained using name selector", "hook", l.Hook.Name,
			"nameSelector", l.Hook.NameSelector, "selectorType", selectorType,
			"selectResource", l.Hook.SelectResource, "cronJobCount", len(cronJobs))
	}

	return cronJobs, nil
}

// getActiveJobsForCronJob returns all Jobs owned by the given CronJob that have at least one active pod.
func (l *CronJobPodLister) getActiveJobsForCronJob(cronJob *batchv1.CronJob) ([]batchv1.Job, error) {
	jobList := &batchv1.JobList{}

	err := l.Reader.List(context.Background(), jobList, client.InNamespace(cronJob.Namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing jobs for cronjob %s: %w", cronJob.Name, err)
	}

	activeJobs := make([]batchv1.Job, 0)

	for i := range jobList.Items {
		job := &jobList.Items[i]
		if IsJobOwnedByCronJob(job, cronJob.Name) && job.Status.Active > 0 {
			activeJobs = append(activeJobs, *job)
		}
	}

	return activeJobs, nil
}

func (l *CronJobPodLister) getOneActivePodPerCronJob(
	cronJobs []batchv1.CronJob, log logr.Logger,
) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	for _, cronJob := range cronJobs {
		activeJobs, err := l.getActiveJobsForCronJob(&cronJob)
		if err != nil {
			return execPods, err
		}

		if len(activeJobs) == 0 {
			log.Info("no active jobs found for cronjob", "cronjob", cronJob.Name, "namespace", cronJob.Namespace)

			continue
		}

		cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
		if err != nil {
			return execPods, fmt.Errorf("error converting command to string array: %w", err)
		}

		// Use the first active Job; SinglePodOnly semantics: one pod is sufficient.
		job := &activeJobs[0]

		pods, err := listActivePodsForJob(context.Background(), l.Reader, job, cmd, l.Hook.Op.Container)
		if err != nil {
			return execPods, fmt.Errorf("error listing pods for job %s: %w", job.Name, err)
		}

		if len(pods) > 0 {
			execPods = append(execPods, pods[0])
		}
	}

	return execPods, nil
}

func (l *CronJobPodLister) getAllActivePodsFromCronJobs(
	cronJobs []batchv1.CronJob, log logr.Logger,
) ([]ExecPodSpec, error) {
	execPods := make([]ExecPodSpec, 0)

	cmd, err := covertCommandToStringArray(l.Hook.Op.Command)
	if err != nil {
		return execPods, fmt.Errorf("error converting command to string array: %w", err)
	}

	log.V(1).Info("getAllActivePodsFromCronJobs", "cronJobCount", len(cronJobs))

	for _, cronJob := range cronJobs {
		activeJobs, err := l.getActiveJobsForCronJob(&cronJob)
		if err != nil {
			return execPods, err
		}

		for i := range activeJobs {
			job := &activeJobs[i]

			pods, err := listActivePodsForJob(context.Background(), l.Reader, job, cmd, l.Hook.Op.Container)
			if err != nil {
				return execPods, fmt.Errorf("error listing pods for job %s: %w", job.Name, err)
			}

			execPods = append(execPods, pods...)
		}
	}

	return execPods, nil
}

// Made with Bob
