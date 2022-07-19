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
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func kubeObjectCaptureInterval(kubeObjectProtectionSpec *ramen.KubeObjectProtectionSpec) time.Duration {
	if kubeObjectProtectionSpec.CaptureInterval == nil {
		return ramen.KubeObjectProtectionCaptureIntervalDefault
	}

	return kubeObjectProtectionSpec.CaptureInterval.Duration
}

func (v *VRGInstance) kubeObjectsProtectIfDue(result *ctrl.Result) {
	if v.instance.Spec.KubeObjectProtection == nil {
		v.log.Info("Kube object protection disabled")

		return
	}

	delayMinimum := kubeObjectCaptureInterval(v.instance.Spec.KubeObjectProtection)
	dueTime := v.instance.Status.KubeObjectProtection.LastProtectedCapture.StartTime.Time.Add(delayMinimum)
	capture := &ramen.KubeObjectCaptureStatus{
		Number:    v.instance.Status.KubeObjectProtection.LastProtectedCapture.Number + 1,
		StartTime: metav1.Now(),
	}

	delay := dueTime.Sub(capture.StartTime.Time)
	if delay > 0 {
		v.log.Info("Kube object protection due later", "number", capture.Number, "delay", delay)
		delaySetIfLess(result, delay, v.log)

		return
	}

	if errors := v.kubeObjectsProtect(capture.Number); len(errors) > 0 {
		result.Requeue = true

		return
	}

	duration := time.Since(capture.StartTime.Time)
	delay = delayMinimum - duration

	if delay <= 0 {
		delay = time.Nanosecond
	}

	v.log.Info("Kube objects protected",
		"number", capture.Number,
		"start", capture.StartTime,
		"duration", duration,
		"delay", delay,
	)

	v.instance.Status.KubeObjectProtection.LastProtectedCapture = capture

	delaySetIfLess(result, delay, v.log)
}

func (v *VRGInstance) kubeObjectsProtect(captureNumber int64) []error {
	errors := make([]error, 0, len(v.instance.Spec.S3Profiles))

	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		// TODO reuse objectStore kube objects from pv upload
		objectStore, err := v.reconciler.ObjStoreGetter.ObjectStore(
			v.ctx,
			v.reconciler.APIReader,
			s3ProfileName,
			v.namespacedName,
			v.log,
		)
		if err != nil {
			v.log.Error(err, "kube objects protect object store access", "profile", s3ProfileName)
			errors = append(errors, err)

			continue
		}

		if err := kubeObjectsProtect(
			v.ctx,
			v.reconciler.Client,
			v.reconciler.APIReader,
			v.log,
			objectStore.AddressComponent1(),
			objectStore.AddressComponent2(),
			v.s3KeyPrefix(),
			v.instance.Namespace,
			VeleroNamespaceNameDefault,
			captureNumber,
		); err != nil {
			v.log.Error(err, "kube object protect", "profile", s3ProfileName)
			errors = append(errors, err)
		}
	}

	return errors
}

func (v *VRGInstance) kubeObjectsRecover(objectStore ObjectStorer) error {
	if v.instance.Spec.KubeObjectProtection == nil {
		v.log.Info("Kube object recovery disabled")

		return nil
	}

	if v.instance.Status.KubeObjectProtection.LastProtectedCapture == nil {
		notFound := func() error {
			v.instance.Status.KubeObjectProtection.LastProtectedCapture = &ramen.KubeObjectCaptureStatus{Number: -1}

			return nil
		}

		var vrg ramen.VolumeReplicationGroup
		if err := downloadTypedObject(objectStore, s3ObjectNamePrefix(*v.instance), vrgS3ObjectNameSuffix, &vrg); err != nil {
			v.log.Error(err, "protected last protected Kube object capture status get")

			return notFound()
		}

		if vrg.Status.KubeObjectProtection.LastProtectedCapture == nil {
			v.log.Info("Protected last protected Kube object capture status nil")

			return notFound()
		}

		v.instance.Status.KubeObjectProtection.LastProtectedCapture = vrg.Status.KubeObjectProtection.LastProtectedCapture
	}

	return kubeObjectsRecover(
		v.ctx,
		v.reconciler.Client,
		v.reconciler.APIReader,
		v.log,
		objectStore.AddressComponent1(),
		objectStore.AddressComponent2(),
		v.s3KeyPrefix(),
		// TODO query source namespace from velero backup kube object in s3 store
		v.instance.Namespace,
		v.instance.Namespace,
		VeleroNamespaceNameDefault,
		v.instance.Status.KubeObjectProtection.LastProtectedCapture.Number,
	)
}

func (v *VRGInstance) kubeObjectProtectionDisabledOrKubeObjectsProtected() bool {
	return v.instance.Spec.KubeObjectProtection == nil ||
		v.instance.Status.KubeObjectProtection.LastProtectedCapture.Number >= 0
}
