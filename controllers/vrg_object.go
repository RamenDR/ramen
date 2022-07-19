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
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (v *VRGInstance) vrgObjectProtect(requeue *bool) {
	v.vrgObjectProtected = metav1.ConditionTrue
	fail := func(err error) {
		*requeue = true
		v.vrgObjectProtected = metav1.ConditionFalse
		rmnutil.ReportIfNotPresent(
			v.reconciler.eventRecorder,
			v.instance,
			corev1.EventTypeWarning,
			rmnutil.EventReasonVrgUploadFailed,
			err.Error(),
		)
	}

	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		objectStore, err := v.getObjectStorer(s3ProfileName)
		if err != nil {
			fail(err)

			continue
		}

		if err := VrgObjectProtect(objectStore, *v.instance); err != nil {
			fail(err)
		}
	}
}

func s3ObjectNamePrefix(vrg ramendrv1alpha1.VolumeReplicationGroup) string {
	return types.NamespacedName{Namespace: vrg.Namespace, Name: vrg.Name}.String() + "/"
}

const vrgS3ObjectNameSuffix = "a"

func VrgObjectProtect(objectStorer ObjectStorer, vrg ramendrv1alpha1.VolumeReplicationGroup) error {
	return uploadTypedObject(objectStorer, s3ObjectNamePrefix(vrg), vrgS3ObjectNameSuffix, vrg)
}

func VrgObjectUnprotect(objectStorer ObjectStorer, vrg ramendrv1alpha1.VolumeReplicationGroup) error {
	return DeleteTypedObjects(objectStorer, s3ObjectNamePrefix(vrg), vrgS3ObjectNameSuffix, vrg)
}
