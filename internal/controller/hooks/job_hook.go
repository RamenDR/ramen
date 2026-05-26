// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	Recipe "github.com/ramendr/recipe/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
)

const (
	jobPollInterval = 5 * time.Second
)

type JobHook struct {
	Hook           *kubeobjects.HookSpec
	Client         client.Client
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
	job, err := j.createOrGetJob(ctx, jobTemplate, log)
	if err != nil {
		return fmt.Errorf("failed to create or get job: %w", err)
	}

	// Monitor job completion
	err = j.monitorJobCompletion(job, log)
	if err != nil {
		// Check if inverse operation should be executed
		if j.shouldExecuteInverseOp(err) {
			j.executeInverseOp(log)
		}

		if shouldJobHookBeFailedOnError(j.Hook) {
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

// createOrGetJob creates a new job or retrieves an existing one based on ForceCreate flag.
func (j JobHook) createOrGetJob(ctx context.Context, jobTemplate *batchv1.Job, log logr.Logger) (*batchv1.Job, error) {
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
func (j JobHook) recreateJob(ctx context.Context, existingJob, jobTemplate *batchv1.Job, log logr.Logger) (*batchv1.Job, error) {
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
	// Create a timeout context for deletion wait (60 seconds)
	deleteCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
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

// monitorJobCompletion monitors the job until it completes or times out.
func (j JobHook) monitorJobCompletion(job *batchv1.Job, log logr.Logger) error {
	timeout := getJobHookTimeoutValue(j.Hook)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	ticker := time.NewTicker(jobPollInterval)
	defer ticker.Stop()

	jobKey := types.NamespacedName{
		Name:      job.Name,
		Namespace: job.Namespace,
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for job completion after %d seconds", timeout)
		case <-ticker.C:
			currentJob := &batchv1.Job{}
			if err := j.Client.Get(context.Background(), jobKey, currentJob); err != nil {
				return fmt.Errorf("failed to get job status: %w", err)
			}

			// Check if job succeeded
			if currentJob.Status.Succeeded > 0 {
				log.Info("Job completed successfully",
					"jobName", job.Name,
					"succeeded", currentJob.Status.Succeeded,
					"completionTime", currentJob.Status.CompletionTime)

				return nil
			}

			// Check if job failed
			if currentJob.Status.Failed > 0 {
				return fmt.Errorf("job failed: succeeded=%d, failed=%d",
					currentJob.Status.Succeeded, currentJob.Status.Failed)
			}

			// Job is still running
			log.Info("Job still running",
				"jobName", job.Name,
				"active", currentJob.Status.Active,
				"succeeded", currentJob.Status.Succeeded,
				"failed", currentJob.Status.Failed)
		}
	}
}

// shouldExecuteInverseOp determines if the inverse operation should be executed.
func (j JobHook) shouldExecuteInverseOp(err error) bool {
	return err != nil && j.Hook.Job.InverseOp != "" && shouldJobHookBeFailedOnError(j.Hook)
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

	tempJobHook := JobHook{
		Hook:           hookSpecForInvOp,
		Client:         j.Client,
		RecipeElements: j.RecipeElements,
	}

	if err := tempJobHook.Execute(log); err != nil {
		log.Error(err, "Failed to execute inverse operation", "inverseOp", inverseOp)

		return
	}

	log.Info("Inverse operation executed successfully", "inverseOp", inverseOp)
}

// getHookSpecForInverseOp retrieves the hook specification for the inverse operation.
// It supports all hook types: exec, check, scale, and job.
func (j JobHook) getHookSpecForInverseOp(inverseOp string) *kubeobjects.HookSpec {
	hookName, opName := parseInverseOp(inverseOp, j.Hook.Name)
	if hookName == "" || opName == "" {
		return nil
	}

	hooks := j.RecipeElements.RecipeWithParams.Spec.Hooks

	// Find the hook by name and dispatch based on its type
	for i := range hooks {
		if hooks[i].Name == hookName {
			return getHookSpecByType(hooks[i], opName)
		}
	}

	return nil
}

// parseInverseOp parses the inverse operation string into hookName and opName.
// Supported formats:
//   - "/opName" - uses defaultHookName with the specified opName
//   - "hookName/opName" - uses the specified hookName and opName
//   - "opName" - uses defaultHookName with the specified opName
func parseInverseOp(inverseOp, defaultHookName string) (hookName, opName string) {
	if inverseOp == "" {
		return "", ""
	}

	// Handle "/opName" format (same hook)
	if strings.HasPrefix(inverseOp, "/") {
		return defaultHookName, strings.TrimPrefix(inverseOp, "/")
	}

	// Handle "hookName/opName" or "opName" format
	parts := strings.SplitN(inverseOp, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	// Just "opName" - use default hook
	return defaultHookName, parts[0]
}

// getHookSpecByType dispatches to the appropriate hook spec builder based on hook type.
func getHookSpecByType(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	switch hook.Type {
	case "job":
		return getJobHookSpec(hook, opName)
	case "exec":
		return getExecHookSpec(hook, opName)
	case "check":
		return getCheckHookSpec(hook, opName)
	case "scale":
		return getScaleHookSpec(hook, opName)
	default:
		return nil
	}
}

// getJobHookSpec creates a HookSpec for a job type hook.
func getJobHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	for _, jobDetail := range hook.Jobs {
		if jobDetail.Name == opName {
			return &kubeobjects.HookSpec{
				Name:      hook.Name,
				Namespace: hook.Namespace,
				Type:      "job",
				Job: kubeobjects.JobSpec{
					Name:        jobDetail.Name,
					OnError:     jobDetail.OnError,
					Timeout:     jobDetail.Timeout,
					InverseOp:   jobDetail.InverseOp,
					ForceCreate: jobDetail.ForceCreate,
				},
				Timeout:   hook.Timeout,
				Essential: hook.Essential,
				OnError:   hook.OnError,
			}
		}
	}

	return nil
}

// getExecHookSpec creates a HookSpec for an exec type hook.
func getExecHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	for _, op := range hook.Ops {
		if op.Name == opName {
			return &kubeobjects.HookSpec{
				Name:      hook.Name,
				Namespace: hook.Namespace,
				Type:      "exec",
				Op: kubeobjects.Operation{
					Name:      op.Name,
					Command:   op.Command,
					Container: op.Container,
					InverseOp: op.InverseOp,
				},
				SelectResource: hook.SelectResource,
				LabelSelector:  hook.LabelSelector,
				NameSelector:   hook.NameSelector,
				SinglePodOnly:  hook.SinglePodOnly,
				Timeout:        hook.Timeout,
				Essential:      hook.Essential,
				OnError:        hook.OnError,
			}
		}
	}

	return nil
}

// getCheckHookSpec creates a HookSpec for a check type hook.
func getCheckHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	for _, chk := range hook.Chks {
		if chk.Name == opName {
			return &kubeobjects.HookSpec{
				Name:      hook.Name,
				Namespace: hook.Namespace,
				Type:      "check",
				Chk: kubeobjects.Check{
					Name:      chk.Name,
					Condition: chk.Condition,
				},
				SelectResource:       hook.SelectResource,
				LabelSelector:        hook.LabelSelector,
				NameSelector:         hook.NameSelector,
				Timeout:              chk.Timeout,
				OnError:              chk.OnError,
				SkipHookIfNotPresent: hook.SkipHookIfNotPresent,
				Essential:            hook.Essential,
			}
		}
	}

	return nil
}

// getScaleHookSpec creates a HookSpec for a scale type hook.
func getScaleHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	return &kubeobjects.HookSpec{
		Name:           hook.Name,
		Namespace:      hook.Namespace,
		Type:           "scale",
		SelectResource: hook.SelectResource,
		LabelSelector:  hook.LabelSelector,
		NameSelector:   hook.NameSelector,
		Essential:      hook.Essential,
		Timeout:        hook.Timeout,
		OnError:        hook.OnError,
		Scale: kubeobjects.ScaleSpec{
			Operation: opName,
		},
	}
}

// shouldJobHookBeFailedOnError determines if the job hook should fail on error.
func shouldJobHookBeFailedOnError(hook *kubeobjects.HookSpec) bool {
	// hook.Job.OnError overwrites the feature of hook.OnError -- defaults to fail
	if hook.Job.OnError != "" {
		return hook.Job.OnError != "continue"
	}

	if hook.OnError != "" {
		return hook.OnError != "continue"
	}

	return true
}

// getJobHookTimeoutValue returns the timeout value for the job hook.
func getJobHookTimeoutValue(hook *kubeobjects.HookSpec) int {
	if hook.Job.Timeout != 0 {
		return hook.Job.Timeout
	}

	if hook.Timeout != 0 {
		return hook.Timeout
	}

	// 300s is the default value for timeout
	return defaultTimeoutValue
}
