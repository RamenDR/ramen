// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
)

func (v *VRGInstance) findFirstProtectedPVCWithName(pvcName string) *ramen.ProtectedPVC {
	for index := range v.instance.Status.ProtectedPVCs {
		protectedPVC := &v.instance.Status.ProtectedPVCs[index]
		if protectedPVC.Name == pvcName {
			return protectedPVC
		}
	}

	return nil
}

func (v *VRGInstance) vrgStatusPvcNamespacesSetIfUnset() {
	vrg := v.instance

	for i := range vrg.Status.ProtectedPVCs {
		vrgStatusPvc := &vrg.Status.ProtectedPVCs[i]
		if vrgStatusPvc.Namespace != "" {
			continue
		}

		v.log.Info("VRG status PVC namespace unset; setting", "PVC", vrgStatusPvc.Name)

		vrgStatusPvc.Namespace = vrg.GetNamespace()
	}
}
