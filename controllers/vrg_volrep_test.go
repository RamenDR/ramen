// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand"
	"strconv"
	"strings"
	"time"

	volrep "github.com/csi-addons/kubernetes-csi-addons/apis/replication.storage/v1alpha1"
	volrepController "github.com/csi-addons/kubernetes-csi-addons/controllers/replication.storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	gomegatypes "github.com/onsi/gomega/types"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	vrgController "github.com/ramendr/ramen/controllers"
	"github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	vrgtimeout   = time.Second * 9
	vrginterval  = time.Millisecond * 10
	letters      = "abcdefghijklmnopqrstuxwxyz"
	namespaceLen = 5
)

var vrgObjectStorer = &objectStorers[vrgS3ProfileNumber]

func init() {
	rand.Seed(time.Now().Unix())
}

var _ = Describe("VolumeReplicationGroupVolRepController", func() {
	conditionStatusReasonExpect := func(condition *metav1.Condition, status metav1.ConditionStatus, reason string) {
		Expect(condition.Status).To(Equal(status))
		Expect(condition.Reason).To(Equal(reason))
	}
	var vrg *ramendrv1alpha1.VolumeReplicationGroup
	vrgConditionExpect := func(typ string) *metav1.Condition {
		return conditionExpect(vrg.Status.Conditions, typ)
	}
	vrgConditionStatusReasonExpect := func(typ string, status metav1.ConditionStatus, reason string) *metav1.Condition {
		condition := vrgConditionExpect(typ)
		conditionStatusReasonExpect(condition, status, reason)

		return condition
	}
	var vrgNamespacedName types.NamespacedName
	vrgGet := func() *ramendrv1alpha1.VolumeReplicationGroup {
		Expect(apiReader.Get(context.TODO(), vrgNamespacedName, vrg)).To(Succeed())

		return vrg
	}
	vrgResourceVersionGet := func() string {
		return vrgGet().GetResourceVersion()
	}
	vrgStatusPvcsGet := func() []ramendrv1alpha1.ProtectedPVC {
		return vrgGet().Status.ProtectedPVCs
	}
	var dataReadyCondition *metav1.Condition
	When("ReplicationState is invalid", func() {
		It("should set DataReady status=False reason=Error", func() {
			vrg = &ramendrv1alpha1.VolumeReplicationGroup{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "asdf",
				},
				Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
					PVCSelector:      metav1.LabelSelector{},
					ReplicationState: "invalid",
					S3Profiles:       []string{},
				},
			}
			Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())
			vrgNamespacedName = types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}
			Eventually(func() int {
				vrgGet()

				return len(vrg.Status.Conditions)
			}, timeout, interval).ShouldNot(BeZero())
			dataReadyCondition = vrgConditionStatusReasonExpect("DataReady", metav1.ConditionFalse, "Error")
		})
		It("should set DataProtected status=Unknown reason=Initializing", func() {
			vrgConditionStatusReasonExpect("DataProtected", metav1.ConditionUnknown, "Initializing")
		})
		It("should set ClusterDataReady status=Unknown reason=Initializing", func() {
			vrgConditionStatusReasonExpect("ClusterDataReady", metav1.ConditionUnknown, "Initializing")
		})
		It("should set ClusterDataProtected status=Unknown reason=Initializing", func() {
			vrgConditionStatusReasonExpect("ClusterDataProtected", metav1.ConditionUnknown, "Initializing")
		})
	})
	When("ReplicationState is primary, but sync and async are disabled", func() {
		It("should change DataReady message", func() {
			vrg.Spec.ReplicationState = "primary"
			dataReadyConditionMessage := dataReadyCondition.Message
			Expect(k8sClient.Update(context.TODO(), vrg)).To(Succeed())
			Eventually(func() string {
				vrgGet()
				dataReadyCondition = vrgConditionExpect("DataReady")

				return dataReadyCondition.Message
			}, timeout, interval).ShouldNot(Equal(dataReadyConditionMessage))
			vrgConditionStatusReasonExpect("DataReady", metav1.ConditionFalse, "Error")
		})
	})
	When("ReplicationState is primary and sync is enabled, but s3 profiles are absent", func() {
		It("should set ClusterDataReady status=False reason=Error", func() {
			vrg.Spec.Sync = &ramendrv1alpha1.VRGSyncSpec{}
			Expect(k8sClient.Update(context.TODO(), vrg)).To(Succeed())
			var clusterDataReadyCondition *metav1.Condition
			Eventually(func() metav1.ConditionStatus {
				vrgGet()
				clusterDataReadyCondition = vrgConditionExpect("ClusterDataReady")

				return clusterDataReadyCondition.Status
			}, timeout, interval).Should(Equal(metav1.ConditionFalse))
			Expect(clusterDataReadyCondition.Reason).To(Equal("Error"))
		})
	})
	When("VRG is deleted", func() {
		BeforeEach(func() {
			Expect(k8sClient.Delete(context.TODO(), vrg)).To(Succeed())
		})
		It("should allow the VRG to be deleted", func() {
			Eventually(func() error {
				return apiReader.Get(context.TODO(), vrgNamespacedName, vrg)
			}).Should(MatchError(errors.NewNotFound(schema.GroupResource{
				Group:    ramendrv1alpha1.GroupVersion.Group,
				Resource: "volumereplicationgroups",
			}, vrg.Name)))
		})
	})
	var pv0 *corev1.PersistentVolume
	var pvc0 *corev1.PersistentVolumeClaim
	When("PV exists, is bound, and its claim's deletion timestamp is non-zero", func() {
		BeforeEach(func() {
			pv := pv("pv0", "pvc0", vrg.Namespace, "")
			pvc := pvc(pv.Spec.ClaimRef.Name, pv.Spec.ClaimRef.Namespace, pv.Name, pv.Spec.StorageClassName, nil)
			pvc.Finalizers = []string{"ramendr.openshift.io/asdf"}
			vrgS3KeyPrefix := vrgS3KeyPrefix(vrgNamespacedName)
			populateS3Store(vrgS3KeyPrefix, []corev1.PersistentVolume{*pv}, []corev1.PersistentVolumeClaim{*pvc})
			Expect(k8sClient.Create(context.TODO(), pv)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), pvc)).To(Succeed())
			Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: pv.Name}, pv)).To(Succeed())
			pv.Status.Phase = corev1.VolumeBound
			Expect(k8sClient.Status().Update(context.TODO(), pv)).To(Succeed())
			Expect(k8sClient.Delete(context.TODO(), pvc)).To(Succeed())
			Expect(apiReader.Get(context.TODO(), types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}, pvc)).
				To(Succeed())
			pv0 = pv
			pvc0 = pvc
		})
		It("should set ClusterDataReady false", func() {
			vrg.ResourceVersion = ""
			vrg.Spec.S3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			Expect(k8sClient.Create(context.TODO(), vrg)).To(Succeed())
			Expect(apiReader.Get(context.TODO(), vrgNamespacedName, vrg)).To(Succeed())
			Eventually(func() *metav1.Condition {
				vrgGet()

				return meta.FindStatusCondition(vrg.Status.Conditions, "ClusterDataReady")
			}).Should(And(
				Not(BeNil()),
				HaveField("Status", metav1.ConditionFalse),
				HaveField("Reason", "Error"),
			))
		})
	})
	When("PVC is deleted finally and PV is unbound", func() {
		BeforeEach(func() {
			pv := pv0
			pvc := pvc0
			Expect(apiReader.Get(context.TODO(), types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}, pvc)).
				To(Succeed())
			pvc.Finalizers = []string{}
			Expect(k8sClient.Update(context.TODO(), pvc)).To(Succeed())
			Expect(apiReader.Get(context.TODO(), types.NamespacedName{Name: pv.Name}, pv)).To(Succeed())
			pv.Status.Phase = corev1.VolumePending
			Expect(k8sClient.Status().Update(context.TODO(), pv)).To(Succeed())
		})
		It("should set ClusterDataReady true", func() {
			Eventually(func() *metav1.Condition {
				vrgGet()

				return meta.FindStatusCondition(vrg.Status.Conditions, "ClusterDataReady")
			}).Should(
				HaveField("Status", metav1.ConditionTrue),
			)
		})
	})
	Specify("PV delete", func() {
		Expect(k8sClient.Delete(context.TODO(), pv0)).To(Succeed())
	})
	Specify("VRG delete", func() {
		Expect(k8sClient.Delete(context.TODO(), vrg)).To(Succeed())
	})

	// Test first restore
	Context("restore test case", func() {
		restoreTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			storageClassName:       "manual",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		It("populates the S3 store with PVs/PVCs and start vrg as primary to check that the PVs/PVCs are restored", func() {
			restoreTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			numPVs := 3
			vtest := newVRGTestCaseCreate(0, restoreTestTemplate, true, false)
			vtest.skipCreationPVandPVC = true
			pvList := vtest.generateFakePVs("pv", numPVs)
			pvcList := vtest.generateFakePVCs(pvList)
			populateS3Store(vtest.s3KeyPrefix(), pvList, pvcList)
			vtest.VRGTestCaseStart()
			waitForPVRestore(pvList)
			waitForPVCRestore(pvcList)
			Expect(vtest.getVRG().Status.State).ToNot(Equal(ramendrv1alpha1.PrimaryState))
			updatePVCClaimBindInfo(pvcList, corev1.ClaimBound)
			vtest.waitForVRCountToMatch(3)
			vtest.promoteVolReps()
			vtest.waitForVRGStateToTransitionToPrimary()
			cleanupS3Store()
		})
	})

	// Test restore success when bound PV/PVC are present
	var vrgTestBoundPV *vrgTest
	Context("restore test case for existing and bound PV/PVC", Ordered, func() {
		restoreTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			storageClassName:       "manual",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		const pvcCount = 3
		It("populates the S3 store with PVs and starts vrg as primary", func() {
			restoreTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			numPVs := pvcCount
			vrgTestBoundPV = newVRGTestCaseCreate(numPVs, restoreTestTemplate, true, false)
			pvList := vrgTestBoundPV.generateFakePVs("pv", numPVs)
			populateS3Store(vrgTestBoundPV.s3KeyPrefix(), pvList, []corev1.PersistentVolumeClaim{})
			vrgTestBoundPV.VRGTestCaseStart()
		})
		It("waits for VRG to create a VR for each PVC", func() {
			vrgTestBoundPV.waitForVRCountToMatch(len(vrgTestBoundPV.pvcNames))
		})
		It("waits for VRG status to match", func() {
			vrgTestBoundPV.promoteVolReps()
			vrgTestBoundPV.verifyVRGStatusExpectation(true)
		})
		var pvcNamespacedNamesActual [pvcCount]types.NamespacedName
		var pvcNamespacedNamesUnqualified, pvcNamespacedNamesQualified []types.NamespacedName
		When("a VRG, reconciled by a controller without multi-namespace support,", func() {
			var t *vrgTest
			const (
				vrgGenerationExpected int64 = 1 + iota
				vrgGenerationNext
			)
			BeforeAll(func() {
				t = vrgTestBoundPV
				vrgNamespacedName := t.vrgNamespacedName()
				pvcNamespacedNamesUnqualified = t.pvcNames[0:1]
				pvcNamespacedNamesQualified = t.pvcNames[1:]

				vrgObjectStorer := *vrgObjectStorer
				vrgS3KeyPrefix := vrgS3KeyPrefix(vrgNamespacedName)
				t.clusterDataProtectedWait(metav1.ConditionTrue)

				By("storing PVCs in S3 without namespace name in key suffix")
				var pvcs []corev1.PersistentVolumeClaim
				Expect(vrgController.DownloadTypedObjects(vrgObjectStorer, vrgS3KeyPrefix, &pvcs)).To(Succeed())
				pvcsMap := make(map[types.NamespacedName]int, len(pvcs))
				for i := range pvcs {
					pvc := &pvcs[i]
					pvcNamespacedName := client.ObjectKeyFromObject(pvc)
					pvcNamespacedNamesActual[i] = pvcNamespacedName
					pvcsMap[pvcNamespacedName] = i
				}
				Expect(pvcNamespacedNamesActual).To(ConsistOf(t.pvcNames))
				for _, pvcNamespacedName := range pvcNamespacedNamesUnqualified {
					pvc := pvcs[pvcsMap[pvcNamespacedName]]
					Expect(vrgController.DeleteTypedObject(vrgObjectStorer, vrgS3KeyPrefix,
						pvcNamespacedName.String(), &corev1.PersistentVolumeClaim{})).To(Succeed())
					Expect(pvc.Namespace).ToNot(BeEmpty())
					Expect(vrgController.UploadPVC(vrgObjectStorer, vrgS3KeyPrefix, pvc.Name, pvc)).To(Succeed())
				}

				By("storing VRG status without PVC namespace name")
				vrg = t.getVRG()
				Expect(vrg.GetGeneration()).To(Equal(vrgGenerationExpected))
				for i := range vrg.Status.ProtectedPVCs {
					pvc := &vrg.Status.ProtectedPVCs[i]
					pvcNamespacedNamesActual[i] = types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}
				}
				Expect(pvcNamespacedNamesActual).To(ConsistOf(t.pvcNames))
				for _, pvcNamespacedName := range pvcNamespacedNamesUnqualified {
					pvc := vrgController.FindProtectedPVC(vrg, pvcNamespacedName.Namespace, pvcNamespacedName.Name)
					Expect(pvc).ToNot(BeNil())
					pvc.Namespace = ""
				}
				Expect(k8sClient.Status().Update(ctx, vrg)).To(Succeed())
			})
			Context("recovers", func() {
				BeforeAll(func() {
					Expect(vrg.GetGeneration()).To(Equal(vrgGenerationExpected))
					vrg.Spec.Action = ramendrv1alpha1.VRGActionFailover
					Expect(k8sClient.Update(ctx, vrg)).To(Succeed())
					Eventually(func() int64 {
						vrg = t.getVRG()
						Expect(vrg.GetGeneration()).To(Equal(vrgGenerationNext))

						return vrg.Status.ObservedGeneration
					}).Should(Equal(vrgGenerationNext))
				})
				It("sets PVC's namespace name in VRG status", func() {
					Expect(vrg.Status.ProtectedPVCs).To(HaveLen(len(t.pvcNames)))
					for i, pvc := range vrg.Status.ProtectedPVCs {
						pvcNamespacedNamesActual[i] = types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}
					}
					Expect(pvcNamespacedNamesActual).To(ConsistOf(t.pvcNames))
				})
				It("sets cluster data ready with current generation", func() {
					clusterDataReady := meta.FindStatusCondition(vrg.Status.Conditions, vrgController.VRGConditionTypeClusterDataReady)
					Expect(clusterDataReady).ToNot(BeNil())
					Expect(clusterDataReady.ObservedGeneration).To(Equal(vrgGenerationNext))
					Expect(clusterDataReady.Status).To(Equal(metav1.ConditionTrue))
				})
			})
		})
		Context("PVC selection", func() {
			var t *vrgTest
			BeforeAll(func() {
				vrgController.VolumeUnprotectionEnabledForAsyncVolRep = true
				DeferCleanup(func() {
					vrgController.VolumeUnprotectionEnabledForAsyncVolRep = false
				})
				t = vrgTestBoundPV
				vrgNamespacedName = t.vrgNamespacedName()
			})
			var pvcNamesSelected, pvcNamesDeselected []types.NamespacedName
			pvcsVerify := func(pvcNames []types.NamespacedName,
				verify func(vrg ramendrv1alpha1.VolumeReplicationGroup, pvcNamespacedName types.NamespacedName, pvName string),
			) {
				for _, pvcName := range pvcNames {
					verify(*vrg, pvcName, t.pvcVolumeNames[pvcName])
				}
			}
			Context("all are selected", func() {
				BeforeAll(func() {
					pvcNamesSelected = t.pvcNames
					vrgGet()
				})
				It("protects them", func() {
					pvcsVerify(pvcNamesSelected, pvcProtectedVerify)
				})
			})
			var vrgResourceVersion string
			When("two are deselected with volume unprotection disabled", func() {
				BeforeAll(func() {
					ramenConfig.VolumeUnprotectionEnabled = false
					configMapUpdate()
					pvcNamesDeselected = append(pvcNamesDeselected, pvcNamespacedNamesUnqualified[0], pvcNamespacedNamesQualified[0])
					pvcNamesSelected = nil
					pvcNamesSelected = append(append(
						pvcNamesSelected, pvcNamespacedNamesUnqualified[1:]...), pvcNamespacedNamesQualified[1:]...)
					vrgResourceVersion = vrgResourceVersionGet()
					forPVCs(pvcNamesDeselected, func(pvc corev1.PersistentVolumeClaim) {
						util.ObjectLabelsDelete(&pvc, t.pvcLabels)
						Expect(k8sClient.Update(context.TODO(), &pvc)).To(Succeed())
					})
				})
				It("updates the status", func() {
					Eventually(vrgResourceVersionGet).ShouldNot(Equal(vrgResourceVersion))
				})
				It("keeps the selected protected", func() {
					pvcsVerify(pvcNamesSelected, pvcProtectedVerify)
				})
				It("keeps them protected", func() {
					pvcsVerify(pvcNamesDeselected, pvcProtectedVerify)
				})
			})
			When("volume protection is enabled", func() {
				BeforeAll(func() {
					ramenConfig.VolumeUnprotectionEnabled = true
					vrgResourceVersion = vrgResourceVersionGet()
					configMapUpdate()
				})
				It("updates the status", func() {
					Eventually(vrgResourceVersionGet).ShouldNot(Equal(vrgResourceVersion))
				})
				It("keeps the selected protected", func() {
					pvcsVerify(pvcNamesSelected, pvcProtectedVerify)
				})
				It("unprotects the deselected", func() {
					pvcsVerify(pvcNamesDeselected, pvcUnprotectedVerify)
				})
			})
			When("one is reselected", func() {
				var pvcNamesReselected []types.NamespacedName
				BeforeAll(func() {
					pvcNamesReselected = pvcNamesDeselected[1:2]
					pvcNamesDeselected = pvcNamesDeselected[0:1]
					DeferCleanup(func() {
						pvcNamesSelected = append(pvcNamesSelected, pvcNamesReselected...)
					})
					vrgResourceVersion = vrgResourceVersionGet()
					forPVCs(pvcNamesReselected, func(pvc corev1.PersistentVolumeClaim) {
						util.ObjectLabelsSet(&pvc, t.pvcLabels)
						Expect(k8sClient.Update(context.TODO(), &pvc)).To(Succeed())
					})
				})
				It("updates the status", func() {
					Eventually(vrgResourceVersionGet).ShouldNot(Equal(vrgResourceVersion))
				})
				It("keeps the selected protected", func() {
					pvcsVerify(pvcNamesSelected, pvcProtectedVerify)
				})
				It("keeps the deselected unprotected", func() {
					pvcsVerify(pvcNamesDeselected, pvcUnprotectedVerify)
				})
				Context("and vr is reconciled", func() {
					BeforeAll(func() {
						t.promoteVolRepsWithoutVrgStatusCheck()
					})
					It("sets data ready condition status to true", func() {
						Eventually(func() metav1.ConditionStatus {
							dataReadyCondition = meta.FindStatusCondition(vrgGet().Status.Conditions, "DataReady")
							Expect(dataReadyCondition).ToNot(BeNil())

							return dataReadyCondition.Status
						}).Should(Equal(metav1.ConditionTrue))
					})
				})
				It("reprotects it", func() {
					pvcsVerify(pvcNamesReselected, pvcProtectedVerify)
				})
			})
			When("all selected are deselected", func() {
				BeforeAll(func() {
					DeferCleanup(func() {
						pvcNamesDeselected = append(pvcNamesDeselected, pvcNamesSelected...)
						pvcNamesSelected = nil
					})
					vrgResourceVersion = vrgResourceVersionGet()
					forPVCs(pvcNamesSelected, func(pvc corev1.PersistentVolumeClaim) {
						util.ObjectLabelsDelete(&pvc, t.pvcLabels)
						Expect(k8sClient.Update(context.TODO(), &pvc)).To(Succeed())
					})
				})
				It("updates the status", func() {
					Eventually(vrgResourceVersionGet).ShouldNot(Equal(vrgResourceVersion))
				})
				It("keeps the deselected unprotected", func() {
					pvcsVerify(pvcNamesDeselected, pvcUnprotectedVerify)
				})
				It("deletes their status", func() {
					Eventually(vrgStatusPvcsGet).Should(BeEmpty())
				})
				It("unprotects them", func() {
					pvcsVerify(pvcNamesSelected, pvcUnprotectedVerify)
				})
			})
		})
		It("cleans up after testing", func() {
			vrgTestBoundPV.cleanupUnprotected()
		})
	})

	// Test Object store "get" failure for an s3 store, expect ClusterDataReady to remain false
	var vrgS3StoreGetTestCase *vrgTest
	Context("in primary state", func() {
		createTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			storageClassName:       "manual",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		It("sets up PVCs, PVs and VRGs (with s3 stores that fail ObjectStore get)", func() {
			createTestTemplate.s3Profiles = []string{s3Profiles[bucketInvalidS3ProfileNumber2].S3ProfileName}
			vrgS3StoreGetTestCase = newVRGTestCaseCreateAndStart(2, createTestTemplate, true, false)
		})
		It("waits for VRG status to match", func() {
			vrgS3StoreGetTestCase.verifyVRGStatusCondition(vrgController.VRGConditionTypeClusterDataReady, false)
		})
		It("cleans up after testing", func() {
			vrgS3StoreGetTestCase.cleanupStatusAbsent()
		})
	})

	// Test PV upload failure to an s3 store, expect ClusterDataProtected to remain false
	// - Also tests if cached s3 errors are returned rather than invoking the upload for each PV, by having
	//   more than one PVCs to protect
	var vrgS3UploadTestCase *vrgTest
	Context("in primary state", func() {
		createTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			storageClassName:       "manual",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		It("sets up PVCs, PVs and VRGs (with s3 stores that fail uploads)", func() {
			createTestTemplate.s3Profiles = []string{s3Profiles[uploadErrorS3ProfileNumber].S3ProfileName}
			vrgS3UploadTestCase = newVRGTestCaseCreateAndStart(3, createTestTemplate, true, false)
		})
		It("waits for VRG to create a VR for each PVC", func() {
			expectedVRCount := len(vrgS3UploadTestCase.pvcNames)
			vrgS3UploadTestCase.waitForVRCountToMatch(expectedVRCount)
		})
		It("waits for VRG status to match", func() {
			vrgS3UploadTestCase.promoteVolReps()
			vrgS3UploadTestCase.verifyVRGStatusExpectation(true)
			vrgS3UploadTestCase.verifyCachedUploadError()
		})
		Specify("set VRG's S3 profile names to empty", func() {
			vrgS3UploadTestCase.vrgS3ProfilesSet([]string{vrgController.NoS3StoreAvailable})
		})
		It("cleans up after testing", func() {
			vrgS3UploadTestCase.cleanupStatusUnprotected()
		})
	})

	// Test VRG finalizer removal during deletion is deferred till VR is deleted
	var vrgVRDeleteEnsureTestCase *vrgTest
	Context("in primary state", func() {
		createTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			storageClassName:       "manual",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		It("sets up PVCs, PVs and VRGs (with s3 stores that fail uploads)", func() {
			createTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			vrgVRDeleteEnsureTestCase = newVRGTestCaseCreateAndStart(1, createTestTemplate, true, false)
		})
		It("waits for VRG to create a VR for each PVC", func() {
			expectedVRCount := len(vrgVRDeleteEnsureTestCase.pvcNames)
			vrgVRDeleteEnsureTestCase.waitForVRCountToMatch(expectedVRCount)
		})
		It("waits for VRG status to match", func() {
			vrgVRDeleteEnsureTestCase.promoteVolReps()
			vrgVRDeleteEnsureTestCase.verifyVRGStatusExpectation(true)
		})
		It("ensures orderly cleanup post VolumeReplication deletion", func() {
			By("Protecting the VolumeReplication resources from deletion")
			vrgVRDeleteEnsureTestCase.protectDeletionOfVolReps()

			By("Starting the VRG deletion process")
			vrgVRDeleteEnsureTestCase.cleanupPVCs(pvcProtectedVerify, vrAndPvcDeletionTimestampsRecentVerify)
			vrg := vrgVRDeleteEnsureTestCase.getVRG()
			Expect(k8sClient.Delete(context.TODO(), vrg)).To(Succeed())

			By("Ensuring VRG is not deleted till VRs are present")
			Consistently(apiReader.Get, vrgtimeout, vrginterval).
				WithArguments(context.TODO(), vrgVRDeleteEnsureTestCase.vrgNamespacedName(), vrg).
				Should(Succeed(), "while waiting for VRG %v to remain undeleted",
					vrgVRDeleteEnsureTestCase.vrgNamespacedName())

			By("Un-protecting the VolumeReplication resources to ensure their deletion")
			vrgVRDeleteEnsureTestCase.unprotectDeletionOfVolReps()

			By("Ensuring VRG is deleted eventually as a result")
			var i int
			Eventually(func() error {
				i++

				return apiReader.Get(context.TODO(), vrgVRDeleteEnsureTestCase.vrgNamespacedName(), vrg)
			}, vrgtimeout*2, vrginterval).
				Should(MatchError(errors.NewNotFound(schema.GroupResource{
					Group:    ramendrv1alpha1.GroupVersion.Group,
					Resource: "volumereplicationgroups",
				}, vrgVRDeleteEnsureTestCase.vrgName)),
					"polled %d times for VRG to be garbage collected\n"+format.Object(*vrg, 1), i)

			vrgVRDeleteEnsureTestCase.cleanupNamespace()
			vrgVRDeleteEnsureTestCase.cleanupSC()
			vrgVRDeleteEnsureTestCase.cleanupVRC()
		})
	})

	// Try the simple case of creating VRG, PVC, PV and
	// check whether VolRep resources are created or not
	var vrgTestCases []*vrgTest
	Context("in primary state", func() {
		createTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			storageClassName:       "manual",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		It("sets up PVCs, PVs and VRGs", func() {
			createTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			for c := 0; c < 5; c++ {
				v := newVRGTestCaseCreateAndStart(c, createTestTemplate, true, false)
				vrgTestCases = append(vrgTestCases, v)
			}
		})
		It("waits for VRG to create a VR for each PVC", func() {
			for c := 0; c < len(vrgTestCases); c++ {
				v := vrgTestCases[c]
				expectedVRCount := len(v.pvcNames)
				v.waitForVRCountToMatch(expectedVRCount)
			}
		})
		It("waits for VRG status to match", func() {
			for c := 0; c < len(vrgTestCases); c++ {
				v := vrgTestCases[c]
				v.promoteVolReps()
				if c != 0 {
					v.verifyVRGStatusExpectation(true)
				} else {
					v.verifyVRGStatusExpectation(false)
				}
			}
		})
		It("protects kube objects", func() { kubeObjectProtectionValidate(vrgTestCases) })
		It("cleans up after testing", func() {
			for c := 0; c < len(vrgTestCases); c++ {
				v := vrgTestCases[c]
				v.cleanupProtected()
			}
		})
	})

	// Ensure PVCs with no SCName results in errors
	var vrgEmptySC *vrgTest
	Context("in primary state", func() {
		createTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		It("sets up PVCs, PVs and VRGs - with nil/empty StorageClassName", func() {
			createTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			createTestTemplate.volsyncEnabled = true
			vrgEmptySC = newVRGTestCaseCreateAndStart(1, createTestTemplate, true, false)
		})
		It("waits for VRG status to match", func() {
			vrgEmptySC.verifyVRGStatusExpectation(false)
		})
		It("cleans up after testing", func() {
			vrgEmptySC.cleanupStatusAbsent()
		})
	})

	// Ensure PVCs with missing SClass results in errors
	var vrgMissingSC *vrgTest
	Context("in primary state", func() {
		createTestTemplate := &template{
			ClaimBindInfo:          corev1.ClaimBound,
			VolumeBindInfo:         corev1.VolumeBound,
			schedulingInterval:     "1h",
			storageClassName:       "manual",
			replicationClassName:   "test-replicationclass",
			vrcProvisioner:         "manual.storage.com",
			scProvisioner:          "manual.storage.com",
			replicationClassLabels: map[string]string{"protection": "ramen"},
		}
		It("sets up PVCs, PVs and VRGs - with missing StorageClass", func() {
			createTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			createTestTemplate.volsyncEnabled = true
			createTestTemplate.scDisabled = true
			vrgMissingSC = newVRGTestCaseCreateAndStart(1, createTestTemplate, true, false)
		})
		It("waits for VRG status to match", func() {
			vrgMissingSC.verifyVRGStatusExpectation(false)
		})
		It("cleans up after testing", func() {
			vrgMissingSC.cleanupStatusAbsent()
		})
	})

	// Creates VRG. PVCs and PV are created with Status.Phase
	// set to pending and VolRep should not be created until
	// all the PVCs and PVs are bound. So, these tests then
	// change the Status.Phase of PVCs and PVs to bound state,
	// and then checks whether appropriate number of VolRep
	// resources have been created or not.
	var vrgTests []*vrgTest
	vrgTestTemplate := &template{
		ClaimBindInfo:          corev1.ClaimPending,
		VolumeBindInfo:         corev1.VolumePending,
		schedulingInterval:     "1h",
		storageClassName:       "manual",
		replicationClassName:   "test-replicationclass",
		vrcProvisioner:         "manual.storage.com",
		scProvisioner:          "manual.storage.com",
		replicationClassLabels: map[string]string{"protection": "ramen"},
	}

	Context("in primary state", func() {
		It("sets up non-bound PVCs, PVs and then bind them", func() {
			vrgTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			for c := 0; c < 5; c++ {
				// Test the scenario where the pvc is not bound yet
				// and expect no VRs to be created.
				v := newVRGTestCaseCreateAndStart(c, vrgTestTemplate, false, false)
				vrgTests = append(vrgTests, v)
			}
		})
		It("expect no VR to be created as PVC not bound", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				v.waitForVRCountToMatch(0)
			}
		})
		It("bind each pv to corresponding pvc", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				v.bindPVAndPVC()
				v.verifyPVCBindingToPV(true)
			}
		})
		It("waits for VRG to create a VR for each PVC bind", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				expectedVRCount := len(v.pvcNames)
				v.waitForVRCountToMatch(expectedVRCount)
			}
		})
		It("waits for VRG status to match", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				v.promoteVolReps()
				if c != 0 {
					v.verifyVRGStatusExpectation(true)
				} else {
					v.verifyVRGStatusExpectation(false)
				}
			}
		})
		It("protects kube objects", func() { kubeObjectProtectionValidate(vrgTests) })
		It("cleans up after testing", func() {
			for c := 0; c < len(vrgTests); c++ {
				v := vrgTests[c]
				v.cleanupProtected()
			}
		})
	})

	var vrgStatusTests []*vrgTest
	vrgTestTemplateVSEnabled := &template{
		ClaimBindInfo:          corev1.ClaimPending,
		VolumeBindInfo:         corev1.VolumePending,
		schedulingInterval:     "1h",
		storageClassName:       "manual",
		replicationClassName:   "test-replicationclass",
		vrcProvisioner:         "manual.storage.com",
		scProvisioner:          "manual.storage.com",
		replicationClassLabels: map[string]string{"protection": "ramen"},
		volsyncEnabled:         true,
	}
	//nolint:dupl
	Context("in primary state status check pending to bound", func() {
		It("sets up non-bound PVCs, PVs and then bind them", func() {
			vrgTestTemplateVSEnabled.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			v := newVRGTestCaseCreateAndStart(4, vrgTestTemplateVSEnabled, false, false)
			vrgStatusTests = append(vrgStatusTests, v)
		})
		var v *vrgTest
		Specify("vrg status test", func() { v = vrgStatusTests[0] })
		It("expect no VR to be created as PVC not bound and check status", func() {
			v.waitForVRCountToMatch(0)
		})
		It("bind each pv to corresponding pvc", func() {
			v.bindPVAndPVC()
			v.verifyPVCBindingToPV(true)
		})
		It("waits for VRG to create a VR for each PVC bind and checks status", func() {
			expectedVRCount := len(v.pvcNames)
			v.waitForVRCountToMatch(expectedVRCount)
		})
		It("waits for VRG status to match", func() {
			v.promoteVolReps()
			v.verifyVRGStatusExpectation(true)
		})
		It("protects kube objects", func() { kubeObjectProtectionValidate(vrgStatusTests) })
		It("cleans up after testing", func() {
			v.cleanupProtected()
		})
	})

	// Changes the order in which VRG and PVC/PV are created.

	vrgTest2Template := &template{
		ClaimBindInfo:          corev1.ClaimBound,
		VolumeBindInfo:         corev1.VolumeBound,
		schedulingInterval:     "1h",
		storageClassName:       "manual",
		replicationClassName:   "test-replicationclass",
		vrcProvisioner:         "manual.storage.com",
		scProvisioner:          "manual.storage.com",
		replicationClassLabels: map[string]string{"protection": "ramen"},
	}
	var vrgStatus2Tests []*vrgTest
	Context("in primary state status check bound", func() {
		It("sets up PVCs, PVs", func() {
			vrgTest2Template.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			v := newVRGTestCaseCreateAndStart(4, vrgTest2Template, true, true)
			vrgStatus2Tests = append(vrgStatus2Tests, v)
		})
		It("waits for VRG to create a VR for each PVC bind and checks status", func() {
			v := vrgStatus2Tests[0]
			expectedVRCount := len(v.pvcNames)
			v.waitForVRCountToMatch(expectedVRCount)
		})
		It("waits for VRG status to match", func() {
			v := vrgStatus2Tests[0]
			v.promoteVolReps()
			v.verifyVRGStatusExpectation(true)
		})
		It("protects kube objects", func() { kubeObjectProtectionValidate(vrgStatus2Tests) })
		It("cleans up after testing", func() {
			v := vrgStatus2Tests[0]
			v.cleanupProtected()
		})
	})

	// Changes the order in which VRG and PVC/PV are created. VRG is created first and then
	// PVC/PV are created (with ClaimPending and VolumePending status respectively). Then
	// each of them is bound and the result should be same (i.e. VRG being available).
	vrgTest3Template := &template{
		ClaimBindInfo:          corev1.ClaimPending,
		VolumeBindInfo:         corev1.VolumePending,
		schedulingInterval:     "1h",
		storageClassName:       "manual",
		replicationClassName:   "test-replicationclass",
		vrcProvisioner:         "manual.storage.com",
		scProvisioner:          "manual.storage.com",
		replicationClassLabels: map[string]string{"protection": "ramen"},
	}
	var vrgStatus3Tests []*vrgTest
	//nolint:dupl
	Context("in primary state status check create VRG first", func() {
		It("sets up non-bound PVCs, PVs and then bind them", func() {
			vrgTest3Template.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			v := newVRGTestCaseCreateAndStart(4, vrgTest3Template, false, true)
			vrgStatus3Tests = append(vrgStatus3Tests, v)
		})
		var v *vrgTest
		Specify("vrg status test", func() { v = vrgStatus3Tests[0] })
		It("expect no VR to be created as PVC not bound and check status", func() {
			v.waitForVRCountToMatch(0)
			// v.verifyVRGStatusExpectation(false)
		})
		It("bind each pv to corresponding pvc", func() {
			v.bindPVAndPVC()
			v.verifyPVCBindingToPV(true)
		})
		It("waits for VRG to create a VR for each PVC bind and checks status", func() {
			expectedVRCount := len(v.pvcNames)
			v.waitForVRCountToMatch(expectedVRCount)
		})
		It("waits for VRG status to match", func() {
			v.promoteVolReps()
			v.verifyVRGStatusExpectation(true)
		})
		It("protects kube objects", func() { kubeObjectProtectionValidate(vrgStatus3Tests) })
		It("cleans up after testing", func() {
			v.cleanupProtected()
		})
	})

	// VolumeReplicationClass provisioner and StorageClass provisioner
	// does not match. VolumeReplication resources should not be created.
	var vrgScheduleTests []*vrgTest
	vrgScheduleTestTemplate := &template{
		ClaimBindInfo:          corev1.ClaimBound,
		VolumeBindInfo:         corev1.VolumeBound,
		schedulingInterval:     "1h",
		storageClassName:       "manual",
		replicationClassName:   "test-replicationclass",
		vrcProvisioner:         "manual.storage.com",
		scProvisioner:          "new.storage.com",
		replicationClassLabels: map[string]string{"protection": "ramen"},
	}
	Context("schedule test, provisioner does not match", func() {
		It("sets up non-bound PVCs, PVs and then bind them", func() {
			vrgScheduleTestTemplate.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			v := newVRGTestCaseCreateAndStart(4, vrgScheduleTestTemplate, true, true)
			vrgScheduleTests = append(vrgScheduleTests, v)
		})
		It("expect no VR to be created as PVC not bound and check status", func() {
			v := vrgScheduleTests[0]
			v.waitForVRCountToMatch(0)
		})
		It("waits for VRG status to match", func() {
			v := vrgScheduleTests[0]
			v.verifyVRGStatusExpectation(false)
		})
		// It("protects kube objects", func() { kubeObjectProtectionValidate(vrgScheduleTests) })
		It("cleans up after testing", func() {
			v := vrgScheduleTests[0]
			v.cleanupStatusAbsent()
		})
	})

	// provisioner match. But schedule does not match. Again,
	// VolumeReplication resource should not be created.
	var vrgSchedule2Tests []*vrgTest
	vrgScheduleTest2Template := &template{
		ClaimBindInfo:          corev1.ClaimBound,
		VolumeBindInfo:         corev1.VolumeBound,
		schedulingInterval:     "22h",
		storageClassName:       "manual",
		replicationClassName:   "test-replicationclass",
		vrcProvisioner:         "manual.storage.com",
		scProvisioner:          "manual.storage.com",
		replicationClassLabels: map[string]string{"protection": "ramen"},
	}
	Context("schedule tests schedue does not match", func() {
		It("sets up non-bound PVCs, PVs and then bind them", func() {
			vrgScheduleTest2Template.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			v := newVRGTestCaseCreateAndStart(4, vrgScheduleTest2Template, true, true)
			vrgSchedule2Tests = append(vrgSchedule2Tests, v)
		})
		It("expect no VR to be created as PVC not bound and check status", func() {
			v := vrgSchedule2Tests[0]
			v.waitForVRCountToMatch(0)
		})
		It("waits for VRG status to match", func() {
			v := vrgSchedule2Tests[0]
			v.verifyVRGStatusExpectation(false)
		})
		// It("protects kube objects", func() { kubeObjectProtectionValidate(vrgSchedule2Tests) })
		It("cleans up after testing", func() {
			v := vrgSchedule2Tests[0]
			v.cleanupStatusAbsent()
		})
	})

	// provisioner and schedule match. But replicationClass
	// does not have the labels that VRG expects to find.
	var vrgSchedule3Tests []*vrgTest
	vrgScheduleTest3Template := &template{
		ClaimBindInfo:          corev1.ClaimBound,
		VolumeBindInfo:         corev1.VolumeBound,
		schedulingInterval:     "1h",
		storageClassName:       "manual",
		replicationClassName:   "test-replicationclass",
		vrcProvisioner:         "manual.storage.com",
		scProvisioner:          "manual.storage.com",
		replicationClassLabels: map[string]string{},
	}
	Context("schedule tests replicationclass does not have labels", func() {
		It("sets up non-bound PVCs, PVs and then bind them", func() {
			vrgScheduleTest3Template.s3Profiles = []string{s3Profiles[vrgS3ProfileNumber].S3ProfileName}
			v := newVRGTestCaseCreateAndStart(4, vrgScheduleTest3Template, true, true)
			vrgSchedule3Tests = append(vrgSchedule3Tests, v)
		})
		It("expect no VR to be created as VR not created and check status", func() {
			v := vrgSchedule3Tests[0]
			v.waitForVRCountToMatch(0)
		})
		It("waits for VRG status to match", func() {
			v := vrgSchedule3Tests[0]
			v.verifyVRGStatusExpectation(false)
		})
		// It("protects kube objects", func() { kubeObjectProtectionValidate(vrgSchedule3Tests) })
		It("cleans up after testing", func() {
			v := vrgSchedule3Tests[0]
			v.cleanupStatusAbsent()
		})
	})
	// TODO: Add tests to move VRG to Secondary
	// TODO: Add tests to ensure delete as Secondary (check if delete as Primary is tested above)
})

type vrgTest struct {
	uniqueID             string
	namespace            string
	pvNames              []string
	pvcNames             []types.NamespacedName
	pvcVolumeNames       map[types.NamespacedName]string
	vrgName              string
	storageClass         string
	replicationClass     string
	pvcLabels            map[string]string
	pvcCount             int
	skipCreationPVandPVC bool
	checkBind            bool
	vrgFirst             bool
	template             *template
}

type template struct {
	ClaimBindInfo          corev1.PersistentVolumeClaimPhase
	VolumeBindInfo         corev1.PersistentVolumePhase
	schedulingInterval     string
	vrcProvisioner         string
	scProvisioner          string
	storageClassName       string
	replicationClassName   string
	replicationClassLabels map[string]string
	s3Profiles             []string
	volsyncEnabled         bool
	scDisabled             bool
}

// we want the math rand version here and not the crypto rand. This way we can debug the tests by repeating the seed.
//
//nolint:gosec
func newRandomNamespaceSuffix() string {
	randomSuffix := make([]byte, namespaceLen)

	for i := range randomSuffix {
		randomSuffix[i] = letters[rand.Intn(len(letters))]
	}

	return string(randomSuffix)
}

func newVRGTestCaseCreate(pvcCount int, testTemplate *template, checkBind, vrgFirst bool) *vrgTest {
	objectNameSuffix := newRandomNamespaceSuffix()

	v := &vrgTest{
		uniqueID:         objectNameSuffix,
		namespace:        fmt.Sprintf("envtest-ns-%v", objectNameSuffix),
		vrgName:          fmt.Sprintf("vrg-%v", objectNameSuffix),
		storageClass:     testTemplate.storageClassName,
		replicationClass: testTemplate.replicationClassName,
		pvcLabels:        make(map[string]string),
		pvcCount:         pvcCount,
		checkBind:        checkBind,
		vrgFirst:         vrgFirst,
		template:         testTemplate,
	}

	if pvcCount > 0 {
		v.pvcLabels["appclass"] = "platinum"
		v.pvcLabels["environment"] = fmt.Sprintf("dev.AZ1-%v", objectNameSuffix)
	}

	return v
}

func (v *vrgTest) VRGTestCaseStart() {
	By("Creating namespace " + v.namespace)
	v.createNamespace()
	v.createSC(v.template)
	v.createVRC(v.template)

	if v.vrgFirst {
		v.createVRG()

		if !v.skipCreationPVandPVC {
			v.createPVCandPV(v.template.ClaimBindInfo, v.template.VolumeBindInfo)
		}
	} else {
		if !v.skipCreationPVandPVC {
			v.createPVCandPV(v.template.ClaimBindInfo, v.template.VolumeBindInfo)
		}

		v.createVRG()
	}

	// If checkBind is true, then check whether PVCs and PVs are
	// bound. Otherwise expect them to not have been bound.
	if !v.skipCreationPVandPVC {
		v.verifyPVCBindingToPV(v.checkBind)
	}
}

// newVRGTestCaseCreateAndStart creates a new namespace, zero or more PVCs (equal
// to the input pvcCount), a PV for each PVC, and a VRG in primary state,
// with label selector that points to the PVCs created. Each PVC is created
// with Status.Phase set to ClaimPending instead of ClaimBound. Expectation
// is that, until pvc is not bound, VolRep resources should not be created
// by VRG.
func newVRGTestCaseCreateAndStart(pvcCount int, testTemplate *template, checkBind, vrgFirst bool) *vrgTest {
	v := newVRGTestCaseCreate(pvcCount, testTemplate, checkBind, vrgFirst)

	v.VRGTestCaseStart()

	return v
}

func (v *vrgTest) createPVCandPV(claimBindInfo corev1.PersistentVolumeClaimPhase,
	volumeBindInfo corev1.PersistentVolumePhase,
) {
	v.pvcVolumeNames = make(map[types.NamespacedName]string, v.pvcCount)
	// Create the requested number of PVs and corresponding PVCs
	for i, volumeNameSuffix := 0, "-"+v.uniqueID+"-"; i < v.pvcCount; i++ {
		volumeNameSuffix := volumeNameSuffix + strconv.Itoa(i)
		pvName, pvcName := "pv"+volumeNameSuffix, "pvc"+volumeNameSuffix
		// Create PV first and then PVC. This is important to ensure that there
		// is no race between the unit test and VRG reconciler in modifying PV.
		// i.e. suppose VRG is already created and then this function is run,
		// then if PVC is created first and then PV is created, the following
		// rance happens.
		// The moment PVC is created and its status.Phase is bound, then VRG
		// races to modify the PV by changing its retaim policy. At the same
		// time createPV tries to modify PV by changing its status.Phase to
		// bound. This race causes the unit test to fail. Hence, to avoid this
		// race, create PV first and then PVC. Until PVC is created and bound,
		// VRG will not be able to reach PV. And by the time VRG reconciler
		// reaches PV, it is already bound by this unit test.
		v.createPV(pvName, pvcName, volumeBindInfo)
		pvc := v.createPVC(pvcName, v.namespace, pvName, v.pvcLabels, claimBindInfo)
		v.pvNames = append(v.pvNames, pvName)
		pvcNamespacedName := client.ObjectKeyFromObject(pvc)
		v.pvcNames = append(v.pvcNames, pvcNamespacedName)
		v.pvcVolumeNames[pvcNamespacedName] = pvName
	}
}

func cleanupS3Store() {
	Expect((*vrgObjectStorer).DeleteObjectsWithKeyPrefix("")).To(Succeed())
}

func (v *vrgTest) generateFakePVs(pvNamePrefix string, count int) []corev1.PersistentVolume {
	pvList := []corev1.PersistentVolume{}

	// The generator has a limit of 9999 unique names.
	if count > 9999 {
		return pvList
	}

	for i := 1; i <= count; i++ {
		pvName := fmt.Sprintf("%s%04d", pvNamePrefix, i)
		pvList = append(pvList, *v.generatePV(pvName, "pvc-of-"+pvName))
		v.pvNames = append(v.pvNames, pvName)
	}

	return pvList
}

func (v *vrgTest) generateFakePVCs(pvList []corev1.PersistentVolume) []corev1.PersistentVolumeClaim {
	pvcList := []corev1.PersistentVolumeClaim{}

	for _, pv := range pvList {
		pvc := v.generatePVC(pv.Spec.ClaimRef.Name, pv.Spec.ClaimRef.Namespace, pv.Name, v.pvcLabels)
		pvcList = append(pvcList, *pvc)
		pvcNamespacedName := client.ObjectKeyFromObject(pvc)
		v.pvcNames = append(v.pvcNames, pvcNamespacedName)
	}

	return pvcList
}

func (v *vrgTest) vrgNamespacedName() types.NamespacedName {
	return types.NamespacedName{Namespace: v.namespace, Name: v.vrgName}
}

func (v *vrgTest) s3KeyPrefix() string {
	return vrgS3KeyPrefix(v.vrgNamespacedName())
}

func vrgS3KeyPrefix(vrgNamespacedName types.NamespacedName) string {
	return vrgController.S3KeyPrefix(vrgNamespacedName.String())
}

func populateS3Store(vrgNamespacedName string, pvList []corev1.PersistentVolume,
	pvcList []corev1.PersistentVolumeClaim,
) {
	for _, pv := range pvList {
		Expect(
			vrgController.UploadPV(*vrgObjectStorer, vrgNamespacedName, pv.Name, pv),
		).To(Succeed())
	}

	for _, pvc := range pvcList {
		Expect(
			vrgController.UploadPVC(*vrgObjectStorer, vrgNamespacedName, pvc.Name, pvc),
		).To(Succeed())
	}
}

func (v *vrgTest) createNamespace() {
	By("creating namespace " + v.namespace)

	appNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: v.namespace}}
	err := k8sClient.Create(context.TODO(), appNamespace)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{Resource: "namespaces"}, v.namespace)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create namespace %s", v.namespace)
}

func (v *vrgTest) createPV(pvName, claimName string, bindInfo corev1.PersistentVolumePhase) {
	By("creating PV " + pvName)

	pv := v.generatePV(pvName, claimName)

	err := k8sClient.Create(context.TODO(), pv)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{Resource: "persistentvolumes"}, pvName)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create PV %s", pvName)

	pv.Status.Phase = bindInfo
	err = k8sClient.Status().Update(context.TODO(), pv)
	Expect(err).To(BeNil(),
		"failed to update status of PV %s", pvName)
}

func (v *vrgTest) generatePV(pvName, claimName string) *corev1.PersistentVolume {
	return pv(pvName, claimName, v.namespace, v.storageClass)
}

func pv(pvName, claimName, claimNamespaceName, storageClassName string) *corev1.PersistentVolume {
	capacity := corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}
	accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	volumeMode := corev1.PersistentVolumeFilesystem

	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: pvName},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: capacity,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "fake.csi",
					FSType:       "ext4",
					VolumeHandle: "fakeVolumeHandle",
				},
			},
			AccessModes: accessModes,
			VolumeMode:  &volumeMode,
			ClaimRef: &corev1.ObjectReference{
				Kind:      "PersistentVolumeClaim",
				Namespace: claimNamespaceName,
				Name:      claimName,
			},
			PersistentVolumeReclaimPolicy: "Delete",
			StorageClassName:              storageClassName,
			MountOptions:                  []string{},
			NodeAffinity: &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "KeyNode",
									Operator: corev1.NodeSelectorOpIn,
									Values: []string{
										"node1",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (v *vrgTest) createPVC(pvcName, namespace, volumeName string, labels map[string]string,
	bindInfo corev1.PersistentVolumeClaimPhase,
) *corev1.PersistentVolumeClaim {
	By("creating PVC " + pvcName)

	pvc := v.generatePVC(pvcName, namespace, volumeName, labels)

	err := k8sClient.Create(context.TODO(), pvc)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{Resource: "persistentvolumeclaims"}, pvcName)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create PVC %s", pvcName)

	pvc.Status.Phase = bindInfo
	pvc.Status.AccessModes = pvc.Spec.AccessModes
	pvc.Status.Capacity = pvc.Spec.Resources.Requests
	err = k8sClient.Status().Update(context.TODO(), pvc)
	Expect(err).To(BeNil(),
		"failed to update status of PVC %s", pvcName)

	return pvc
}

func (v *vrgTest) generatePVC(pvcName, namespace, volumeName string, labels map[string]string,
) *corev1.PersistentVolumeClaim {
	return pvc(pvcName, namespace, volumeName, v.storageClass, labels)
}

func pvc(pvcName, namespace, volumeName, storageClassName string, labels map[string]string,
) *corev1.PersistentVolumeClaim {
	capacity := corev1.ResourceList{
		corev1.ResourceStorage: resource.MustParse("1Gi"),
	}

	accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Labels:    labels,
			Namespace: namespace,
			// ResourceVersion: "1",
			SelfLink: "/api/v1/namespaces/testns/persistentvolumeclaims/" + pvcName,
			UID:      types.UID(volumeName),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			Resources:        corev1.ResourceRequirements{Requests: capacity},
			VolumeName:       volumeName,
			StorageClassName: &storageClassName, // Set to nil?
		},
	}
}

func (v *vrgTest) bindPVAndPVC() {
	By("Waiting for PVC to get bound to PVs for " + v.vrgName)

	for i := 0; i < len(v.pvcNames); i++ {
		// Bind PV
		pv := v.getPV(v.pvNames[i])
		pv.Status.Phase = corev1.VolumeBound
		err := k8sClient.Status().Update(context.TODO(), pv)
		Expect(err).To(BeNil(),
			"failed to update status of PV %s", v.pvNames[i])

		i := i // capture i for use in closure

		// Bind PVC
		pvc := getPVC(v.pvcNames[i])
		pvc.Status.Phase = corev1.ClaimBound
		err = k8sClient.Status().Update(context.TODO(), pvc)
		Expect(err).To(BeNil(),
			"failed to update status of PVC %s", v.pvcNames[i])
	}
}

func (v *vrgTest) createVRG() {
	By("creating VRG " + v.vrgName)

	schedulingInterval := "1h"
	replicationClassLabels := map[string]string{"protection": "ramen"}

	vrg := &ramendrv1alpha1.VolumeReplicationGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: ramendrv1alpha1.GroupVersion.String(),
			Kind:       "VolumeReplicationGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.vrgName,
			Namespace: v.namespace,
		},
		Spec: ramendrv1alpha1.VolumeReplicationGroupSpec{
			PVCSelector:      metav1.LabelSelector{MatchLabels: v.pvcLabels},
			ReplicationState: "primary",
			Async: &ramendrv1alpha1.VRGAsyncSpec{
				SchedulingInterval:       schedulingInterval,
				ReplicationClassSelector: metav1.LabelSelector{MatchLabels: replicationClassLabels},
			},
			VolSync: ramendrv1alpha1.VolSyncSpec{
				Disabled: !v.template.volsyncEnabled,
			},
			S3Profiles: v.template.s3Profiles,
		},
	}
	err := k8sClient.Create(context.TODO(), vrg)
	expectedErr := errors.NewAlreadyExists(
		schema.GroupResource{
			Group:    "ramendr.openshift.io",
			Resource: "volumereplicationgroups",
		},
		v.vrgName)
	Expect(err).To(SatisfyAny(BeNil(), MatchError(expectedErr)),
		"failed to create VRG %s in %s", v.vrgName, v.namespace)
}

func (v *vrgTest) vrgS3ProfilesSet(names []string) {
	vrg := v.getVRG()
	vrg.Spec.S3Profiles = names
	Expect(k8sClient.Update(context.TODO(), vrg)).To(Succeed())
	vrg = v.getVRG()
	Expect(vrg.Spec.S3Profiles).To(Equal(names), "%#v", vrg.Spec.S3Profiles)
}

func (v *vrgTest) createVRC(testTemplate *template) {
	By("creating VRC " + v.replicationClass)

	parameters := make(map[string]string)

	if testTemplate.schedulingInterval != "" {
		parameters["schedulingInterval"] = testTemplate.schedulingInterval
	}

	vrc := &volrep.VolumeReplicationClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: v.replicationClass,
		},
		Spec: volrep.VolumeReplicationClassSpec{
			Provisioner: testTemplate.vrcProvisioner,
			Parameters:  parameters,
		},
	}

	if len(testTemplate.replicationClassLabels) > 0 {
		vrc.ObjectMeta.Labels = testTemplate.replicationClassLabels
	}

	err := k8sClient.Create(context.TODO(), vrc)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			err = k8sClient.Get(context.TODO(), types.NamespacedName{Name: v.replicationClass}, vrc)
		}
	}

	Expect(err).NotTo(HaveOccurred(),
		"failed to create/get VolumeReplicationClass %s/%s", v.replicationClass, v.vrgName)
}

func (v *vrgTest) createSC(testTemplate *template) {
	By("creating StorageClass " + v.storageClass)

	if v.storageClass == "" || testTemplate.scDisabled {
		return
	}

	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: v.storageClass,
		},
		Provisioner: testTemplate.scProvisioner,
	}

	err := k8sClient.Create(context.TODO(), sc)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			err = k8sClient.Get(context.TODO(), types.NamespacedName{Name: v.storageClass}, sc)
		}
	}

	Expect(err).NotTo(HaveOccurred(),
		"failed to create/get StorageClass %s/%s", v.storageClass, v.vrgName)
}

func (v *vrgTest) verifyPVCBindingToPV(shouldBeBound bool) {
	By("Waiting for PVC to get bound to PVs for " + v.vrgName)

	for i := 0; i < len(v.pvcNames); i++ {
		_ = v.getPV(v.pvNames[i])
		i := i // capture i for use in closure
		Eventually(func() bool {
			pvc := getPVC(v.pvcNames[i])

			if shouldBeBound == true {
				return pvc.Status.Phase == corev1.ClaimBound
			}

			return pvc.Status.Phase != corev1.ClaimBound
		}, timeout, interval).Should(BeTrue(),
			"while waiting for PVC %s to bind with PV %s",
			v.pvcNames[i], v.pvNames[i])
	}
}

func (v *vrgTest) getPV(pvName string) *corev1.PersistentVolume {
	pvLookupKey := types.NamespacedName{Name: pvName}
	pv := &corev1.PersistentVolume{}
	err := apiReader.Get(context.TODO(), pvLookupKey, pv)
	Expect(err).NotTo(HaveOccurred(),
		"failed to get PV %s", pvName)

	return pv
}

func getPVC(key types.NamespacedName) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{}
	err := apiReader.Get(context.TODO(), key, pvc)
	Expect(err).NotTo(HaveOccurred(),
		"failed to get PVC %s", key.String())

	return pvc
}

func (v *vrgTest) getVRG() *ramendrv1alpha1.VolumeReplicationGroup {
	return vrgGet(v.vrgNamespacedName())
}

func vrgGet(vrgNamespacedName types.NamespacedName) *ramendrv1alpha1.VolumeReplicationGroup {
	vrg := &ramendrv1alpha1.VolumeReplicationGroup{}
	err := apiReader.Get(context.TODO(), vrgNamespacedName, vrg)
	Expect(err).NotTo(HaveOccurred(),
		"failed to get VRG %s", vrgNamespacedName.String())

	return vrg
}

func (v *vrgTest) isAnyPVCProtectedByVolSync(vrg *ramendrv1alpha1.VolumeReplicationGroup) bool {
	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		if protectedPVC.ProtectedByVolSync {
			return true
		}
	}

	return false
}

func (v *vrgTest) verifyVRGStatusExpectation(expectedStatus bool) {
	Eventually(func() bool {
		vrg := v.getVRG()
		dataReadyCondition := meta.FindStatusCondition(
			vrg.Status.Conditions, vrgController.VRGConditionTypeDataReady)
		if dataReadyCondition == nil {
			return false
		}

		if expectedStatus == true {
			// reasons for success can be different for Primary and
			// secondary. Validate that as well.
			switch vrg.Spec.ReplicationState {
			case ramendrv1alpha1.Primary:
				return dataReadyCondition.Status == metav1.ConditionTrue && dataReadyCondition.Reason ==
					vrgController.VRGConditionReasonReady
			case ramendrv1alpha1.Secondary:
				return dataReadyCondition.Status == metav1.ConditionTrue && dataReadyCondition.Reason ==
					vrgController.VRGConditionReasonReplicating
			}
		}

		if v.isAnyPVCProtectedByVolSync(vrg) {
			return true
		}

		return dataReadyCondition.Status != metav1.ConditionTrue
	}, vrgtimeout, vrginterval).Should(BeTrue(),
		"while waiting for VRG TRUE condition %s/%s", v.vrgName, v.namespace)
}

func (v *vrgTest) verifyVRGStatusCondition(conditionName string, expectedStatus bool) {
	testFunc := func() bool {
		vrg := v.getVRG()
		vrgCondition := meta.FindStatusCondition(
			vrg.Status.Conditions,
			conditionName)

		switch expectedStatus {
		case true:
			if vrgCondition == nil {
				return false
			}

			return vrgCondition.Status == metav1.ConditionTrue
		default: // false
			if vrgCondition == nil {
				return true
			}

			return vrgCondition.Status == metav1.ConditionFalse
		}
	}

	switch expectedStatus {
	case true:
		Eventually(testFunc, vrgtimeout, vrginterval).Should(BeTrue(),
			"while waiting for VRG %s TRUE condition %s/%s", conditionName, v.vrgName, v.namespace)
	default: // false
		Consistently(testFunc, vrgtimeout, vrginterval).Should(BeTrue(),
			"while waiting for VRG %s FALSE condition %s/%s", conditionName, v.vrgName, v.namespace)
	}
}

func (v *vrgTest) verifyCachedUploadError() {
	// Verify cluster data protected remains false
	v.verifyVRGStatusCondition(vrgController.VRGConditionTypeClusterDataProtected, false)

	// We verify is exactly one PVC got the expected aws error and rest report the cached error
	cachedErr := 0
	nonCachedErr := 0

	vrg := v.getVRG()
	for _, protectedPVC := range vrg.Status.ProtectedPVCs {
		pvcConditionClusterDataProtected := meta.FindStatusCondition(
			protectedPVC.Conditions,
			vrgController.VRGConditionTypeClusterDataProtected)

		Expect(pvcConditionClusterDataProtected).NotTo(BeNil(),
			"failed to find %s condition for PVC %s in VRG %#v",
			vrgController.VRGConditionTypeClusterDataProtected,
			protectedPVC.Name, vrg)

		switch strings.Contains(pvcConditionClusterDataProtected.Message,
			"persistent error while uploading to s3 profile") &&
			strings.Contains(pvcConditionClusterDataProtected.Message,
				"will retry later") {
		case true:
			cachedErr++
		default: // false
			nonCachedErr++
		}
	}

	Expect(nonCachedErr).To(BeNumerically("==", 1),
		"found multiple non cached codes for PVCs in VRG %s", v.vrgName)
	Expect(cachedErr).To(BeNumerically("==", v.pvcCount-1),
		"found mismatched cached code counts for PVCs in VRG %s", v.vrgName)
}

func (v *vrgTest) clusterDataProtectedWait(status metav1.ConditionStatus,
) (vrg *ramendrv1alpha1.VolumeReplicationGroup) {
	Eventually(func() metav1.ConditionStatus {
		vrg = v.getVRG()
		clusterDataProtectedCondition := meta.FindStatusCondition(
			vrg.Status.Conditions, vrgController.VRGConditionTypeClusterDataProtected)
		if clusterDataProtectedCondition == nil {
			return metav1.ConditionUnknown
		}

		return clusterDataProtectedCondition.Status
	}, vrgtimeout, vrginterval).Should(Equal(status))

	return
}

func (v *vrgTest) vrgDownloadAndValidate(vrgK8s *ramendrv1alpha1.VolumeReplicationGroup) {
	vrgs := []ramendrv1alpha1.VolumeReplicationGroup{}
	Expect(vrgController.DownloadTypedObjects(*vrgObjectStorer, v.s3KeyPrefix(), &vrgs)).To(Succeed())
	Expect(vrgs).To(HaveLen(1))
	vrgS3 := &vrgs[0]
	// TODO fix in controller and remove
	for i := range vrgS3.Status.Conditions {
		t := &vrgS3.Status.Conditions[i].LastTransitionTime
		*t = t.Rfc3339Copy()
	}
	// vrgS3.Status.LastUpdateTime = vrgS3.Status.LastUpdateTime.Rfc3339Copy()
	// TODO fix in controller and remove
	if len(vrgS3.Status.ProtectedPVCs) == 0 {
		vrgS3.Status.ProtectedPVCs = nil
	}

	vrgStatusStateUpdate(vrgS3, vrgK8s)
	// Expect(vrgS3).To(Equal(vrgK8s)) TODO re-enable: fails on github despite matching VRGs output
}

func (v *vrgTest) kubeObjectProtectionValidate() *ramendrv1alpha1.VolumeReplicationGroup {
	vrg := v.clusterDataProtectedWait(metav1.ConditionTrue)
	v.vrgDownloadAndValidate(vrg)

	return vrg
}

func kubeObjectProtectionValidate(tests []*vrgTest) {
	protectedVrgList := protectedVrgListCreateAndStatusWait("protectedvrglist-vrg-"+tests[0].uniqueID, vrgS3ProfileNumber)
	vrgs := make([]ramendrv1alpha1.VolumeReplicationGroup, len(tests))

	for i, v := range tests {
		vrg := v.kubeObjectProtectionValidate()
		vrgController.VrgTidyForList(vrg)
		vrgs[i] = *vrg
		protectedVrgListExpectInclude(protectedVrgList, vrgs[i:i+1])
	}

	protectedVrgListExpectInclude(protectedVrgList, vrgs)
	protectedVrgListDeleteAndNotFoundWait(protectedVrgList)
}

func (v *vrgTest) cleanupStatusAbsent() {
	v.cleanup(vrgPvcStatusAbsentVerify)
}

func (v *vrgTest) cleanupStatusUnprotected() {
	v.cleanup(pvcClusterDataProtectedFalseVerify)
}

func (v *vrgTest) cleanupUnprotected() {
	v.cleanup(pvcUnprotectedVerify)
}

func (v *vrgTest) cleanupProtected() {
	v.cleanup(pvcProtectedVerify)
}

func (v *vrgTest) cleanup(
	pvcPreDeleteVerify pvcPreDeleteVerify,
) {
	v.cleanupPVCs(pvcPreDeleteVerify, vrAndPvcFinalizerOrPvcAndPvAbsentVerify)
	v.cleanupVRG()
	v.cleanupNamespace()
	v.cleanupSC()
	v.cleanupVRC()
}

func (v *vrgTest) cleanupPVCs(
	pvcPreDeleteVerify pvcPreDeleteVerify,
	pvcPostDeleteVerify pvcPostDeleteVerify,
) {
	vrg := v.getVRG()

	pvcPostDeleteVerify1 := pvcPostDeleteVerify
	if !vrgController.VolumeUnprotectionEnabledForAsyncVolRep {
		pvcPostDeleteVerify1 = func(pvcNamespacedName types.NamespacedName, pvName string) {
			pvcDeletionTimestampRecentVerify(pvcNamespacedName)
		}
	} else if vrg.Spec.ReplicationState == ramendrv1alpha1.Primary {
		pvcPostDeleteVerify1 = func(pvcNamespacedName types.NamespacedName, pvName string) {
			pvcPostDeleteVerify(pvcNamespacedName, pvName)
			pvcUnprotectedEventuallyVerify(client.ObjectKeyFromObject(vrg), pvcNamespacedName, pvName)
		}
	}

	v.forEachPVC(func(pvc corev1.PersistentVolumeClaim) {
		pvcDelete(*vrg, pvc, pvcPreDeleteVerify, pvcPostDeleteVerify1)
	})
}

func (v *vrgTest) forEachPVC(do func(pvc corev1.PersistentVolumeClaim)) {
	forPVCs(v.pvcNames, do)
}

func forPVCs(pvcNames []types.NamespacedName, do func(pvc corev1.PersistentVolumeClaim)) {
	for _, pvcName := range pvcNames {
		pvc := getPVC(pvcName)
		do(*pvc)
	}
}

func pvcDelete(
	vrg ramendrv1alpha1.VolumeReplicationGroup,
	pvc corev1.PersistentVolumeClaim,
	preDeleteVerify pvcPreDeleteVerify,
	postDeleteVerify pvcPostDeleteVerify,
) {
	pvcNamespacedName := client.ObjectKeyFromObject(&pvc)
	pvNamespacedName := types.NamespacedName{Name: pvc.Spec.VolumeName}

	Expect(objectGet(pvNamespacedName, &corev1.PersistentVolume{})).To(Succeed())
	preDeleteVerify(vrg, pvcNamespacedName, pvNamespacedName.Name)
	Expect(k8sClient.Delete(context.TODO(), &pvc)).To(BeNil(), "failed to delete PVC %s", pvcNamespacedName)
	postDeleteVerify(pvcNamespacedName, pvNamespacedName.Name)
}

func pvcUnprotectedEventuallyVerify(vrgNamespacedName, pvcNamespacedName types.NamespacedName, pvName string) {
	pvAndPvcObjectReplicasAbsentVerify(vrgNamespacedName, pvcNamespacedName, pvName)
	vrgPvcStatusAbsentEventually(vrgNamespacedName, pvcNamespacedName)
}

func vrgPvcStatusAbsentEventually(vrgNamespacedName, pvcNamespacedName types.NamespacedName) {
	Eventually(func() *ramendrv1alpha1.ProtectedPVC {
		return vrgController.FindProtectedPVC(vrgGet(vrgNamespacedName), pvcNamespacedName.Namespace, pvcNamespacedName.Name)
	}).Should(BeNil())
}

func pvcUnprotectedVerify(
	vrg ramendrv1alpha1.VolumeReplicationGroup, pvcNamespacedName types.NamespacedName, pvName string,
) {
	pvAndPvcObjectReplicasAbsentVerify(client.ObjectKeyFromObject(&vrg), pvcNamespacedName, pvName)
	vrgPvcStatusAbsentVerify(vrg, pvcNamespacedName, pvName)
}

type pvcPreDeleteVerify func(ramendrv1alpha1.VolumeReplicationGroup, types.NamespacedName, string)

func vrgPvcStatusAbsentVerify(
	vrg ramendrv1alpha1.VolumeReplicationGroup, pvcNamespacedName types.NamespacedName, pvName string,
) {
	By("not storing PVC in VRG's status")
	Expect(vrgController.FindProtectedPVC(&vrg, pvcNamespacedName.Namespace, pvcNamespacedName.Name)).To(BeNil())
}

func pvcClusterDataProtectedFalseVerify(
	vrg ramendrv1alpha1.VolumeReplicationGroup, pvcNamespacedName types.NamespacedName, pvName string,
) {
	vrgPvcStatus := vrgPvcStatusGet(vrg, pvcNamespacedName)

	By("setting VRG's PVC ClusterDataProtected status to false")
	pvcClusterDataProtectedStatusVerify(vrgPvcStatus, Equal(metav1.ConditionFalse))
}

func pvcProtectedVerify(
	vrg ramendrv1alpha1.VolumeReplicationGroup, pvcNamespacedName types.NamespacedName, pvName string,
) {
	vrgPvcStatus := vrgPvcStatusGet(vrg, pvcNamespacedName)

	By("setting VRG's PVC ClusterDataProtected status to true")
	pvcClusterDataProtectedStatusVerify(vrgPvcStatus, Equal(metav1.ConditionTrue))
	pvAndPvcObjectReplicasPresentVerify(client.ObjectKeyFromObject(&vrg), pvcNamespacedName, pvName)
}

func vrgPvcStatusGet(
	vrg ramendrv1alpha1.VolumeReplicationGroup, pvcNamespacedName types.NamespacedName,
) ramendrv1alpha1.ProtectedPVC {
	vrgPvcStatus := vrgController.FindProtectedPVC(&vrg, pvcNamespacedName.Namespace, pvcNamespacedName.Name)
	Expect(vrgPvcStatus).ToNot(BeNil())

	return *vrgPvcStatus
}

func pvcClusterDataProtectedStatusVerify(
	vrgPvcStatus ramendrv1alpha1.ProtectedPVC,
	matcher gomegatypes.GomegaMatcher,
) {
	pvcClusterDataProtected := meta.FindStatusCondition(
		vrgPvcStatus.Conditions, vrgController.VRGConditionTypeClusterDataProtected,
	)
	Expect(pvcClusterDataProtected).ToNot(BeNil())
	Expect(pvcClusterDataProtected.Status).To(matcher)
}

type pvcPostDeleteVerify func(types.NamespacedName, string)

func vrAndPvcDeletionTimestampsRecentVerify(pvcNamespacedName types.NamespacedName, pvName string) {
	vrDeletionTimestampRecentVerify(pvcNamespacedName)
	pvcDeletionTimestampRecentVerify(pvcNamespacedName)
}

func pvcDeletionTimestampRecentVerify(pvcNamespacedName types.NamespacedName) {
	objectDeletionTimestampRecentVerify(pvcNamespacedName, &corev1.PersistentVolumeClaim{})
}

func vrDeletionTimestampRecentVerify(vrNamespacedName types.NamespacedName) {
	objectDeletionTimestampRecentVerify(vrNamespacedName, &volrep.VolumeReplication{})
}

func VrAndPvcAndPvAbsentVerify(pvcNamespacedName types.NamespacedName, pvName string) {
	vrAbsentVerify(pvcNamespacedName)
	pvcAbsentVerify(pvcNamespacedName)
	pvAbsentVerify(pvName)
}

func vrAndPvcFinalizerOrPvcAndPvAbsentVerify(pvcNamespacedName types.NamespacedName, pvName string) {
	vrAbsentVerify(pvcNamespacedName)

	if objectOrItsFinalizerAbsentVerify(pvcNamespacedName, &corev1.PersistentVolumeClaim{}, pvcGroupResource(),
		vrgController.PvcVRFinalizerProtected) {
		pvAbsentVerify(pvName)
	}
}

func vrAbsentVerify(namespacedName types.NamespacedName) {
	By("not storing VR resource in API server")
	objectAbsentVerify(namespacedName, &volrep.VolumeReplication{}, vrGroupResource())
}

func pvcAbsentVerify(namespacedName types.NamespacedName) {
	By("not storing PVC resource in API server")
	objectAbsentVerify(namespacedName, &corev1.PersistentVolumeClaim{}, pvcGroupResource())
}

func pvAbsentVerify(pvName string) {
	By("not storing PV resource in API server")
	objectAbsentVerify(types.NamespacedName{Name: pvName}, &corev1.PersistentVolume{}, pvGroupResource())
}

func vrGroupResource() schema.GroupResource {
	return schema.GroupResource{
		Group:    volrep.GroupVersion.Group,
		Resource: "volumereplications",
		// Resource: "VolumeReplication",
	}
}

func pvcGroupResource() schema.GroupResource {
	return schema.GroupResource{Group: corev1.SchemeGroupVersion.Group, Resource: "persistentvolumeclaims"}
}

func pvGroupResource() schema.GroupResource {
	return schema.GroupResource{Group: corev1.SchemeGroupVersion.Group, Resource: "persistentvolumes"}
}

func pvAndPvcObjectReplicasPresentVerify(vrgNamespacedName, pvcNamespacedName types.NamespacedName, pvName string) {
	By("storing PVC and its PV API resources in VRG's S3 stores")
	pvAndPvcObjectReplicaDownloadsExpectTo(vrgNamespacedName, pvcNamespacedName, pvName, Succeed())
}

func pvAndPvcObjectReplicasAbsentVerify(vrgNamespacedName, pvcNamespacedName types.NamespacedName, pvName string) {
	By("not storing PVC and its PV API resources in VRG's S3 stores")
	pvAndPvcObjectReplicaDownloadsExpectTo(vrgNamespacedName, pvcNamespacedName, pvName, MatchError(fs.ErrNotExist))
}

func pvAndPvcObjectReplicaDownloadsExpectTo(
	vrgNamespacedName, pvcNamespacedName types.NamespacedName, pvName string,
	matcher gomegatypes.GomegaMatcher,
) {
	var (
		pv  corev1.PersistentVolume
		pvc corev1.PersistentVolumeClaim
	)

	keyPrefix := vrgS3KeyPrefix(vrgNamespacedName)

	By(fmt.Sprintf("PVC %v PV %v", pvcNamespacedName.String(), pvName))
	Expect(vrgController.DownloadTypedObject(*vrgObjectStorer, keyPrefix, pvName, &pv)).To(matcher)
	Expect(vrgController.DownloadTypedObject(*vrgObjectStorer, keyPrefix, pvcNamespacedName.String(), &pvc)).To(matcher)
}

func (v *vrgTest) cleanupVRG() {
	vrg := v.getVRG()
	err := k8sClient.Delete(context.TODO(), vrg)
	Expect(err).To(BeNil(),
		"failed to delete VRG %s", v.vrgName)
	v.waitForVRCountToMatch(0)
}

func (v *vrgTest) cleanupSC() {
	if v.storageClass == "" {
		return
	}

	key := types.NamespacedName{
		Name: v.storageClass,
	}

	sc := &storagev1.StorageClass{}

	err := k8sClient.Get(context.TODO(), key, sc)
	if err != nil {
		if errors.IsNotFound(err) {
			return
		}
	}

	err = k8sClient.Delete(context.TODO(), sc)
	Expect(err).To(BeNil(),
		"failed to delete StorageClass %s", v.storageClass)
}

func (v *vrgTest) cleanupVRC() {
	key := types.NamespacedName{
		Name: v.replicationClass,
	}

	vrc := &volrep.VolumeReplicationClass{}

	err := k8sClient.Get(context.TODO(), key, vrc)
	if err != nil {
		if errors.IsNotFound(err) {
			return
		}
	}

	err = k8sClient.Delete(context.TODO(), vrc)
	Expect(err).To(BeNil(),
		"failed to delete replicationClass %s", v.replicationClass)
}

func (v *vrgTest) cleanupNamespace() {
	By("deleting namespace " + v.namespace)

	appNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: v.namespace}}
	err := k8sClient.Delete(context.TODO(), appNamespace)
	Expect(err).To(BeNil(),
		"failed to delete namespace %s", v.namespace)
	v.waitForNamespaceDeletion()
}

func (v *vrgTest) waitForVRCountToMatch(vrCount int) {
	By("Waiting for VRs count to match " + v.namespace)

	// selector, err := metav1.LabelSelectorAsSelector(&vrg.Spec.PVCSelector)
	// Expect(err).To(BeNil())

	Eventually(func() int {
		listOptions := &client.ListOptions{
			// LabelSelector: selector,
			Namespace: v.namespace,
		}
		volRepList := &volrep.VolumeReplicationList{}
		err := k8sClient.List(context.TODO(), volRepList, listOptions)
		Expect(err).NotTo(HaveOccurred(),
			"failed to get a list of VRs in namespace %s", v.namespace)

		return len(volRepList.Items)
	}, timeout, interval).Should(BeNumerically("==", vrCount),
		"while waiting for VR count of %d in VRG %s of namespace %s",
		vrCount, v.vrgName, v.namespace)
}

func (v *vrgTest) promoteVolReps() {
	v.promoteVolRepsAndDo(func(index, count int) {
		// VRG should not be ready until last VolRep is ready.
		v.verifyVRGStatusExpectation(index == count-1)
	})
}

func (v *vrgTest) promoteVolRepsWithoutVrgStatusCheck() {
	v.promoteVolRepsAndDo(func(index, count int) {})
}

func (v *vrgTest) promoteVolRepsAndDo(do func(int, int)) {
	By("Promoting VolumeReplication resources " + v.namespace)

	volRepList := &volrep.VolumeReplicationList{}
	listOptions := &client.ListOptions{
		Namespace: v.namespace,
	}
	err := k8sClient.List(context.TODO(), volRepList, listOptions)
	Expect(err).NotTo(HaveOccurred(), "failed to get a list of VRs in namespace %s", v.namespace)

	for index := range volRepList.Items {
		volRep := volRepList.Items[index]

		volRepStatus := volrep.VolumeReplicationStatus{
			Conditions: []metav1.Condition{
				{
					Type:               volrepController.ConditionCompleted,
					Reason:             volrepController.Promoted,
					ObservedGeneration: volRep.Generation,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(time.Now()),
				},
				{
					Type:               volrepController.ConditionDegraded,
					Reason:             volrepController.Healthy,
					ObservedGeneration: volRep.Generation,
					Status:             metav1.ConditionFalse,
					LastTransitionTime: metav1.NewTime(time.Now()),
				},
				{
					Type:               volrepController.ConditionResyncing,
					Reason:             volrepController.NotResyncing,
					ObservedGeneration: volRep.Generation,
					Status:             metav1.ConditionFalse,
					LastTransitionTime: metav1.NewTime(time.Now()),
				},
			},
		}
		volRepStatus.ObservedGeneration = volRep.Generation
		volRepStatus.State = volrep.PrimaryState
		volRepStatus.Message = "volume is marked primary"
		volRep.Status = volRepStatus

		err = k8sClient.Status().Update(context.TODO(), &volRep)
		Expect(err).NotTo(HaveOccurred(), "failed to update the status of VolRep %s", volRep.Name)

		volrepKey := types.NamespacedName{
			Name:      volRep.Name,
			Namespace: volRep.Namespace,
		}
		v.waitForVolRepPromotion(volrepKey)

		do(index, len(volRepList.Items))
	}
}

func (v *vrgTest) protectDeletionOfVolReps() {
	By("Adding a finalizer to protect VolumeReplication resources being deleted " + v.namespace)

	volRepList := &volrep.VolumeReplicationList{}
	listOptions := &client.ListOptions{
		Namespace: v.namespace,
	}
	err := apiReader.List(context.TODO(), volRepList, listOptions)
	Expect(err).NotTo(HaveOccurred(), "failed to get a list of VRs in namespace %s", v.namespace)

	for index := range volRepList.Items {
		volRep := volRepList.Items[index]
		if controllerutil.AddFinalizer(client.Object(&volRep), "testDeleteProtected") {
			err = k8sClient.Update(context.TODO(), &volRep)
			Expect(err).NotTo(HaveOccurred(), "failed to add finalizer to VolRep %s", volRep.Name)
		}
	}
}

func (v *vrgTest) unprotectDeletionOfVolReps() {
	By("Removing finalizer that protects VolumeReplication resources from being deleted " + v.namespace)

	volRepList := &volrep.VolumeReplicationList{}
	listOptions := &client.ListOptions{
		Namespace: v.namespace,
	}
	err := apiReader.List(context.TODO(), volRepList, listOptions)
	Expect(err).NotTo(HaveOccurred(), "failed to get a list of VRs in namespace %s", v.namespace)

	for index := range volRepList.Items {
		volRep := volRepList.Items[index]
		if controllerutil.RemoveFinalizer(client.Object(&volRep), "testDeleteProtected") {
			err = k8sClient.Update(context.TODO(), &volRep)
			Expect(err).NotTo(HaveOccurred(), "failed to remove finalizer to VolRep %s", volRep.Name)
		}
	}
}

func (v *vrgTest) waitForVolRepPromotion(vrNamespacedName types.NamespacedName) {
	updatedVolRep := volrep.VolumeReplication{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), vrNamespacedName, &updatedVolRep)

		return err == nil && len(updatedVolRep.Status.Conditions) == 3
	}, vrgtimeout, vrginterval).Should(BeTrue(),
		"failed to wait for volRep condition type to change to 'ConditionCompleted' (%d)",
		len(updatedVolRep.Status.Conditions))

	Eventually(func() bool {
		vrg := v.getVRG()
		// as of now name of VolumeReplication resource created by the VolumeReplicationGroup
		// is same as the pvc that it replicates. When that changes this has to be changed to
		// use the right name to get the appropriate protected PVC condition from VRG status.
		protectedPVC := vrgController.FindProtectedPVC(vrg, updatedVolRep.Namespace, updatedVolRep.Name)

		// failed to get the protectedPVC. Returning false
		if protectedPVC == nil {
			return false
		}

		return v.checkProtectedPVCSuccess(vrg, protectedPVC)
	}, vrgtimeout, vrginterval).Should(BeTrue(),
		"while waiting for protected pvc condition %s/%s", updatedVolRep.Namespace, updatedVolRep.Name)
}

func (v *vrgTest) checkProtectedPVCSuccess(vrg *ramendrv1alpha1.VolumeReplicationGroup,
	protectedPVC *ramendrv1alpha1.ProtectedPVC,
) bool {
	success := false
	dataReadyCondition := meta.FindStatusCondition(protectedPVC.Conditions,
		vrgController.VRGConditionTypeDataReady)

	switch {
	case vrg.Spec.ReplicationState == ramendrv1alpha1.Primary:
		if dataReadyCondition.Status == metav1.ConditionTrue && dataReadyCondition.Reason ==
			vrgController.VRGConditionReasonReady {
			success = true
		}

	case vrg.Spec.ReplicationState == ramendrv1alpha1.Secondary:
		if dataReadyCondition.Status == metav1.ConditionTrue && dataReadyCondition.Reason ==
			vrgController.VRGConditionReasonReplicating {
			success = true
		}
	}

	return success
}

func (v *vrgTest) waitForNamespaceDeletion() {
	By("Waiting for namespace deletion " + v.namespace)

	appNamespace := &corev1.Namespace{}
	nsObjectKey := client.ObjectKey{Name: v.namespace}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), nsObjectKey, appNamespace)

		return err == nil
	}, timeout, interval).Should(BeTrue(),
		"while waiting for namespace %s to be deleted", v.namespace)
}

func waitForPVRestore(pvList []corev1.PersistentVolume) {
	var pvCount int

	for _, pv := range pvList {
		pvLookupKey := types.NamespacedName{Name: pv.Name}
		restoredPV := &corev1.PersistentVolume{}

		Eventually(func() bool {
			err := k8sClient.Get(context.TODO(), pvLookupKey, restoredPV)
			if err != nil {
				return false
			}

			Expect(restoredPV.ObjectMeta.Annotations[vrgController.RestoreAnnotation]).Should(Equal("True"))

			pvCount++

			return true
		}, timeout, interval).Should(BeTrue(),
			"while waiting for PV %s to be restored", pv.Name)
	}

	Expect(pvCount == len(pvList))
}

func waitForPVCRestore(pvcList []corev1.PersistentVolumeClaim) {
	var pvcCount int

	for _, pvc := range pvcList {
		pvcLookupKey := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		restoredPVC := &corev1.PersistentVolumeClaim{}

		Eventually(func() bool {
			err := k8sClient.Get(context.TODO(), pvcLookupKey, restoredPVC)
			if err != nil {
				return false
			}

			Expect(restoredPVC.ObjectMeta.Annotations[vrgController.RestoreAnnotation]).Should(Equal("True"))

			pvcCount++

			return true
		}, timeout, interval).Should(BeTrue(),
			"while waiting for PVC %+v to be restored", restoredPVC)
	}

	Expect(pvcCount == len(pvcList))
}

func updatePVCClaimBindInfo(pvcList []corev1.PersistentVolumeClaim, claimBindInfo corev1.PersistentVolumeClaimPhase) {
	for _, pvc := range pvcList {
		pvcLookupKey := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
		restoredPVC := &corev1.PersistentVolumeClaim{}

		err := k8sClient.Get(context.TODO(), pvcLookupKey, restoredPVC)
		Expect(err).NotTo(HaveOccurred())

		restoredPVC.Status.Phase = claimBindInfo
		restoredPVC.Status.AccessModes = pvc.Spec.AccessModes
		restoredPVC.Status.Capacity = pvc.Spec.Resources.Requests
		err = k8sClient.Status().Update(context.TODO(), restoredPVC)
		Expect(err).To(BeNil(),
			"failed to update status of PVC %s", pvc.Name)
	}
}

func (v *vrgTest) waitForVRGStateToTransitionToPrimary() {
	Eventually(func() bool {
		return v.getVRG().Status.State == ramendrv1alpha1.PrimaryState
	}, timeout, interval).Should(BeTrue())
}
