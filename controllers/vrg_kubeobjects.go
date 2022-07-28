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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"

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

		err = v.processSubBackups(objectStore, s3ProfileName, captureNumber)
		if err != nil {
			errors := append(errors, err)

			return errors
		}
	}

	return nil
}

func (v *VRGInstance) processSubBackups(objectStore ObjectStorer, s3ProfileName string, captureNumber int64) error {
	categories := v.getTypeSequenceSubBackups()

	var err error // declare for scope

	for _, captureInstance := range categories {
		includeList, excludeList := getTypeSequenceResourceList(captureInstance.IncludedResources,
			captureInstance.ExcludedResources)

		spec := captureInstance.DeepCopy() // don't modify spec with processed results
		spec.IncludedResources = includeList
		spec.ExcludedResources = excludeList

		sourceNamespacedName := types.NamespacedName{Name: v.instance.Name, Namespace: v.instance.Namespace}

		if err := kubeObjectsProtect(
			v.ctx,
			v.reconciler.Client,
			v.reconciler.APIReader,
			v.log,
			objectStore.AddressComponent1(),
			objectStore.AddressComponent2(),
			v.s3KeyPrefix(),
			sourceNamespacedName,
			captureInstance,
			captureNumber,
		); err != nil {
			v.log.Error(err, "kube object protect", "profile", s3ProfileName)
			// don't return error yet since it could be non-fatal (e.g. slow API server)
		}

		backupNamespacedName := getBackupNamespacedName(sourceNamespacedName.Name,
			sourceNamespacedName.Namespace, spec.Name, captureNumber)

		// TODO: remove tight loop here
		backupComplete := false
		for !backupComplete {
			backupComplete, err = backupIsDone(v.ctx, v.reconciler.APIReader,
				objectWriter{v.reconciler.Client, v.ctx, v.log}, backupNamespacedName)

			if err != nil {
				backupComplete = true
			}
		}
	}

	return err
}

// return values: includedResources, excludedResources
func getTypeSequenceResourceList(toInclude, toExclude []string) ([]string, []string) {
	included := []string{"*"} // include everything
	excluded := []string{}    // exclude nothing

	if toInclude != nil {
		included = toInclude
	}

	if toExclude != nil {
		excluded = toExclude
	}

	return included, excluded
}

func (v *VRGInstance) getTypeSequenceSubBackups() []ramen.ResourceCaptureGroupSpec {
	if v.instance.Spec.KubeObjectProtection != nil &&
		v.instance.Spec.KubeObjectProtection.ResourceCaptureOrder != nil {
		return v.instance.Spec.KubeObjectProtection.ResourceCaptureOrder
	}

	// default case: no backup groups defined
	return getTypeSequenceDefaultBackup()
}

func (v *VRGInstance) getTypeSequenceSubRestores() []ramen.ResourceRecoveryGroupSpec {
	if v.instance.Spec.KubeObjectProtection != nil &&
		v.instance.Spec.KubeObjectProtection.ResourceCaptureOrder != nil {
		return v.instance.Spec.KubeObjectProtection.ResourceRecoveryOrder
	}

	// default case: no backup groups defined
	return getTypeSequenceDefaultRestore()
}

func getTypeSequenceDefaultBackup() []ramen.ResourceCaptureGroupSpec {
	instance := make([]ramen.ResourceCaptureGroupSpec, 1)

	includedResources := make([]string, 0)
	includedResources[0] = "*"

	instance[0].Name = "everything"
	*instance[0].IncludeClusterResources = false
	instance[0].IncludedResources = includedResources

	return instance
}

func getTypeSequenceDefaultRestore() []ramen.ResourceRecoveryGroupSpec {
	instance := make([]ramen.ResourceRecoveryGroupSpec, 1)

	includedResources := make([]string, 0)
	includedResources[0] = "*"

	*instance[0].IncludeClusterResources = true // TODO: needed for default restore case?
	instance[0].IncludedResources = includedResources
	instance[0].BackupName = "everything"

	return instance
}

func getBackupNamespacedName(vrg, namespace, backupName string, sequenceNumber int64) types.NamespacedName {
	name := fmt.Sprintf("%s-%s-%s-%d", vrg, namespace, backupName, sequenceNumber)
	namespacedName := types.NamespacedName{
		Name:      name,
		Namespace: namespace, // note: must create backups in the same namespace as Velero/OADP
	}

	return namespacedName
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

	categories := v.getTypeSequenceSubRestores()

	var err error // declare for scope

	for restoreIndex, restoreInstance := range categories {
		err = v.processSubRestores(restoreIndex, restoreInstance, objectStore)

		if err != nil {
			return err
		}
	}

	return err
}

func (v *VRGInstance) processSubRestores(restoreIndex int, restoreInstance ramen.ResourceRecoveryGroupSpec,
	objectStore ObjectStorer) error {
	includeList, excludeList := getTypeSequenceResourceList(restoreInstance.IncludedResources,
		restoreInstance.ExcludedResources)

	spec := restoreInstance.DeepCopy() // don't modify spec with processed results
	spec.IncludedResources = includeList
	spec.ExcludedResources = excludeList

	sourceNamespacedName := types.NamespacedName{Name: v.instance.Name, Namespace: v.instance.Namespace}

	var err error // declare for scope

	if err := kubeObjectsRecover(
		v.ctx,
		v.reconciler.Client,
		v.reconciler.APIReader,
		v.log,
		objectStore.AddressComponent1(),
		objectStore.AddressComponent2(),
		v.s3KeyPrefix(),
		// TODO query source namespace from velero backup kube object in s3 store
		sourceNamespacedName,
		v.instance.Namespace,
		*spec,
		restoreIndex,
		v.instance.Status.KubeObjectProtection.LastProtectedCapture.Number,
	); err != nil {
		v.log.Error(err, "kube object recover")
		// don't return error yet since it could be non-fatal (e.g. slow API server)
	}

	// for backupName, user specifies name. for restoreName, it's generated by system.
	restoreNamespacedName := getBackupNamespacedName(sourceNamespacedName.Name,
		sourceNamespacedName.Namespace, fmt.Sprintf("%d", restoreIndex),
		v.instance.Status.KubeObjectProtection.LastProtectedCapture.Number)

	// can only create backup/restores in Velero/OADP namespace
	restoreNamespacedName.Namespace = VeleroNamespaceNameDefault

	// TODO: remove tight loop here
	restoreComplete := false
	for !restoreComplete {
		restoreComplete, err = restoreIsDone(v.ctx, v.reconciler.APIReader,
			objectWriter{v.reconciler.Client, v.ctx, v.log}, restoreNamespacedName)

		if err != nil {
			restoreComplete = true
		}
	}

	return err
}

func (v *VRGInstance) kubeObjectProtectionDisabledOrKubeObjectsProtected() bool {
	return v.instance.Spec.KubeObjectProtection == nil ||
		v.instance.Status.KubeObjectProtection.LastProtectedCapture.Number >= 0
}
