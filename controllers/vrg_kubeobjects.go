/*
Copyright 2022 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"errors"
	"strconv"
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
	number := strconv.FormatInt(captureNumber, 10)

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
				captureGroup.KubeObjectsSpec,
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

	if err := v.reconciler.kubeObjects.ProtectRequestsDelete(v.ctx, v.reconciler.Client, veleroNamespaceName, labels,
	); err != nil {
		v.log.Error(err, "Kube objects capture requests delete error", "number", captureNumber)
		v.kubeObjectsCaptureFailed(err.Error())

		result.Requeue = true

		return
	}

	status.CaptureToRecoverFrom = &ramen.KubeObjectsCaptureIdentifier{
		Number: captureNumber, StartTime: startTime,
	}

	v.kubeObjectsProtected = newVRGClusterDataProtectedCondition(v.instance.Generation, clusterDataProtectedTrueMessage)

	duration, delay := timeSincePreviousAndUntilNext(status.CaptureToRecoverFrom.StartTime.Time, interval)
	if delay <= 0 {
		delay = time.Nanosecond
	}

	v.log.Info("Kube objects captured", "recovery point", status.CaptureToRecoverFrom,
		"duration", duration, "delay", delay)

	delaySetIfLess(result, delay, v.log)
}

func (v *VRGInstance) kubeObjectsCaptureFailed(message string) {
	if v.instance.Status.KubeObjectProtection.CaptureToRecoverFrom != nil {
		// TODO && time.Since(CaptureToRecoverFrom.StartTime) < Spec.KubeObjectProtection.CaptureInterval * 2 or 3
		return
	}

	v.kubeObjectsProtected = newVRGClusterDataUnprotectedCondition(v.instance.Generation, message)
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

func (v *VRGInstance) getCaptureGroups() []ramen.KubeObjectsCaptureSpec {
	if v.instance.Spec.KubeObjectProtection.CaptureOrder != nil {
		return v.instance.Spec.KubeObjectProtection.CaptureOrder
	}

	return []ramen.KubeObjectsCaptureSpec{{}}
}

func (v *VRGInstance) getRecoverGroups() []ramen.KubeObjectsRecoverSpec {
	if v.instance.Spec.KubeObjectProtection.RecoverOrder != nil {
		return v.instance.Spec.KubeObjectProtection.RecoverOrder
	}

	return []ramen.KubeObjectsRecoverSpec{{}}
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

func (v *VRGInstance) kubeObjectsRecoveryStartOrResume(
	result *ctrl.Result, s3StoreAccessor s3StoreAccessor,
	sourceVrgNamespaceName, sourceVrgName string,
	capture *ramen.KubeObjectsCaptureIdentifier,
) error {
	vrg := v.instance
	capturePathName, captureNamePrefix := kubeObjectsCapturePathNameAndNamePrefix(
		sourceVrgNamespaceName, sourceVrgName, capture.Number)
	recoverNamePrefix := kubeObjectsRecoverNamePrefix(vrg.Namespace, vrg.Name)
	veleroNamespaceName := v.veleroNamespaceName()
	labels := util.OwnerLabels(vrg.Namespace, vrg.Name)
	groups := v.getRecoverGroups()
	requests := make([]kubeobjects.ProtectRequest, len(groups))

	for groupNumber, recoverGroup := range groups {
		request, err := v.reconciler.kubeObjects.RecoverRequestCreate(
			v.ctx, v.reconciler.Client, v.reconciler.APIReader, v.log,
			s3StoreAccessor.url,
			s3StoreAccessor.bucketName,
			s3StoreAccessor.regionName,
			capturePathName,
			s3StoreAccessor.veleroNamespaceSecretKeyRef,
			sourceVrgNamespaceName, vrg.Namespace, recoverGroup.KubeObjectsSpec, veleroNamespaceName,
			kubeObjectsCaptureName(captureNamePrefix, recoverGroup.BackupName, s3StoreAccessor.profileName),
			kubeObjectsRecoverName(recoverNamePrefix, groupNumber), labels)
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
	if err := v.reconciler.kubeObjects.RecoverRequestsDelete(v.ctx, v.reconciler.Client, veleroNamespaceName, labels,
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
