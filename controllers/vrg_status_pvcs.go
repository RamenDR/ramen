// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
)

// findProtectedPVC returns the &VRG.Status.ProtectedPVC[x] for the given pvcName
func (v *VRGInstance) findProtectedPVC(pvcName string) *ramen.ProtectedPVC {
	return FindProtectedPVC(v.instance, pvcName)
}

func FindProtectedPVC(vrg *ramen.VolumeReplicationGroup, pvcName string) *ramen.ProtectedPVC {
	protectedPvc, _ := FindProtectedPvcAndIndex(vrg, pvcName)

	return protectedPvc
}

func (v *VRGInstance) pvcStatusDeleteIfPresent(pvcName string, log logr.Logger) {
	pvcStatus, i := FindProtectedPvcAndIndex(v.instance, pvcName)
	if pvcStatus == nil {
		log.Info("PVC status absent already")

		return
	}

	log.Info("PVC status delete", "index", i)
	v.instance.Status.ProtectedPVCs = sliceUnorderedElementDelete(v.instance.Status.ProtectedPVCs, i)
}

func sliceUnorderedElementDelete[T any](s []T, i int) []T {
	s[i] = s[len(s)-1]

	return s[:len(s)-1]
}

func FindProtectedPvcAndIndex(vrg *ramen.VolumeReplicationGroup, pvcName string) (*ramen.ProtectedPVC, int) {
	for index := range vrg.Status.ProtectedPVCs {
		protectedPVC := &vrg.Status.ProtectedPVCs[index]
		if protectedPVC.Name == pvcName {
			return protectedPVC, index
		}
	}

	return nil, len(vrg.Status.ProtectedPVCs)
}
