// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/kubeobjects"
	"github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	Recipe "github.com/ramendr/recipe/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func s3PathNamePrefix(vrgNamespaceName, vrgName string) string {
	return types.NamespacedName{Namespace: vrgNamespaceName, Name: vrgName}.String() + "/"
}

const vrgS3ObjectNameSuffix = "a"

func VrgObjectProtect(objectStorer ObjectStorer, vrg ramen.VolumeReplicationGroup) error {
	return uploadTypedObject(objectStorer, s3PathNamePrefix(vrg.Namespace, vrg.Name), vrgS3ObjectNameSuffix, vrg)
}

func VrgObjectUnprotect(objectStorer ObjectStorer, vrg ramen.VolumeReplicationGroup) error {
	return DeleteTypedObjects(objectStorer, s3PathNamePrefix(vrg.Namespace, vrg.Name), vrgS3ObjectNameSuffix, vrg)
}

func vrgObjectDownload(objectStorer ObjectStorer, pathName string, vrg *ramen.VolumeReplicationGroup) error {
	return downloadTypedObject(objectStorer, pathName, vrgS3ObjectNameSuffix, vrg)
}

func kubeObjectsCaptureInterval(kubeObjectProtectionSpec *ramen.KubeObjectProtectionSpec) time.Duration {
	if kubeObjectProtectionSpec.CaptureInterval == nil {
		return ramen.KubeObjectProtectionCaptureIntervalDefault
	}

	return kubeObjectProtectionSpec.CaptureInterval.Duration
}

func timeSincePreviousAndUntilNext(previousTime time.Time, interval time.Duration) (time.Duration, time.Duration) {
	since := time.Since(previousTime)

	return since, interval - since
}

func kubeObjectsCapturePathNameAndNamePrefix(namespaceName, vrgName string, captureNumber int64) (string, string) {
	const numberBase = 10
	number := strconv.FormatInt(captureNumber, numberBase)

	return s3PathNamePrefix(namespaceName, vrgName) + "kube-objects/" + number + "/",
		// TODO fix: may exceed name capacity
		namespaceName + "--" + vrgName + "--" + number
}

func kubeObjectsCaptureName(prefix, groupName, s3ProfileName string) string {
	return prefix + "--" + groupName + "--" + s3ProfileName
}

func kubeObjectsRecoverNamePrefix(vrgNamespaceName, vrgName string) string {
	return vrgNamespaceName + "--" + vrgName
}

func kubeObjectsRecoverName(prefix string, groupNumber int) string {
	return prefix + "--" + strconv.Itoa(groupNumber)
}

func (v *VRGInstance) kubeObjectsProtect(result *ctrl.Result) {
	s3StoreAccessors := v.s3StoreAccessorsGet()
	if len(s3StoreAccessors) == 0 {
		result.Requeue = true

		return
	}

	if !v.kubeObjectProtectionDisabled("capture") {
		v.kubeObjectsCaptureStartOrResumeOrDelay(result, s3StoreAccessors)
	}

	v.vrgObjectProtect(result, s3StoreAccessors)
}

type s3StoreAccessor struct {
	ObjectStorer
	profileName                 string
	url                         string
	bucketName                  string
	regionName                  string
	veleroNamespaceSecretKeyRef *corev1.SecretKeySelector
}

func (v *VRGInstance) s3StoreAccessorsGet() []s3StoreAccessor {
	s3StoreAccessors := make([]s3StoreAccessor, 0, len(v.instance.Spec.S3Profiles))

	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		if s3ProfileName == NoS3StoreAvailable {
			v.log.Info("Kube object protection store dummy")

			continue
		}

		objectStorer, s3StoreProfile, err := v.reconciler.ObjStoreGetter.ObjectStore(
			v.ctx,
			v.reconciler.APIReader,
			s3ProfileName,
			v.namespacedName,
			v.log,
		)
		if err != nil {
			v.log.Error(err, "Kube object protection store inaccessible", "name", s3ProfileName)

			return nil
		}

		s3StoreAccessors = append(s3StoreAccessors, s3StoreAccessor{
			objectStorer,
			s3ProfileName,
			s3StoreProfile.S3CompatibleEndpoint,
			s3StoreProfile.S3Bucket,
			s3StoreProfile.S3Region,
			s3StoreProfile.VeleroNamespaceSecretKeyRef,
		})
	}

	return s3StoreAccessors
}

func (v *VRGInstance) kubeObjectsCaptureStartOrResumeOrDelay(result *ctrl.Result, s3StoreAccessors []s3StoreAccessor) {
	veleroNamespaceName := v.veleroNamespaceName()
	vrg := v.instance
	interval := kubeObjectsCaptureInterval(vrg.Spec.KubeObjectProtection)
	status := &vrg.Status.KubeObjectProtection

	captureToRecoverFrom := status.CaptureToRecoverFrom
	if captureToRecoverFrom == nil {
		v.log.Info("Kube objects capture-to-recover-from nil")
		v.kubeObjectsCaptureStatusFalse(VRGConditionReasonUploading, "Kube objects initial capture in-progress")

		captureToRecoverFrom = &ramen.KubeObjectsCaptureIdentifier{}
	}

	number := 1 - captureToRecoverFrom.Number
	pathName, namePrefix := kubeObjectsCapturePathNameAndNamePrefix(vrg.Namespace, vrg.Name, number)
	labels := util.OwnerLabels(vrg.Namespace, vrg.Name)
	captureStartOrResume := func() {
		v.kubeObjectsCaptureStartOrResume(result, s3StoreAccessors, number, pathName, namePrefix,
			veleroNamespaceName, interval, labels)
	}

	requests, err := v.reconciler.kubeObjects.ProtectRequestsGet(
		v.ctx, v.reconciler.APIReader, veleroNamespaceName, labels)
	if err != nil {
		v.log.Error(err, "Kube objects capture in-progress query error", "number", number)
		v.kubeObjectsCaptureFailed(err.Error())

		result.Requeue = true

		return
	}

	if count := requests.Count(); count > 0 {
		v.log.Info("Kube objects capture resume", "number", number)
		captureStartOrResume()

		return
	}

	if _, delay := timeSincePreviousAndUntilNext(captureToRecoverFrom.StartTime.Time, interval); delay > 0 {
		v.log.Info("Kube objects capture start delay", "number", number, "delay", delay)
		delaySetIfLess(result, delay, v.log)

		return
	}

	if v.kubeObjectsCaptureDelete(result, s3StoreAccessors, number, pathName); result.Requeue {
		return
	}

	v.log.Info("Kube objects capture start", "number", number)
	captureStartOrResume()
}

func (v *VRGInstance) kubeObjectsCaptureDelete(
	result *ctrl.Result, s3StoreAccessors []s3StoreAccessor, captureNumber int64, pathName string,
) {
	pathName += v.reconciler.kubeObjects.ProtectsPath()

	// current s3 profiles may differ from those at capture time
	for _, s3StoreAccessor := range s3StoreAccessors {
		if err := s3StoreAccessor.ObjectStorer.DeleteObjects(pathName); err != nil {
			v.log.Error(err, "Kube objects capture s3 objects delete error",
				"number", captureNumber,
				"profile", s3StoreAccessor.profileName,
			)
			v.kubeObjectsCaptureFailed(err.Error())

			result.Requeue = true

			return
		}
	}
}

func (v *VRGInstance) kubeObjectsCaptureStartOrResume(
	result *ctrl.Result, s3StoreAccessors []s3StoreAccessor, captureNumber int64,
	pathName, namePrefix, veleroNamespaceName string, interval time.Duration, labels map[string]string,
) {
	vrg := v.instance
	groups := v.getCaptureGroups()
	requests := make([]kubeobjects.ProtectRequest, len(groups)*len(s3StoreAccessors))
	requestsProcessedCount := 0
	requestsCompletedCount := 0

	for groupNumber, captureGroup := range groups {
		for _, s3StoreAccessor := range s3StoreAccessors {
			request, err := v.reconciler.kubeObjects.ProtectRequestCreate(
				v.ctx, v.reconciler.Client, v.reconciler.APIReader, v.log,
				s3StoreAccessor.url,
				s3StoreAccessor.bucketName,
				s3StoreAccessor.regionName,
				pathName,
				s3StoreAccessor.veleroNamespaceSecretKeyRef,
				vrg.Namespace,
				captureGroup.Spec,
				veleroNamespaceName, kubeObjectsCaptureName(namePrefix, captureGroup.Name, s3StoreAccessor.profileName),
				labels)
			requests[requestsProcessedCount] = request
			requestsProcessedCount++

			if err == nil {
				v.log.Info("Kube objects group captured", "number", captureNumber,
					"group", groupNumber, "name", captureGroup.Name, "profile", s3StoreAccessor.profileName,
					"start", request.StartTime(), "end", request.EndTime())
				requestsCompletedCount++

				continue
			}

			if errors.Is(err, kubeobjects.RequestProcessingError{}) {
				v.log.Info("Kube objects group capturing", "number", captureNumber, "group", groupNumber,
					"name", captureGroup.Name, "profile", s3StoreAccessor.profileName, "state", err.Error())

				continue
			}

			v.log.Error(err, "Kube objects group capture error", "number", captureNumber,
				"group", groupNumber, "name", captureGroup.Name, "profile", s3StoreAccessor.profileName)
			v.kubeObjectsCaptureFailed(err.Error())

			result.Requeue = true

			return
		}

		if requestsCompletedCount < requestsProcessedCount {
			v.log.Info("Kube objects group capturing", "number", captureNumber, "group", groupNumber,
				"complete", requestsCompletedCount, "total", requestsProcessedCount)

			return
		}
	}

	v.kubeObjectsCaptureComplete(result, captureNumber, veleroNamespaceName, interval, labels, requests[0].StartTime())
}

func (v *VRGInstance) kubeObjectsCaptureComplete(
	result *ctrl.Result, captureNumber int64, veleroNamespaceName string, interval time.Duration,
	labels map[string]string, startTime metav1.Time,
) {
	vrg := v.instance
	status := &vrg.Status.KubeObjectProtection

	if err := v.reconciler.kubeObjects.ProtectRequestsDelete(
		v.ctx, v.reconciler.Client, veleroNamespaceName, labels,
	); err != nil {
		v.log.Error(err, "Kube objects capture requests delete error", "number", captureNumber)
		v.kubeObjectsCaptureFailed(err.Error())

		result.Requeue = true

		return
	}

	status.CaptureToRecoverFrom = &ramen.KubeObjectsCaptureIdentifier{
		Number: captureNumber, StartTime: startTime,
	}

	v.kubeObjectsCaptureStatus(metav1.ConditionTrue, VRGConditionReasonUploaded, clusterDataProtectedTrueMessage)

	duration, delay := timeSincePreviousAndUntilNext(status.CaptureToRecoverFrom.StartTime.Time, interval)
	if delay <= 0 {
		delay = time.Nanosecond
	}

	v.log.Info("Kube objects captured", "recovery point", status.CaptureToRecoverFrom,
		"duration", duration, "delay", delay)

	delaySetIfLess(result, delay, v.log)
}

func (v *VRGInstance) kubeObjectsCaptureFailed(message string) {
	v.kubeObjectsCaptureStatusFalse(VRGConditionReasonUploadError, message)
}

func (v *VRGInstance) kubeObjectsCaptureStatusFalse(reason, message string) {
	v.kubeObjectsCaptureStatus(metav1.ConditionFalse, reason, message)
}

func (v *VRGInstance) kubeObjectsCaptureStatus(status metav1.ConditionStatus, reason, message string) {
	v.kubeObjectsProtected = &metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Status:             status,
		ObservedGeneration: v.instance.Generation,
		Reason:             reason,
		Message:            message,
	}
}

const clusterDataProtectedTrueMessage = "Kube objects protected"

func (v *VRGInstance) vrgObjectProtect(result *ctrl.Result, s3StoreAccessors []s3StoreAccessor) {
	vrg := *v.instance

	for _, s3StoreAccessor := range s3StoreAccessors {
		if err := VrgObjectProtect(s3StoreAccessor.ObjectStorer, vrg); err != nil {
			util.ReportIfNotPresent(
				v.reconciler.eventRecorder,
				&vrg,
				corev1.EventTypeWarning,
				util.EventReasonVrgUploadFailed,
				err.Error(),
			)

			const message = "VRG Kube object protect error"

			v.log.Error(err, message, "profile", s3StoreAccessor.profileName)

			v.vrgObjectProtected = newVRGClusterDataUnprotectedCondition(v.instance.Generation, message)

			result.Requeue = true

			return
		}

		v.log.Info("VRG Kube object protected", "profile", s3StoreAccessor.profileName)

		v.vrgObjectProtected = newVRGClusterDataProtectedCondition(v.instance.Generation, clusterDataProtectedTrueMessage)
	}
}

func RecipeInfoExistsOnVRG(vrgInstance ramen.VolumeReplicationGroup) bool {
	return vrgInstance.Spec.KubeObjectProtection != nil &&
		vrgInstance.Spec.KubeObjectProtection.RecipeRef != nil &&
		vrgInstance.Spec.KubeObjectProtection.RecipeRef.Name != nil
}

func RecipeHasVolumeGroup(recipe *Recipe.Recipe) bool {
	return recipe != nil && recipe.Spec.Volumes != nil
}

func GetLabelSelectorFromRecipeVolumeGroupWithName(recipe *Recipe.Recipe) (metav1.LabelSelector, error) {
	labelSelector := &metav1.LabelSelector{} // init

	if recipe.Spec.Volumes == nil || recipe.Spec.Volumes.LabelSelector == nil {
		recipeInfo := fmt.Sprintf("Recipe Name '%s' in Namespace '%s'", recipe.Name, recipe.GetNamespace())

		return *labelSelector, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Volumes"}, recipeInfo)
	}

	return *recipe.Spec.Volumes.LabelSelector, nil
}

func (v *VRGInstance) getCaptureGroups() []kubeobjects.CaptureSpec {
	if RecipeInfoExistsOnVRG(*v.instance) {
		return v.getCaptureGroupsFromRecipe()
	}

	return []kubeobjects.CaptureSpec{{}}
}

func (v VRGInstance) getNameAndNamespaceString() string {
	return fmt.Sprintf("VRG Name: %s, Namespace: %s", v.instance.ObjectMeta.Name, v.instance.Namespace)
}

func (v *VRGInstance) getCaptureGroupsFromRecipe() []kubeobjects.CaptureSpec {
	recipe, err := GetRecipeWithName(
		v.ctx, v.reconciler.Client, *v.instance.Spec.KubeObjectProtection.RecipeRef.Name, v.instance.Namespace)
	if err != nil {
		v.log.Error(err, "failed to get Recipe from name.", "vrgInfo", v.getNameAndNamespaceString())
	}

	groups, err := v.getCaptureGroupsFromWorkflow(recipe, recipe.Spec.CaptureWorkflow)
	if err != nil {
		v.log.Error(err, "failed to get Capture Groups from Workflow.", "vrgInfo", v.getNameAndNamespaceString())
	}

	v.log.Info(fmt.Sprintf("successfully found Recipe Capture Groups for '%s'",
		v.getNameAndNamespaceString()))

	return groups
}

func (v *VRGInstance) getRecoverGroups() []kubeobjects.RecoverSpec {
	if RecipeInfoExistsOnVRG(*v.instance) {
		return v.getRecoverGroupsFromRecipe()
	}

	return []kubeobjects.RecoverSpec{{}}
}

func (v *VRGInstance) getRecoverGroupsFromRecipe() []kubeobjects.RecoverSpec {
	recipe, err := GetRecipeWithName(
		v.ctx, v.reconciler.Client, *v.instance.Spec.KubeObjectProtection.RecipeRef.Name, v.instance.Namespace)
	if err != nil {
		v.log.Error(err, "failed to get Recipe from name.", "vrgInfo", v.getNameAndNamespaceString())
	}

	groups, err := v.getRestoreGroupsFromWorkflow(recipe, recipe.Spec.RecoverWorkflow)
	if err != nil {
		v.log.Error(err, "failed to get Restore Groups from Workflow.", "vrgInfo",
			v.getNameAndNamespaceString())
	}

	v.log.Info(fmt.Sprintf("getRecoverGroupsFromRecipe() successfully found groups for recover spec. '%s'",
		v.getNameAndNamespaceString()))

	return groups
}

func (v *VRGInstance) kubeObjectsRecover(result *ctrl.Result,
	s3ProfileName string, s3StoreProfile ramen.S3StoreProfile, objectStorer ObjectStorer,
) error {
	vrg := v.instance

	if v.kubeObjectProtectionDisabled("recovery") {
		return nil
	}

	sourceVrgNamespaceName := vrg.Namespace
	sourceVrgName := vrg.Name
	sourcePathNamePrefix := s3PathNamePrefix(sourceVrgNamespaceName, sourceVrgName)

	sourceVrg := &ramen.VolumeReplicationGroup{}
	if err := vrgObjectDownload(objectStorer, sourcePathNamePrefix, sourceVrg); err != nil {
		v.log.Error(err, "Kube objects capture-to-recover-from identifier get error")

		return nil
	}

	capture := sourceVrg.Status.KubeObjectProtection.CaptureToRecoverFrom
	if capture == nil {
		v.log.Info("Kube objects capture-to-recover-from identifier nil")

		return nil
	}

	vrg.Status.KubeObjectProtection.CaptureToRecoverFrom = capture

	return v.kubeObjectsRecoveryStartOrResume(
		result,
		s3StoreAccessor{
			objectStorer,
			s3ProfileName,
			s3StoreProfile.S3CompatibleEndpoint,
			s3StoreProfile.S3Bucket,
			s3StoreProfile.S3Region,
			s3StoreProfile.VeleroNamespaceSecretKeyRef,
		},
		sourceVrgNamespaceName,
		sourceVrgName,
		capture,
	)
}

func (v *VRGInstance) createRecoverOrProtectRequest(
	s3StoreAccessor s3StoreAccessor,
	sourceVrgNamespaceName, sourceVrgName string,
	capture *ramen.KubeObjectsCaptureIdentifier,
	groupNumber int,
	recoverGroup kubeobjects.RecoverSpec,
	veleroNamespaceName string,
	labels map[string]string,
) (kubeobjects.Request, error) {
	vrg := v.instance
	capturePathName, captureNamePrefix := kubeObjectsCapturePathNameAndNamePrefix(
		sourceVrgNamespaceName, sourceVrgName, capture.Number)
	recoverNamePrefix := kubeObjectsRecoverNamePrefix(vrg.Namespace, vrg.Name)

	var request kubeobjects.Request

	var err error

	if recoverGroup.BackupName == ramen.ReservedBackupName {
		status := &vrg.Status.KubeObjectProtection
		captureToRecoverFrom := status.CaptureToRecoverFrom
		backupSequenceNumber := 1 - captureToRecoverFrom.Number // is this a good way to do this?
		backupName := fmt.Sprintf("%s-restore-%d", recoverGroup.BackupName, groupNumber)
		v.log.Info(fmt.Sprintf("backup: %s, captureToRecoverFrom: %d", backupName, captureToRecoverFrom.Number))

		pathName, namePrefix := kubeObjectsCapturePathNameAndNamePrefix(vrg.Namespace, vrg.Name, backupSequenceNumber)
		request, err = v.reconciler.kubeObjects.ProtectRequestCreate(
			v.ctx, v.reconciler.Client, v.reconciler.APIReader, v.log,
			s3StoreAccessor.url,
			s3StoreAccessor.bucketName,
			s3StoreAccessor.regionName,
			pathName,
			s3StoreAccessor.veleroNamespaceSecretKeyRef,
			vrg.Namespace,
			recoverGroup.Spec,
			veleroNamespaceName, kubeObjectsCaptureName(namePrefix, backupName, s3StoreAccessor.profileName),
			labels)
	} else {
		request, err = v.reconciler.kubeObjects.RecoverRequestCreate(
			v.ctx, v.reconciler.Client, v.reconciler.APIReader, v.log,
			s3StoreAccessor.url,
			s3StoreAccessor.bucketName,
			s3StoreAccessor.regionName,
			capturePathName,
			s3StoreAccessor.veleroNamespaceSecretKeyRef,
			sourceVrgNamespaceName, vrg.Namespace, recoverGroup, veleroNamespaceName,
			kubeObjectsCaptureName(captureNamePrefix, recoverGroup.BackupName, s3StoreAccessor.profileName),
			kubeObjectsRecoverName(recoverNamePrefix, groupNumber), labels)
	}

	return request, err
}

func (v *VRGInstance) kubeObjectsRecoveryStartOrResume(
	result *ctrl.Result, s3StoreAccessor s3StoreAccessor,
	sourceVrgNamespaceName, sourceVrgName string,
	capture *ramen.KubeObjectsCaptureIdentifier,
) error {
	vrg := v.instance

	veleroNamespaceName := v.veleroNamespaceName()
	labels := util.OwnerLabels(vrg.Namespace, vrg.Name)
	groups := v.getRecoverGroups()
	requests := make([]kubeobjects.Request, len(groups)) // Request: interface for ProtectRequest, RecoverRequest

	for groupNumber, recoverGroup := range groups {
		var request kubeobjects.Request

		var err error

		request, err = v.createRecoverOrProtectRequest(s3StoreAccessor, sourceVrgNamespaceName, sourceVrgName,
			capture, groupNumber, recoverGroup, veleroNamespaceName, labels)
		requests[groupNumber] = request

		if err == nil {
			v.log.Info("Kube objects group recovered", "number", capture.Number,
				"group", groupNumber, "name", recoverGroup.BackupName, "profile", s3StoreAccessor.profileName,
				"start", request.StartTime(), "end", request.EndTime())

			continue
		}

		if errors.Is(err, kubeobjects.RequestProcessingError{}) {
			v.log.Info("Kube objects group recovering", "number", capture.Number,
				"group", groupNumber, "name", recoverGroup.BackupName, "profile", s3StoreAccessor.profileName,
				"state", err.Error())

			return err
		}

		v.log.Error(err, "Kube objects group recover error", "number", capture.Number,
			"group", groupNumber, "name", recoverGroup.BackupName, "profile", s3StoreAccessor.profileName)

		result.Requeue = true

		return err
	}

	startTime := requests[0].StartTime()
	duration := time.Since(startTime.Time)
	v.log.Info("Kube objects recovered", "number", capture.Number, "profile", s3StoreAccessor.profileName,
		"groups", len(groups), "start", startTime, "duration", duration)

	return v.kubeObjectsRecoverRequestsDelete(result, veleroNamespaceName, labels)
}

func (v *VRGInstance) kubeObjectsRecoverRequestsDelete(
	result *ctrl.Result, veleroNamespaceName string, labels map[string]string,
) error {
	if err := v.reconciler.kubeObjects.RecoverRequestsDelete(
		v.ctx, v.reconciler.Client, veleroNamespaceName, labels,
	); err != nil {
		v.log.Error(err, "Kube objects recover requests delete error")

		result.Requeue = true

		return err
	}

	v.log.Info("Kube objects recover requests deleted")

	return nil
}

func (v *VRGInstance) veleroNamespaceName() string {
	if v.ramenConfig.KubeObjectProtection.VeleroNamespaceName != "" {
		return v.ramenConfig.KubeObjectProtection.VeleroNamespaceName
	}

	return VeleroNamespaceNameDefault
}

func (v *VRGInstance) kubeObjectProtectionDisabled(caller string) bool {
	vrgDisabled := v.instance.Spec.KubeObjectProtection == nil
	cmDisabled := v.ramenConfig.KubeObjectProtection.Disabled
	disabled := vrgDisabled || cmDisabled

	v.log.Info("Kube object protection", "disabled", disabled, "VRG", vrgDisabled, "configMap", cmDisabled, "for", caller)

	return disabled
}

func (v *VRGInstance) kubeObjectsProtectionDelete(result *ctrl.Result) error {
	if v.kubeObjectProtectionDisabled("deletion") {
		return nil
	}

	vrg := v.instance

	return v.kubeObjectsRecoverRequestsDelete(
		result,
		v.veleroNamespaceName(),
		util.OwnerLabels(vrg.Namespace, vrg.Name),
	)
}

func kubeObjectsRequestsWatch(b *builder.Builder, kubeObjects kubeobjects.RequestsManager) *builder.Builder {
	watch := func(request kubeobjects.Request) {
		src := &source.Kind{Type: request.Object()}
		b.Watches(
			src,
			handler.EnqueueRequestsFromMapFunc(func(o client.Object) []reconcile.Request {
				labels := o.GetLabels()
				log := func(s string) {
					ctrl.Log.WithName("VolumeReplicationGroup").Info(
						"Kube objects request updated; "+s,
						"kind", o.GetObjectKind(),
						"name", o.GetNamespace()+"/"+o.GetName(),
						"created", o.GetCreationTimestamp(),
						"gen", o.GetGeneration(),
						"ver", o.GetResourceVersion(),
						"labels", labels,
					)
				}

				if ownerNamespaceName, ownerName, ok := util.OwnerNamespaceNameAndName(labels); ok {
					log("owner labels found, enqueue VRG reconcile")

					return []reconcile.Request{
						{NamespacedName: types.NamespacedName{Namespace: ownerNamespaceName, Name: ownerName}},
					}
				}

				log("owner labels not found")

				return []reconcile.Request{}
			}),
			builder.WithPredicates(ResourceVersionUpdatePredicate{}),
		)
	}

	watch(kubeObjects.ProtectRequestNew())
	watch(kubeObjects.RecoverRequestNew())

	return b
}

func GetRecipeWithName(ctx context.Context, client client.Client, name, namespace string) (Recipe.Recipe, error) {
	recipe := &Recipe.Recipe{}

	err := client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, recipe)

	return *recipe, err
}

func (v *VRGInstance) getCaptureGroupsFromWorkflow(
	recipe Recipe.Recipe, workflow *Recipe.Workflow) ([]kubeobjects.CaptureSpec, error,
) {
	if workflow == nil {
		return []kubeobjects.CaptureSpec{{}}, nil
	}

	resources := make([]kubeobjects.CaptureSpec, len(workflow.Sequence))

	for index, resource := range workflow.Sequence {
		for resourceType := range resource {
			resourceName := resource[resourceType]

			captureInstance, err := getResourceAndConvertToCaptureGroup(recipe, resourceType, resourceName)
			if err != nil {
				return resources, err
			}

			resources[index] = *captureInstance
		}
	}

	return resources, nil
}

func (v *VRGInstance) getRestoreGroupsFromWorkflow(
	recipe Recipe.Recipe, workflow *Recipe.Workflow) ([]kubeobjects.RecoverSpec, error,
) {
	if workflow == nil {
		return []kubeobjects.RecoverSpec{{}}, nil
	}

	resources := make([]kubeobjects.RecoverSpec, len(workflow.Sequence))

	for index, resource := range workflow.Sequence {
		// group: map[string]string, e.g. "group": "groupName", or "hook": "hookName"
		for resourceType := range resource {
			resourceName := resource[resourceType]

			captureInstance, err := getResourceAndConvertToRecoverGroup(recipe, resourceType, resourceName)
			if err != nil {
				return resources, err
			}

			resources[index] = *captureInstance
		}
	}

	return resources, nil
}

func getResourceAndConvertToCaptureGroup(
	recipe Recipe.Recipe, resourceType, name string) (*kubeobjects.CaptureSpec, error,
) {
	// check hooks OR groups
	if resourceType == "group" {
		for _, group := range recipe.Spec.Groups {
			if group.Name == name {
				return convertRecipeGroupToCaptureSpec(*group)
			}
		}

		return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Group.Name"}, name)
	}

	if resourceType == "hook" {
		hook, op, err := getHookAndOpFromRecipe(&recipe, name)
		if err != nil {
			return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
		}

		return convertRecipeHookToCaptureSpec(*hook, *op)
	}

	return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
}

// resource: could be Group or Hook
func getResourceAndConvertToRecoverGroup(
	recipe Recipe.Recipe, resourceType, name string) (*kubeobjects.RecoverSpec, error,
) {
	if resourceType == "group" {
		for _, group := range recipe.Spec.Groups {
			if group.Name == name {
				return convertRecipeGroupToRecoverSpec(*group)
			}
		}

		return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Group.Name"}, name)
	}

	if resourceType == "hook" {
		hook, op, err := getHookAndOpFromRecipe(&recipe, name)
		if err != nil {
			return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
		}

		return convertRecipeHookToRecoverSpec(*hook, *op)
	}

	return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
}

func getHookAndOpFromRecipe(recipe *Recipe.Recipe, name string) (*Recipe.Hook, *Recipe.Operation, error) {
	// hook can be made up of optionalPrefix/hookName; workflow sequence uses full name
	var prefix string

	var suffix string

	const containsSingleDelim = 2

	parts := strings.Split(name, "/")
	switch len(parts) {
	case 1:
		prefix = ""
		suffix = name
	case containsSingleDelim:
		prefix = parts[0]
		suffix = parts[1]
	default:
		return nil, nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Hook.Name"}, name)
	}

	// match prefix THEN suffix
	for _, hook := range recipe.Spec.Hooks {
		if hook.Name == prefix {
			for _, op := range hook.Ops {
				if op.Name == suffix {
					return hook, op, nil
				}
			}
		}
	}

	return nil, nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Hook.Name"}, name)
}

// TODO: complete functionality - add Hook support to KubeResourcesSpec, then copy in Velero object creation
func convertRecipeHookToCaptureSpec(
	hook Recipe.Hook, op Recipe.Operation) (*kubeobjects.CaptureSpec, error,
) {
	hookName := hook.Name + "-" + op.Name

	hooks := getHookSpecFromHook(hook, op)

	captureSpec := kubeobjects.CaptureSpec{
		Name: hookName,
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedResources: []string{"pod"},
				ExcludedResources: []string{},
				Hooks:             hooks,
			},
			LabelSelector:           &hooks[0].LabelSelector,
			IncludeClusterResources: new(bool),
		},
	}

	return &captureSpec, nil
}

func convertRecipeHookToRecoverSpec(hook Recipe.Hook, op Recipe.Operation) (*kubeobjects.RecoverSpec, error) {
	hooks := getHookSpecFromHook(hook, op)

	return &kubeobjects.RecoverSpec{
		// BackupName: arbitrary fixed string to designate that this is will be a Backup, not Restore, object
		BackupName: ramen.ReservedBackupName,
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedResources: []string{"pod"},
				ExcludedResources: []string{},
				Hooks:             hooks,
			},
			LabelSelector:           &hooks[0].LabelSelector,
			IncludeClusterResources: new(bool),
		},
	}, nil
}

func getHookSpecFromHook(hook Recipe.Hook, op Recipe.Operation) []kubeobjects.HookSpec {
	return []kubeobjects.HookSpec{
		{
			Name:          op.Name,
			Type:          hook.Type,
			Timeout:       *op.Timeout,
			Container:     op.Container,
			Command:       op.Command,
			LabelSelector: *hook.LabelSelector,
		},
	}
}

func convertRecipeGroupToRecoverSpec(group Recipe.Group) (*kubeobjects.RecoverSpec, error) {
	return &kubeobjects.RecoverSpec{
		BackupName: group.BackupRef,
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedResources: group.IncludedResourceTypes,
				ExcludedResources: group.ExcludedResourceTypes,
			},
			LabelSelector:           group.LabelSelector,
			OrLabelSelectors:        []*metav1.LabelSelector{},
			IncludeClusterResources: group.IncludeClusterResources,
		},
	}, nil
}

func convertRecipeGroupToCaptureSpec(group Recipe.Group) (*kubeobjects.CaptureSpec, error) {
	captureSpec := kubeobjects.CaptureSpec{
		Name: group.Name,
		// TODO: add backupRef/backupName here?
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedResources: group.IncludedResourceTypes,
				ExcludedResources: group.ExcludedResourceTypes,
			},
			LabelSelector:           group.LabelSelector,
			OrLabelSelectors:        []*metav1.LabelSelector{},
			IncludeClusterResources: group.IncludeClusterResources,
		},
	}

	return &captureSpec, nil
}
