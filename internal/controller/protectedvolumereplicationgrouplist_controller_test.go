// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func protectedVrgListCreate(name string, s3ProfileNumber int) *ramen.ProtectedVolumeReplicationGroupList {
	protectedVrgList := &ramen.ProtectedVolumeReplicationGroupList{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: ramen.ProtectedVolumeReplicationGroupListSpec{
			S3ProfileName: s3Profiles[s3ProfileNumber].S3ProfileName,
		},
	}
	Expect(k8sClient.Create(context.TODO(), protectedVrgList)).To(Succeed())

	return protectedVrgList
}

func protectedVrgListGet(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) error {
	return apiReader.Get(context.TODO(), types.NamespacedName{Name: protectedVrgList.Name}, protectedVrgList)
}

func protectedVrgListStatusUpdate(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) {
	Expect(k8sClient.Status().Update(context.TODO(), protectedVrgList)).To(Succeed())
}

func protectedVrgListStatusZero(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) {
	protectedVrgList.Status = nil
	protectedVrgListStatusUpdate(protectedVrgList)
}

func protectedVrgListSampleTimeRecentWait(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) {
	Eventually(func() *ramen.ProtectedVolumeReplicationGroupListStatus {
		Expect(protectedVrgListGet(protectedVrgList)).To(Succeed())

		return protectedVrgList.Status
	}, timeout, interval).ShouldNot(BeNil())
	Expect(protectedVrgList.Status.SampleTime.Time).Should(
		BeTemporally("~", time.Now(), 2*time.Second),
		"%#v", *protectedVrgList,
	)
}

func protectedVrgListCreateAndStatusWait(name string, s3ProfileNumber int) *ramen.ProtectedVolumeReplicationGroupList {
	protectedVrgList := protectedVrgListCreate(name, s3ProfileNumber)
	protectedVrgListSampleTimeRecentWait(protectedVrgList)

	return protectedVrgList
}

func protectedVrgListRefresh(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) {
	protectedVrgListStatusZero(protectedVrgList)
	protectedVrgListSampleTimeRecentWait(protectedVrgList)
}

func protectedVrgListDeleteAndNotFoundWait(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) {
	Expect(k8sClient.Delete(context.TODO(), protectedVrgList)).To(Succeed())
	Eventually(func() error {
		return protectedVrgListGet(protectedVrgList)
	}, timeout, interval).Should(
		MatchError(
			errors.NewNotFound(
				schema.GroupResource{
					Group:    ramen.GroupVersion.Group,
					Resource: "protectedvolumereplicationgrouplists",
				},
				protectedVrgList.Name,
			),
		),
		"%#v", *protectedVrgList,
	)
}

func protectedVrgListExpectIncludeOnly(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList,
	vrgsExpected []ramen.VolumeReplicationGroup,
) {
	vrgsStatusStateUpdate(protectedVrgList.Status.Items, vrgsExpected)
	Expect(protectedVrgList.Status.Items).To(ConsistOf(vrgsExpected))
}

func protectedVrgListExpectInclude(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList,
	vrgsExpected []ramen.VolumeReplicationGroup,
) {
	vrgsStatusStateUpdate(protectedVrgList.Status.Items, vrgsExpected)
	Expect(protectedVrgList.Status.Items).To(ContainElements(vrgsExpected))
}

func vrgsStatusStateUpdate(vrgsS3, vrgsK8s []ramen.VolumeReplicationGroup) {
	for i := range vrgsS3 {
		vrgS3 := &vrgsS3[i]

		for j := range vrgsK8s {
			vrgK8s := &vrgsK8s[j]
			if vrgS3.Namespace == vrgK8s.Namespace &&
				vrgS3.Name == vrgK8s.Name {
				vrgStatusStateUpdate(vrgS3, vrgK8s)

				break
			}
		}
	}
}

func vrgStatusStateUpdate(vrgS3, vrgK8s *ramen.VolumeReplicationGroup) {
	// VRG is not reconciled for VRG status updates
	if vrgS3.Status.ObservedGeneration != vrgK8s.Status.ObservedGeneration {
		vrgS3.Status.ObservedGeneration = vrgK8s.Status.ObservedGeneration
		vrgS3.Status.LastUpdateTime = vrgK8s.Status.LastUpdateTime
	}

	// vrg is uploaded to s3 store before status is updated
	if (vrgS3.Status.State == "" || vrgS3.Status.State == ramen.UnknownState) &&
		vrgK8s.Status.State == ramen.PrimaryState {
		vrgS3.Status.State = ramen.PrimaryState
		vrgS3.ResourceVersion = vrgK8s.ResourceVersion
		vrgS3.Status.LastUpdateTime = vrgK8s.Status.LastUpdateTime
	}

	if vrgS3.Status.State == "" && vrgK8s.Status.State == ramen.UnknownState {
		vrgS3.Status.State = ramen.UnknownState
		vrgS3.ResourceVersion = vrgK8s.ResourceVersion
		vrgS3.Status.LastUpdateTime = vrgK8s.Status.LastUpdateTime
	}
}

var _ = Describe("ProtectedVolumeReplicationGroupListController", func() {
	const (
		namePrefix = "protectedvrglist-"
		name0      = namePrefix + "0"
		name1      = namePrefix + "1"
	)
	vrg := func(namespaceName, objectName string) ramen.VolumeReplicationGroup {
		return ramen.VolumeReplicationGroup{
			TypeMeta: metav1.TypeMeta{
				APIVersion: ramen.GroupVersion.String(),
				Kind:       "VolumeReplicationGroup",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceName,
				Name:      objectName,
			},
			Spec: ramen.VolumeReplicationGroupSpec{
				PVCSelector:      metav1.LabelSelector{},
				ReplicationState: ramen.Primary,
				S3Profiles:       []string{},
				Sync:             &ramen.VRGSyncSpec{},
			},
		}
	}
	vrgs := [...]ramen.VolumeReplicationGroup{
		vrg(name0, name0),
		vrg(name1, name1),
		vrg(name1, name0),
		vrg(name0, name1),
	}
	const s3ProfileNumber int = 1
	objectStorer := &objectStorers[s3ProfileNumber]
	vrgNumbersExpected := make(map[int]struct{}, len(vrgs))
	vrgAnnotationsSet := func(number int, value string) {
		vrgs[number].Annotations = map[string]string{"userMetadata": value}
	}
	vrgAnnotationsDelete := func(number int) {
		vrgs[number].Annotations = nil
	}
	vrgProtect := func(number int) {
		Expect(controllers.VrgObjectProtect(*objectStorer, vrgs[number])).To(Succeed())
		vrgNumbersExpected[number] = struct{}{}
	}
	vrgUnprotect := func(number int) {
		Expect(controllers.VrgObjectUnprotect(*objectStorer, vrgs[number])).To(Succeed())
		delete(vrgNumbersExpected, number)
	}
	vrgsExpected := func() (vrgsExpected []ramen.VolumeReplicationGroup) {
		for number := range vrgNumbersExpected {
			vrgsExpected = append(vrgsExpected, vrgs[number])
		}

		return
	}
	protectedVrgListValidate := func(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) {
		protectedVrgListExpectIncludeOnly(protectedVrgList, vrgsExpected())
	}
	protectedVrgListRefreshAndValidate := func(protectedVrgList *ramen.ProtectedVolumeReplicationGroupList) {
		protectedVrgListRefresh(protectedVrgList)
		protectedVrgListValidate(protectedVrgList)
	}
	var protectedVrgList *ramen.ProtectedVolumeReplicationGroupList
	When("a list is created", func() {
		It("should set its status's sample time to within a second", func() {
			protectedVrgList = protectedVrgListCreateAndStatusWait(name0, s3ProfileNumber)
		})
	})
	When("no VRGs exist in a list's store", func() {
		It("should report none", func() {
			protectedVrgListValidate(protectedVrgList)
		})
	})
	When("a 1st VRG exists in a list's store and list is refreshed", func() {
		It("should report the 1st VRG", func() {
			vrgProtect(0)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	When("a 2nd VRG exists in a list's store and list is refreshed", func() {
		It("should report the 1st and 2nd VRGs", func() {
			vrgProtect(1)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	When("a 3rd VRG exists in a list's store and list is refreshed", func() {
		It("should report the 1st, 2nd, and 3rd VRGs", func() {
			vrgProtect(2)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	Context("annotations", func() {
		const (
			n      = 1
			value  = "fsda"
			value2 = value + "1"
		)
		When("the 2nd VRG's user metadata annotation is added and list is refreshed", func() {
			It("should report it", func() {
				vrgAnnotationsSet(n, value)
				vrgProtect(n)
				protectedVrgListRefreshAndValidate(protectedVrgList)
			})
		})
		When("the 2nd VRG's user metadata annotation is updated and list is refreshed", func() {
			It("should report the updated version", func() {
				vrgAnnotationsSet(n, value2)
				vrgProtect(n)
				protectedVrgListRefreshAndValidate(protectedVrgList)
			})
		})
		When("the 2nd VRG's user metadata annotation is deleted and list is refreshed", func() {
			It("should not report it", func() {
				vrgAnnotationsDelete(n)
				vrgProtect(n)
				protectedVrgListRefreshAndValidate(protectedVrgList)
			})
		})
	})
	When("the 2nd VRG no longer exists in a list's store", func() {
		It("should report the 1st and 3rd VRGs", func() {
			vrgUnprotect(1)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	When("a 4th VRG exists in a list's store", func() {
		It("should report the 1st, 3rd, and 4th VRGs", func() {
			vrgProtect(3)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	When("the 1st VRG no longer exists in a list's store", func() {
		It("should report the 3rd and 4th VRGs", func() {
			vrgUnprotect(0)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	When("the 4th VRG no longer exists in a list's store", func() {
		It("should report the 3rd VRG", func() {
			vrgUnprotect(3)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	When("the 3rd VRG no longer exists in a list's store", func() {
		It("should report no VRGs", func() {
			vrgUnprotect(2)
			protectedVrgListRefreshAndValidate(protectedVrgList)
		})
	})
	When("a list delete is deleted", func() {
		It("should not find it", func() {
			protectedVrgListDeleteAndNotFoundWait(protectedVrgList)
		})
	})
})
