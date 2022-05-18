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

package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func bvCreate(name string, s3ProfileNumber int) *ramen.S3BucketView {
	bv := &ramen.S3BucketView{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: ramen.S3BucketViewSpec{
			ProfileName: s3Profiles[s3ProfileNumber].S3ProfileName,
		},
	}
	Expect(k8sClient.Create(context.TODO(), bv)).To(Succeed())

	return bv
}

func bvGet(bv *ramen.S3BucketView) error {
	return apiReader.Get(context.TODO(), types.NamespacedName{Name: bv.Name}, bv)
}

func bvStatusUpdate(bv *ramen.S3BucketView) {
	Expect(k8sClient.Status().Update(context.TODO(), bv)).To(Succeed())
}

func bvStatusZero(bv *ramen.S3BucketView) {
	bv.Status = nil
	bvStatusUpdate(bv)
}

func bvSampleTimeRecentWait(bv *ramen.S3BucketView) {
	Eventually(func() *ramen.S3BucketViewStatus {
		Expect(bvGet(bv)).To(Succeed())

		return bv.Status
	}, timeout, interval).ShouldNot(BeNil())
	Expect(bv.Status.SampleTime.Time).Should(
		BeTemporally("~", time.Now(), 2*time.Second),
		"%#v", *bv,
	)
}

func bvCreateAndStatusWait(name string, s3ProfileNumber int) *ramen.S3BucketView {
	bv := bvCreate(name, s3ProfileNumber)
	bvSampleTimeRecentWait(bv)

	return bv
}

func bvRefresh(bv *ramen.S3BucketView) {
	bvStatusZero(bv)
	bvSampleTimeRecentWait(bv)
}

func bvDeleteAndNotFoundWait(bv *ramen.S3BucketView) {
	Expect(k8sClient.Delete(context.TODO(), bv)).To(Succeed())
	Eventually(func() error {
		return bvGet(bv)
	}, timeout, interval).Should(
		MatchError(
			errors.NewNotFound(
				schema.GroupResource{
					Group:    ramen.GroupVersion.Group,
					Resource: "s3bucketviews",
				},
				bv.Name,
			),
		),
		"%#v", *bv,
	)
}

func bvVrgsExpectIncludeOnly(bv *ramen.S3BucketView, vrgsExpected []ramen.VolumeReplicationGroup) {
	vrgsStatusStateUpdate(bv.Status.VolumeReplicationGroups, vrgsExpected)
	Expect(bv.Status.VolumeReplicationGroups).To(ConsistOf(vrgsExpected))
}

func bvVrgsExpectInclude(bv *ramen.S3BucketView, vrgsExpected []ramen.VolumeReplicationGroup) {
	vrgsStatusStateUpdate(bv.Status.VolumeReplicationGroups, vrgsExpected)
	Expect(bv.Status.VolumeReplicationGroups).To(ContainElements(vrgsExpected))
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
	// vrg is uploaded to s3 store before status is updated
	if (vrgS3.Status.State == "" || vrgS3.Status.State == ramen.UnknownState) &&
		vrgK8s.Status.State == ramen.PrimaryState {
		vrgS3.Status.State = ramen.PrimaryState
		vrgS3.ResourceVersion = vrgK8s.ResourceVersion
		vrgS3.Status.LastUpdateTime = vrgK8s.Status.LastUpdateTime
	}
}

var _ = Describe("S3BucketViewController", func() {
	const (
		namePrefix = "bv-"
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
				Async: ramen.VRGAsyncSpec{
					Mode:               ramen.AsyncModeDisabled,
					SchedulingInterval: "0m",
				},
				Sync: ramen.VRGSyncSpec{
					Mode: ramen.SyncModeEnabled,
				},
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
	bvVrgsValidate := func(bv *ramen.S3BucketView) {
		bvVrgsExpectIncludeOnly(bv, vrgsExpected())
	}
	bvRefreshAndVrgsValidate := func(bv *ramen.S3BucketView) {
		bvRefresh(bv)
		bvVrgsValidate(bv)
	}
	var bv *ramen.S3BucketView
	When("a view is created", func() {
		It("should set its status's sample time to within a second", func() {
			bv = bvCreateAndStatusWait(name0, s3ProfileNumber)
		})
	})
	When("no VRGs exist in a view's bucket", func() {
		It("should report none", func() {
			bvVrgsValidate(bv)
		})
	})
	When("a 1st VRG exists in a view's bucket and view is refreshed", func() {
		It("should report the 1st VRG", func() {
			vrgProtect(0)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	When("a 2nd VRG exists in a view's bucket and view is refreshed", func() {
		It("should report the 1st and 2nd VRGs", func() {
			vrgProtect(1)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	When("a 3rd VRG exists in a view's bucket and view is refreshed", func() {
		It("should report the 1st, 2nd, and 3rd VRGs", func() {
			vrgProtect(2)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	Context("annotations", func() {
		const (
			n      = 1
			value  = "fsda"
			value2 = value + "1"
		)
		When("the 2nd VRG's user metadata annotation is added and view is refreshed", func() {
			It("should report it", func() {
				vrgAnnotationsSet(n, value)
				vrgProtect(n)
				bvRefreshAndVrgsValidate(bv)
			})
		})
		When("the 2nd VRG's user metadata annotation is updated and view is refreshed", func() {
			It("should report the updated version", func() {
				vrgAnnotationsSet(n, value2)
				vrgProtect(n)
				bvRefreshAndVrgsValidate(bv)
			})
		})
		When("the 2nd VRG's user metadata annotation is deleted and view is refreshed", func() {
			It("should not report it", func() {
				vrgAnnotationsDelete(n)
				vrgProtect(n)
				bvRefreshAndVrgsValidate(bv)
			})
		})
	})
	When("the 2nd VRG no longer exists in a view's bucket", func() {
		It("should report the 1st and 3rd VRGs", func() {
			vrgUnprotect(1)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	When("a 4th VRG exists in a view's bucket", func() {
		It("should report the 1st, 3rd, and 4th VRGs", func() {
			vrgProtect(3)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	When("the 1st VRG no longer exists in a view's bucket", func() {
		It("should report the 3rd and 4th VRGs", func() {
			vrgUnprotect(0)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	When("the 4th VRG no longer exists in a view's bucket", func() {
		It("should report the 3rd VRG", func() {
			vrgUnprotect(3)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	When("the 3rd VRG no longer exists in a view's bucket", func() {
		It("should report no VRGs", func() {
			vrgUnprotect(2)
			bvRefreshAndVrgsValidate(bv)
		})
	})
	When("a view delete is deleted", func() {
		It("should not find it", func() {
			bvDeleteAndNotFoundWait(bv)
		})
	})
})
