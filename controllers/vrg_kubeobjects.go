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
	"strconv"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
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
	objectStorers := v.objectStorersGet()
	if objectStorers == nil {
		result.Requeue = true

		return
	}

	if v.kubeObjectProtectionDisabled() {
		v.log.Info("Kube objects protection disabled")

		return
	}

	if v.instance.Spec.KubeObjectProtection != nil {
		v.kubeObjectsCaptureStartOrResumeOrDelay(result, objectStorers)
	}

	v.vrgObjectProtect(result, objectStorers)
}

func (v *VRGInstance) objectStorersGet() []ObjectStorer {
	objectStorers := make([]ObjectStorer, len(v.instance.Spec.S3Profiles))

	for i, s3ProfileName := range v.instance.Spec.S3Profiles {
		var err error

		objectStorers[i], err = v.reconciler.ObjStoreGetter.ObjectStore(
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
	}

	return objectStorers
}

const kubeObjectsRequestNameLabelKey = "ramendr.openshift.io/request-name"

func (v *VRGInstance) kubeObjectsCaptureStartOrResumeOrDelay(result *ctrl.Result, objectStorers []ObjectStorer) {
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
	captureStartOrResume := func() {
		v.kubeObjectsCaptureStartOrResume(result, objectStorers, number, pathName, namePrefix,
			veleroNamespaceName, interval)
	}

	requests, err := KubeObjectsCaptureRequestsGet(v.ctx, v.reconciler.APIReader,
		veleroNamespaceName, kubeObjectsRequestNameLabelKey, namePrefix,
	)
	if err != nil {
		v.log.Error(err, "Kube objects capture in-progress query error", "number", number)

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

	if v.kubeObjectsCaptureDelete(result, objectStorers, number, pathName); result.Requeue {
		return
	}

	v.log.Info("Kube objects capture start", "number", number)
	captureStartOrResume()
}

func (v *VRGInstance) kubeObjectsCaptureDelete(
	result *ctrl.Result, objectStorers []ObjectStorer, captureNumber int64, pathName string,
) {
	vrg := v.instance
	pathName += KubeObjectsCapturesPath

	// current s3 profiles may differ from those at capture time
	for i, s3ProfileName := range vrg.Spec.S3Profiles {
		objectStore := objectStorers[i]
		if err := objectStore.DeleteObjects(pathName); err != nil {
			v.log.Error(err, "Kube objects capture s3 objects delete failed", "number", captureNumber, "profile", s3ProfileName)

			result.Requeue = true

			return
		}
	}
}

func (v *VRGInstance) kubeObjectsCaptureStartOrResume(
	result *ctrl.Result, objectStorers []ObjectStorer, captureNumber int64,
	pathName, namePrefix, veleroNamespaceName string, interval time.Duration,
) {
	vrg := v.instance
	status := &vrg.Status.KubeObjectProtection
	groups := v.getCaptureGroups()
	requests := make([]KubeObjectsProtectRequest, len(vrg.Spec.S3Profiles)*len(groups))
	requestNumber := 0

	for i, s3ProfileName := range vrg.Spec.S3Profiles {
		objectStore := objectStorers[i]

		for groupNumber, captureGroup := range groups {
			request, err := KubeObjectsProtect(
				v.ctx, v.reconciler.Client, v.reconciler.APIReader, v.log,
				objectStore.AddressComponent1(), objectStore.AddressComponent2(), pathName, vrg.Namespace,
				captureGroup.KubeObjectsSpec,
				veleroNamespaceName, kubeObjectsCaptureName(namePrefix, captureGroup.Name, s3ProfileName),
				kubeObjectsRequestNameLabelKey, namePrefix, v.getVeleroSecretName())
			if err != nil {
				v.log.Error(err, "Kube objects group capture error", "number", captureNumber,
					"group", groupNumber, "name", captureGroup.Name, "profile", s3ProfileName)

				result.Requeue = true

				return
			}

			v.log.Info("Kube objects group captured", "number", captureNumber,
				"group", groupNumber, "name", captureGroup.Name, "profile", s3ProfileName,
				"start", request.StartTime(), "end", request.EndTime())

			requests[requestNumber] = request
			requestNumber++
		}
	}

	if err := KubeObjectsCaptureRequestsDelete(v.ctx, v.reconciler.Client, veleroNamespaceName,
		kubeObjectsRequestNameLabelKey, namePrefix); err != nil {
		v.log.Error(err, "Kube objects capture requests delete failed", "number", captureNumber)

		result.Requeue = true

		return
	}

	status.CaptureToRecoverFrom = &ramen.KubeObjectsCaptureIdentifier{
		Number: captureNumber, StartTime: requests[0].StartTime(),
	}

	duration, delay := timeSincePreviousAndUntilNext(status.CaptureToRecoverFrom.StartTime.Time, interval)
	if delay <= 0 {
		delay = time.Nanosecond
	}

	v.log.Info("Kube objects captured", "recovery point", status.CaptureToRecoverFrom,
		"duration", duration, "delay", delay)

	delaySetIfLess(result, delay, v.log)
}

func (v *VRGInstance) vrgObjectProtect(result *ctrl.Result, objectStorers []ObjectStorer) {
	vrg := *v.instance
	v.vrgObjectProtected = metav1.ConditionTrue

	for i, s3ProfileName := range vrg.Spec.S3Profiles {
		objectStore := objectStorers[i]

		if err := VrgObjectProtect(objectStore, vrg); err != nil {
			result.Requeue = true
			v.vrgObjectProtected = metav1.ConditionFalse
			util.ReportIfNotPresent(
				v.reconciler.eventRecorder,
				&vrg,
				corev1.EventTypeWarning,
				util.EventReasonVrgUploadFailed,
				err.Error(),
			)
			v.log.Error(err, "Vrg Kube object protect failed", "profile", s3ProfileName)

			return
		}

		v.log.Info("Vrg Kube object protected", "profile", s3ProfileName)
	}
}

func (v *VRGInstance) getCaptureGroups() []ramen.KubeObjectsCaptureGroup {
	if v.instance.Spec.KubeObjectProtection.CaptureOrder != nil {
		return v.instance.Spec.KubeObjectProtection.CaptureOrder
	}

	return []ramen.KubeObjectsCaptureGroup{{}}
}

func (v *VRGInstance) getRecoverGroups() []ramen.KubeObjectsRecoverGroup {
	if v.instance.Spec.KubeObjectProtection.RecoverOrder != nil {
		return v.instance.Spec.KubeObjectProtection.RecoverOrder
	}

	return []ramen.KubeObjectsRecoverGroup{{}}
}

func (v *VRGInstance) kubeObjectsRecover(s3ProfileName string, objectStore ObjectStorer) error {
	vrg := v.instance

	spec := vrg.Spec.KubeObjectProtection
	if spec == nil || v.kubeObjectProtectionDisabled() {
		v.log.Info("Kube objects recovery disabled")

		return nil
	}

	sourceVrgNamespaceName := vrg.Namespace
	sourceVrgName := vrg.Name
	sourcePathNamePrefix := s3PathNamePrefix(sourceVrgNamespaceName, sourceVrgName)

	sourceVrg := &ramen.VolumeReplicationGroup{}
	if err := vrgObjectDownload(objectStore, sourcePathNamePrefix, sourceVrg); err != nil {
		v.log.Error(err, "Kube objects capture-to-recover-from identifier get failed")

		return nil
	}

	capture := sourceVrg.Status.KubeObjectProtection.CaptureToRecoverFrom
	if capture == nil {
		v.log.Info("Kube objects capture-to-recover-from identifier nil")

		return nil
	}

	vrg.Status.KubeObjectProtection.CaptureToRecoverFrom = capture
	capturePathName, captureNamePrefix := kubeObjectsCapturePathNameAndNamePrefix(
		sourceVrgNamespaceName, sourceVrgName, capture.Number)
	recoverNamePrefix := kubeObjectsRecoverNamePrefix(vrg.Namespace, vrg.Name)
	veleroNamespaceName := v.veleroNamespaceName()

	for groupNumber, recoverGroup := range v.getRecoverGroups() {
		request, err := KubeObjectsRecover(
			v.ctx, v.reconciler.Client, v.reconciler.APIReader, v.log,
			objectStore.AddressComponent1(), objectStore.AddressComponent2(), capturePathName,
			sourceVrgNamespaceName, vrg.Namespace,
			recoverGroup.KubeObjectsSpec,
			veleroNamespaceName,
			kubeObjectsCaptureName(captureNamePrefix, recoverGroup.BackupName, s3ProfileName),
			kubeObjectsRecoverName(recoverNamePrefix, groupNumber),
			kubeObjectsRequestNameLabelKey, captureNamePrefix, recoverNamePrefix, v.getVeleroSecretName(),
		)
		if err != nil {
			v.log.Error(err, "Kube objects group recover error", "number", capture.Number,
				"group", groupNumber, "name", recoverGroup.BackupName, "profile", s3ProfileName,
			)

			return err
		}

		v.log.Info("Kube objects group recovered", "number", capture.Number,
			"group", groupNumber, "name", recoverGroup.BackupName, "profile", s3ProfileName,
			"start", request.StartTime(), "end", request.EndTime(),
		)
	}

	return KubeObjectsRecoverRequestsDelete(v.ctx, v.reconciler.Client, veleroNamespaceName,
		kubeObjectsRequestNameLabelKey, recoverNamePrefix)
}

func (v *VRGInstance) veleroNamespaceName() string {
	veleroNamespaceName := VeleroNamespaceNameDefault

	_, ramenConfig, err := ConfigMapGet(v.ctx, v.reconciler.APIReader)
	if err != nil {
		v.log.Error(err, "veleroNamespaceName config failed")

		return veleroNamespaceName
	}

	if ramenConfig.VeleroNamespaceName != "" {
		veleroNamespaceName = ramenConfig.VeleroNamespaceName
	}

	return veleroNamespaceName
}

// return true = disabled
func (v *VRGInstance) kubeObjectProtectionDisabled() bool {
	const defaultState = false

	_, ramenConfig, err := ConfigMapGet(v.ctx, v.reconciler.APIReader)
	if err != nil {
		return defaultState
	}

	if ramenConfig.KubeObjectProtection != nil && ramenConfig.KubeObjectProtection.Disabled != nil {
		return *ramenConfig.KubeObjectProtection.Disabled
	}

	return defaultState
}

func (v *VRGInstance) getVeleroSecretName() string {
	name := secretNameDefault

	_, ramenConfig, err := ConfigMapGet(v.ctx, v.reconciler.APIReader)
	if err != nil {
		v.log.Error(err, "getVeleroSecretName failed getting Ramen ConfigMap")

		return name
	}

	if ramenConfig.KubeObjectProtection != nil && ramenConfig.KubeObjectProtection.VeleroSecretName != "" {
		name = ramenConfig.KubeObjectProtection.VeleroSecretName
	}

	return name
}

func (v *VRGInstance) kubeObjectProtectionDisabledOrKubeObjectsProtected() bool {
	return v.kubeObjectProtectionDisabled() ||
		v.instance.Spec.KubeObjectProtection == nil ||
		v.instance.Status.KubeObjectProtection.CaptureToRecoverFrom != nil
}
