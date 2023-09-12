// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var vrgLastUploadTime = map[string]metav1.Time{}

func (v *VRGInstance) vrgObjectProtect(result *ctrl.Result, s3StoreAccessors []s3StoreAccessor) {
	log := v.log

	if lastUploadTime, ok := vrgLastUploadTime[v.namespacedName]; ok {
		const maxVRGProtectionInterval = time.Minute

		// Throttle VRG protection if this call is more recent than maxVRGProtectionInterval.
		if shouldThrottleVRGProtection(lastUploadTime, maxVRGProtectionInterval) {
			log.Info("VRG already protected recently. Throttling...")

			return
		}
	}

	v.vrgObjectProtectThrottled(result, s3StoreAccessors, func() {}, func() {})
}

func (v *VRGInstance) vrgObjectProtectThrottled(result *ctrl.Result, s3StoreAccessors []s3StoreAccessor,
	success, failure func(),
) {
	vrg := v.instance
	eventReporter := v.reconciler.eventRecorder
	log := v.log

	for _, s3StoreAccessor := range s3StoreAccessors {
		log1 := log.WithValues("profile", s3StoreAccessor.S3ProfileName)

		if err := VrgObjectProtect(s3StoreAccessor.ObjectStorer, *vrg); err != nil {
			util.ReportIfNotPresent(
				eventReporter, vrg, corev1.EventTypeWarning, util.EventReasonVrgUploadFailed, err.Error(),
			)

			const message = "VRG Kube object protect error"

			log1.Error(err, message)

			v.vrgObjectProtected = newVRGClusterDataUnprotectedCondition(vrg.Generation, message)
			result.Requeue = true

			failure()

			return
		}

		log1.Info("VRG Kube object protected")

		vrgLastUploadTime[v.namespacedName] = metav1.Now()

		v.vrgObjectProtected = newVRGClusterDataProtectedCondition(vrg.Generation, clusterDataProtectedTrueMessage)
	}

	success()
}

func shouldThrottleVRGProtection(lastUploadTime metav1.Time, maxVRGProtectionTime time.Duration) bool {
	// Throttle VRG protection if this call is more recent than MaxVRGProtectionTime.
	return time.Now().Before(lastUploadTime.Add(maxVRGProtectionTime))
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
