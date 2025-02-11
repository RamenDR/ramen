// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var vrgLastUploadVersion = map[string]string{}

func (v *VRGInstance) vrgObjectProtect(result *ctrl.Result) {
	vrg := v.instance
	log := v.log

	if lastUploadVersion, ok := vrgLastUploadVersion[v.namespacedName]; ok {
		if vrg.ResourceVersion == lastUploadVersion {
			log.Info("VRG resource version unchanged, skip S3 upload", "version", vrg.ResourceVersion)

			return
		}
	}

	v.vrgObjectProtectThrottled(result, func() {}, func() {})
}

func (v *VRGInstance) vrgObjectProtectThrottled(result *ctrl.Result,
	success, failure func(),
) {
	vrg := v.instance
	eventReporter := v.reconciler.eventRecorder
	log := v.log

	for _, s3StoreAccessor := range v.s3StoreAccessors {
		log1 := log.WithValues("profile", s3StoreAccessor.S3ProfileName)

		if err := VrgObjectProtect(s3StoreAccessor.ObjectStorer, *vrg); err != nil {
			util.ReportIfNotPresent(
				eventReporter, vrg, corev1.EventTypeWarning, util.EventReasonVrgUploadFailed, err.Error(),
			)

			const message = "VRG Kube object protect error"

			log1.Error(err, message)

			v.vrgObjectProtected = newVRGClusterDataUnprotectedCondition(vrg.Generation,
				"VolumeReplicationGroupObjectCaptureError", message)
			result.Requeue = true

			failure()

			return
		}

		log1.Info("VRG Kube object protected")

		vrgLastUploadVersion[v.namespacedName] = vrg.ResourceVersion
		v.vrgObjectProtected = newVRGClusterDataProtectedCondition(vrg.Generation, vrgClusterDataProtectedTrueMessage)
	}

	success()
}

const vrgS3ObjectNameSuffix = "a"

func VrgObjectProtect(objectStorer ObjectStorer, vrg ramen.VolumeReplicationGroup) error {
	return uploadTypedObject(objectStorer, s3PathNamePrefix(vrg.Namespace, vrg.Name), vrgS3ObjectNameSuffix, vrg)
}

func VrgObjectUnprotect(objectStorer ObjectStorer, vrg ramen.VolumeReplicationGroup) error {
	return DeleteTypedObject(objectStorer, s3PathNamePrefix(vrg.Namespace, vrg.Name), vrgS3ObjectNameSuffix, vrg)
}

func vrgObjectDownload(objectStorer ObjectStorer, pathName string, vrg *ramen.VolumeReplicationGroup) error {
	return DownloadTypedObject(objectStorer, pathName, vrgS3ObjectNameSuffix, vrg)
}
