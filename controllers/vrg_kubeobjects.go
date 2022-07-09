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

func (v *VRGInstance) kubeObjectsProtect() error {
	if v.instance.Spec.KubeObjectProtection == nil {
		v.log.Info("Kube object protection disabled")

		return nil
	}

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
		); err != nil {
			v.log.Error(err, "kube object protect", "profile", s3ProfileName)
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return errors[0]
	}

	return nil
}

func (v *VRGInstance) kubeObjectsRecover(objectStore ObjectStorer) error {
	if v.instance.Spec.KubeObjectProtection == nil {
		v.log.Info("Kube object recovery disabled")

		return nil
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
	)
}
