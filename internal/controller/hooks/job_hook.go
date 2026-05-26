// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/ramendr/ramen/internal/controller/hooks/common"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	jobPollInterval           = 5 * time.Second
	jobDeletionTimeoutSeconds = 60
	jobDeletionPollSeconds    = 2
)

type JobHook struct {
	Hook           *kubeobjects.HookSpec
	Client         client.Client
	Reader         client.Reader
	Scheme         *runtime.Scheme
	RecipeElements util.RecipeElements
}

// Execute creates a Kubernetes Job based on the hook definition and monitors its completion status.
func (j JobHook) Execute(log logr.Logger) error {
	log.Info("Executing job hook", "hook", j.Hook.Name, "namespace", j.Hook.Namespace, "jobName", j.Hook.Job.Name)

	// Get the job template from the recipe
	jobTemplate, err := j.getJobTemplateFromRecipe()
	if err != nil {
		return fmt.Errorf("failed to get job template from recipe: %w", err)
	}

	// Create or get the job
	ctx := context.Background()

	job, err := j.CreateOrGetJob(ctx, jobTemplate, log)
	if err != nil {
		return fmt.Errorf("failed to create or get job: %w", err)
	}

	// Monitor job completion
	err = j.MonitorJobCompletion(job, log)
	if err != nil {
		// Check if inverse operation should be executed
		if common.ShouldInverseOpBeExecuted(j.Hook.Job.InverseOp, j.Hook, err) {
			j.executeInverseOp(log)
		}

		if common.ShouldFailOnError(j.Hook) {
			return fmt.Errorf("job hook failed: %w", err)
		}

		log.Info("Job hook failed but continuing due to onError=continue", "hook", j.Hook.Name, "error", err)

		return nil
	}

	log.Info("Job hook executed successfully", "hook", j.Hook.Name, "jobName", j.Hook.Job.Name)

	return nil
}

// getJobTemplateFromRecipe retrieves the job template from the recipe specification.
func (j JobHook) getJobTemplateFromRecipe() (*batchv1.Job, error) {
	// Get the job template from RecipeSpec.Jobs field
	jobs := j.RecipeElements.RecipeWithParams.Spec.Jobs

	// Look for the job template by name
	var jobTemplateStr *string

	for _, jobMap := range jobs {
		if templateStr, exists := jobMap[j.Hook.Job.Name]; exists {
			jobTemplateStr = templateStr

			break
		}
	}

	if jobTemplateStr == nil {
		return nil, fmt.Errorf("job template '%s' not found in recipe jobs field", j.Hook.Job.Name)
	}

	// Parse the job template from YAML/JSON string
	job := &batchv1.Job{}

	// Try YAML first, then JSON
	err := yaml.Unmarshal([]byte(*jobTemplateStr), job)
	if err != nil {
		// Try JSON if YAML fails
		err = json.Unmarshal([]byte(*jobTemplateStr), job)
		if err != nil {
			return nil, fmt.Errorf("failed to parse job template for '%s': %w", j.Hook.Job.Name, err)
		}
	}

	// Override name and namespace from hook spec
	job.Name = j.Hook.Job.Name
	job.Namespace = j.Hook.Namespace

	// Add labels for tracking
	if job.Labels == nil {
		job.Labels = make(map[string]string)
	}

	job.Labels["ramendr.openshift.io/hook-name"] = j.Hook.Name
	job.Labels["ramendr.openshift.io/job-name"] = j.Hook.Job.Name

	return job, nil
}

// CreateOrGetJob creates a new job or retrieves an existing one based on ForceCreate flag.
func (j JobHook) CreateOrGetJob(ctx context.Context, jobTemplate *batchv1.Job, log logr.Logger) (*batchv1.Job, error) {
	jobKey := types.NamespacedName{
		Name:      j.Hook.Job.Name,
		Namespace: j.Hook.Namespace,
	}

	// Check if job exists
	existingJob, exists, err := j.getExistingJob(ctx, jobKey)
	if err != nil {
		return nil, fmt.Errorf("failed to check job %s in namespace %s: %w",
			j.Hook.Job.Name, j.Hook.Namespace, err)
	}

	// Handle force recreation
	if exists && j.shouldForceRecreate() {
		return j.recreateJob(ctx, existingJob, jobTemplate, log)
	}

	// Use existing job if found
	if exists {
		log.Info("Job already exists, using existing job", "jobName", j.Hook.Job.Name)

		return existingJob, nil
	}

	// Create new job
	return j.createNewJob(ctx, jobTemplate, log)
}

// getExistingJob checks if a job exists and returns it along with existence status.
func (j JobHook) getExistingJob(ctx context.Context, jobKey types.NamespacedName) (*batchv1.Job, bool, error) {
	job := &batchv1.Job{}

	err := j.Client.Get(ctx, jobKey, job)
	if err == nil {
		return job, true, nil
	}

	if k8serrors.IsNotFound(err) {
		return nil, false, nil
	}

	return nil, false, err
}

// shouldForceRecreate returns true if the job should be forcefully recreated.
func (j JobHook) shouldForceRecreate() bool {
	return j.Hook.Job.ForceCreate != nil && *j.Hook.Job.ForceCreate
}

// recreateJob deletes an existing job and creates a new one.
func (j JobHook) recreateJob(
	ctx context.Context, existingJob, jobTemplate *batchv1.Job, log logr.Logger,
) (*batchv1.Job, error) {
	log.Info("Job exists but ForceCreate is true, deleting existing job", "jobName", j.Hook.Job.Name)

	// Delete the existing job
	if err := j.Client.Delete(ctx, existingJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		return nil, fmt.Errorf("failed to delete existing job %s in namespace %s: %w",
			j.Hook.Job.Name, j.Hook.Namespace, err)
	}

	// Wait for job to be deleted
	jobKey := types.NamespacedName{
		Name:      j.Hook.Job.Name,
		Namespace: j.Hook.Namespace,
	}
	if err := j.waitForJobDeletion(ctx, jobKey, log); err != nil {
		return nil, fmt.Errorf("failed waiting for job %s deletion in namespace %s: %w",
			j.Hook.Job.Name, j.Hook.Namespace, err)
	}

	// Create new job
	if err := j.Client.Create(ctx, jobTemplate); err != nil {
		return nil, fmt.Errorf("failed to create job %s in namespace %s after deletion: %w",
			j.Hook.Job.Name, j.Hook.Namespace, err)
	}

	log.Info("Job created successfully after deletion", "jobName", j.Hook.Job.Name)

	return jobTemplate, nil
}

// createNewJob creates a new job.
func (j JobHook) createNewJob(ctx context.Context, jobTemplate *batchv1.Job, log logr.Logger) (*batchv1.Job, error) {
	if err := j.Client.Create(ctx, jobTemplate); err != nil {
		return nil, fmt.Errorf("failed to create job %s in namespace %s: %w",
			j.Hook.Job.Name, j.Hook.Namespace, err)
	}

	log.Info("Job created successfully", "jobName", j.Hook.Job.Name)

	return jobTemplate, nil
}

// waitForJobDeletion waits for a job to be fully deleted.
func (j JobHook) waitForJobDeletion(ctx context.Context, jobKey types.NamespacedName, log logr.Logger) error {
	// Create a timeout context for deletion wait
	deleteCtx, cancel := context.WithTimeout(ctx, jobDeletionTimeoutSeconds*time.Second)
	defer cancel()

	ticker := time.NewTicker(jobDeletionPollSeconds * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deleteCtx.Done():
			return fmt.Errorf("timeout waiting for job %s deletion in namespace %s",
				jobKey.Name, jobKey.Namespace)
		case <-ticker.C:
			job := &batchv1.Job{}
			err := j.Client.Get(deleteCtx, jobKey, job)

			if k8serrors.IsNotFound(err) {
				log.Info("Job deleted successfully", "jobName", jobKey.Name)

				return nil
			}

			if err != nil {
				return fmt.Errorf("error checking job %s deletion status in namespace %s: %w",
					jobKey.Name, jobKey.Namespace, err)
			}

			log.Info("Waiting for job deletion", "jobName", jobKey.Name)
		}
	}
}

// MonitorJobCompletion monitors the job until it completes or times out.
func (j JobHook) MonitorJobCompletion(job *batchv1.Job, log logr.Logger) error {
	timeout := common.GetHookTimeout(j.Hook)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	ticker := time.NewTicker(jobPollInterval)
	defer ticker.Stop()

	jobKey := types.NamespacedName{Name: job.Name, Namespace: job.Namespace}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for job completion after %d seconds", timeout)
		case <-ticker.C:
			if done, err := j.checkJobStatus(ctx, jobKey, job.Name, log); done || err != nil {
				return err
			}
		}
	}
}

// checkJobStatus fetches the current job and inspects its conditions.
// Returns (true, nil) on success, (true, err) on failure, (false, nil) if still running.
func (j JobHook) checkJobStatus(ctx context.Context, jobKey types.NamespacedName, jobName string,
	log logr.Logger,
) (bool, error) {
	currentJob := &batchv1.Job{}
	if err := j.Client.Get(ctx, jobKey, currentJob); err != nil {
		return true, fmt.Errorf("failed to get job status: %w", err)
	}

	for _, cond := range currentJob.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			log.Info("Job completed successfully",
				"jobName", jobName,
				"succeeded", currentJob.Status.Succeeded,
				"completionTime", currentJob.Status.CompletionTime)

			return true, nil
		}

		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return true, fmt.Errorf("job failed: succeeded=%d, failed=%d",
				currentJob.Status.Succeeded, currentJob.Status.Failed)
		}
	}

	log.Info("Job still running",
		"jobName", jobName,
		"active", currentJob.Status.Active,
		"succeeded", currentJob.Status.Succeeded,
		"failed", currentJob.Status.Failed)

	return false, nil
}

// executeInverseOp executes the inverse operation if defined.
func (j JobHook) executeInverseOp(log logr.Logger) {
	inverseOp := j.Hook.Job.InverseOp
	log.Info("Executing inverse operation", "inverseOp", inverseOp, "namespace", j.Hook.Namespace)

	hookSpecForInvOp := j.getHookSpecForInverseOp(inverseOp)
	if hookSpecForInvOp == nil {
		log.Error(nil, "Inverse operation not found in recipe", "inverseOp", inverseOp)

		return
	}

	executor, err := GetHookExecutor(HookContext{
		Hook:           *hookSpecForInvOp,
		Client:         j.Client,
		Reader:         j.Reader,
		Scheme:         j.Scheme,
		RecipeElements: j.RecipeElements,
	})
	if err != nil {
		log.Error(err, "Failed to resolve executor for inverse operation", "inverseOp", inverseOp)

		return
	}

	if err := executor.Execute(log); err != nil {
		log.Error(err, "Failed to execute inverse operation", "inverseOp", inverseOp)

		return
	}

	log.Info("Inverse operation executed successfully", "inverseOp", inverseOp)
}

// getHookSpecForInverseOp retrieves the hook specification for the inverse operation.
// It supports all hook types: exec, check, scale, and job.
func (j JobHook) getHookSpecForInverseOp(inverseOp string) *kubeobjects.HookSpec {
	hooks := j.RecipeElements.RecipeWithParams.Spec.Hooks

	return common.GetHookSpecForInverseOp(hooks, inverseOp, j.Hook.Name)
}
