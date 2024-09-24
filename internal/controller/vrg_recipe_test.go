// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"strings"

	volrep "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegatypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("VolumeReplicationGroupRecipe", func() {
	const recipeErrorMessagePrefix = "Failed to get recipe"
	extraVrgNamespacesFeatureEnabledSetAndDeferRestore := func(enable bool) {
		enabled := ramenConfig.MultiNamespace.FeatureEnabled
		ramenConfig.MultiNamespace.FeatureEnabled = enable
		configMapUpdate()
		DeferCleanup(func() {
			ramenConfig.MultiNamespace.FeatureEnabled = enabled
			configMapUpdate()
		})
	}
	const (
		scName          = "a"
		vrcName         = "b"
		provisionerName = "x"
		vrInterval      = "0m"
	)
	var (
		r                   *recipe.Recipe
		vrg                 *ramen.VolumeReplicationGroup
		vrgDataReadyPointer *metav1.Condition
		vrgDataReady        metav1.Condition
	)

	scCreateAndDeferDelete := func() {
		sc := &storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: scName},
			Provisioner: provisionerName,
		}
		Expect(k8sClient.Create(ctx, sc)).To(Succeed())
		DeferCleanup(k8sClient.Delete, ctx, sc)
	}
	vrcCreateAndDeferDelete := func() {
		vrc := &volrep.VolumeReplicationClass{
			ObjectMeta: metav1.ObjectMeta{Name: vrcName},
			Spec: volrep.VolumeReplicationClassSpec{
				Provisioner: provisionerName,
				Parameters: map[string]string{
					"schedulingInterval": vrInterval,
				},
			},
		}
		Expect(k8sClient.Create(ctx, vrc)).To(Succeed())
		DeferCleanup(k8sClient.Delete, ctx, vrc)
	}
	nsCreate := func() *corev1.Namespace {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: vrgTestNamespaceBase},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		return ns
	}
	nsDelete := func(ns *corev1.Namespace) error {
		return k8sClient.Delete(ctx, ns)
	}
	pvcCreate := func(namespaceName string) *corev1.PersistentVolumeClaim {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceName,
				Name:      "a",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Mi"),
					},
				},
				StorageClassName: func() *string {
					s := scName

					return &s
				}(),
			},
		}
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())

		return pvc
	}
	pvcDelete := func(pvc *corev1.PersistentVolumeClaim) error {
		return k8sClient.Delete(ctx, pvc)
	}
	pvcNameGet := func(pvc *corev1.PersistentVolumeClaim) types.NamespacedName {
		return types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}
	}
	group := func(typeName string, namespaceNames ...string) *recipe.Group {
		return &recipe.Group{
			Name:               typeName,
			Type:               typeName,
			IncludedNamespaces: namespaceNames,
		}
	}
	volumes := func(namespaceNames ...string) *recipe.Group {
		return group("volume", namespaceNames...)
	}
	resources := func(namespaceNames ...string) *recipe.Group {
		return group("resource", namespaceNames...)
	}
	hook := func(namespaceName string) *recipe.Hook {
		return &recipe.Hook{
			Name:      namespaceName,
			Namespace: namespaceName,
			Type:      "exec",
			Ops: []*recipe.Operation{
				{
					Name:    namespaceName,
					Command: namespaceName,
				},
			},
		}
	}
	recipeDefine := func(namespaceName string) {
		r = &recipe.Recipe{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceName,
				Name:      "r",
			},
			Spec: recipe.RecipeSpec{},
		}

		r.Spec.Workflows = make([]*recipe.Workflow, 0)
	}
	recipeVolumesDefine := func(volumes *recipe.Group) {
		r.Spec.Volumes = volumes
	}
	recipeGroupsDefine := func(groups ...*recipe.Group) {
		r.Spec.Groups = groups
	}
	recipeHooksDefine := func(hooks ...*recipe.Hook) {
		r.Spec.Hooks = hooks
	}
	allGroupsAllHooksWorkflow := func() *recipe.Workflow {
		sequence := make([]map[string]string, len(r.Spec.Groups)+len(r.Spec.Hooks))

		for i, group := range r.Spec.Groups {
			sequence[i] = map[string]string{"group": group.Name}
		}

		for i, hook := range r.Spec.Hooks {
			sequence[i+len(r.Spec.Groups)] = map[string]string{"hook": hook.Name + "/" + hook.Ops[0].Name}
		}

		return &recipe.Workflow{
			Sequence: sequence,
		}
	}
	recipeCaptureWorkflowDefine := func(workflow *recipe.Workflow) {
		workflow.Name = recipe.BackupWorkflowName
		r.Spec.Workflows = append(r.Spec.Workflows, workflow)
	}
	recipeRecoverWorkflowDefine := func(workflow *recipe.Workflow) {
		workflow.Name = recipe.RestoreWorkflowName
		r.Spec.Workflows = append(r.Spec.Workflows, workflow)
	}
	recipeCreate := func() {
		Expect(k8sClient.Create(ctx, r)).To(Succeed())
	}
	recipeDelete := func() error {
		return k8sClient.Delete(ctx, r)
	}
	vrgDefine := func(namespaceName string) {
		vrg = &ramen.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespaceName, Name: "a"},
			Spec: ramen.VolumeReplicationGroupSpec{
				S3Profiles:           []string{controllers.NoS3StoreAvailable},
				ReplicationState:     ramen.Primary,
				Sync:                 &ramen.VRGSyncSpec{},
				KubeObjectProtection: &ramen.KubeObjectProtectionSpec{},
			},
		}
	}
	vrgRecipeRefDefine := func(name string) {
		vrg.Spec.KubeObjectProtection.RecipeRef = &ramen.RecipeRef{
			Namespace: r.Namespace,
			Name:      name,
		}
	}
	vrgRecipeParametersDefine := func(recipeParameters map[string][]string) {
		vrg.Spec.KubeObjectProtection.RecipeParameters = recipeParameters
	}
	vrgRecipeParameters := func() map[string][]string {
		return vrg.Spec.KubeObjectProtection.RecipeParameters
	}
	vrgCreate := func() error {
		return k8sClient.Create(ctx, vrg)
	}
	vrgGet := func() error {
		return apiReader.Get(ctx, types.NamespacedName{Namespace: vrg.Namespace, Name: vrg.Name}, vrg)
	}
	vrgDelete := func() {
		Expect(k8sClient.Delete(ctx, vrg)).To(Succeed())
		Eventually(vrgGet).Should(MatchError(errors.NewNotFound(
			schema.GroupResource{
				Group:    ramen.GroupVersion.Group,
				Resource: "volumereplicationgroups",
			},
			vrg.Name,
		)))
	}
	vrgGetAndExpectSuccess := func() *ramen.VolumeReplicationGroup {
		Expect(vrgGet()).To(Succeed())

		return vrg
	}
	vrgStatusConditionGet := func(conditionType string) *metav1.Condition {
		return meta.FindStatusCondition(vrgGetAndExpectSuccess().Status.Conditions, conditionType)
	}
	vrgStatusConditionGetAndExpectNonNil := func(conditionType string) metav1.Condition {
		condition := meta.FindStatusCondition(vrgGetAndExpectSuccess().Status.Conditions, conditionType)
		Expect(condition).ToNot(BeNil())

		return *condition
	}
	vrgDataReadyConditionGet := func() *metav1.Condition {
		vrgDataReadyPointer = vrgStatusConditionGet(controllers.VRGConditionTypeDataReady)

		return vrgDataReadyPointer
	}
	vrgDataReadyConditionGetAndExpectNonNil := func() metav1.Condition {
		vrgDataReady = vrgStatusConditionGetAndExpectNonNil(controllers.VRGConditionTypeDataReady)

		return vrgDataReady
	}
	vrgPvcsGet := func() []ramen.ProtectedPVC {
		return vrgGetAndExpectSuccess().Status.ProtectedPVCs
	}
	vrgPvcNameGet := func(pvc ramen.ProtectedPVC) types.NamespacedName {
		return types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}
	}
	vrgPvcNameMatchesPvc := func(pvc *corev1.PersistentVolumeClaim) gomegatypes.GomegaMatcher {
		return WithTransform(vrgPvcNameGet, Equal(pvcNameGet(pvc)))
	}
	vrgPvcNamesMatchPvcs := func(pvcs ...*corev1.PersistentVolumeClaim) []gomegatypes.GomegaMatcher {
		matchers := make([]gomegatypes.GomegaMatcher, len(pvcs))
		for i, pvc := range pvcs {
			matchers[i] = vrgPvcNameMatchesPvc(pvc)
		}

		return matchers
	}
	vrgPvcsConsistOfEventually := func(pvcs ...*corev1.PersistentVolumeClaim) {
		Eventually(vrgPvcsGet, timeout, interval).Should(ConsistOf(vrgPvcNamesMatchPvcs(pvcs...)))
	}
	vrgPvcSelectorGet := func() (controllers.PvcSelector, error) {
		return controllers.GetPVCSelector(ctx, apiReader, *vrg, *ramenConfig, testLogger)
	}
	skipIfAdmissionValidateAndCommitAreAtomicIs := func(condition bool, message string) {
		if !condition {
			Skip(message)
		}
	}
	skipIfAdmissionValidationDenies := func() {
		skipIfAdmissionValidateAndCommitAreAtomicIs(true, "VRG admission validation denies")
	}

	const nsCount = 3
	var (
		nsNames      [nsCount]string
		pvcs         [nsCount]*corev1.PersistentVolumeClaim
		nsNamesSlice []string
		pvcsSlice    []*corev1.PersistentVolumeClaim
	)
	nsSlices := func(low, high uint) {
		nsNamesSlice, pvcsSlice = nsNames[low:high], pvcs[low:high]
	}
	BeforeEach(OncePerOrdered, func() {
		scCreateAndDeferDelete()
		vrcCreateAndDeferDelete()
		for i := range nsNames {
			ns := nsCreate()
			DeferCleanup(nsDelete, ns)
			nsNames[i] = ns.Name
			pvcs[i] = pvcCreate(ns.Name)
			DeferCleanup(pvcDelete, pvcs[i])
		}
		recipeDefine(nsNames[0])
		vrgDefine(ramenNamespace)
	})
	var err error
	JustBeforeEach(OncePerOrdered, func() {
		recipeRecoverWorkflowDefine(allGroupsAllHooksWorkflow())
		recipeCreate()
		DeferCleanup(recipeDelete)
		err = vrgCreate()
	})

	Describe("AdmissionController", func() {
		BeforeEach(func() {
			skipIfAdmissionValidateAndCommitAreAtomicIs(false, "VRG admission validation disabled")
		})
		When("a VRG creation request is submitted", func() {
			Context("without a recipe reference", func() {
				BeforeEach(func() {
					DeferCleanup(vrgDelete)
				})
				It("allows it", func() { Expect(err).ToNot(HaveOccurred()) })
			})
			Context("referencing an absent recipe", func() {
				BeforeEach(func() {
					vrgRecipeRefDefine("asdf")
				})
				It("denies it", func() { Expect(err).To(HaveOccurred()) })
			})
			Context("referencing a recipe", func() {
				BeforeEach(func() {
					vrgRecipeRefDefine(r.Name)
				})
				Context("for an administrator namespace", func() {
					BeforeEach(func() {
						vrg.Namespace = ramenNamespace
					})
					Context("whose recipe references another namespace", func() {
						BeforeEach(func() {
							recipeVolumesDefine(volumes(nsNames[0]))
						})
						Context("with extra-VRG namespaces feature disabled", func() {
							BeforeEach(func() {
								extraVrgNamespacesFeatureEnabledSetAndDeferRestore(false)
							})
							It("denies it", func() { Expect(err).To(HaveOccurred()) })
						})
						Context("with extra-VRG namespaces feature enabled", func() {
							BeforeEach(func() {
								DeferCleanup(vrgDelete)
							})
							It("allows it", func() { Expect(err).ToNot(HaveOccurred()) })
						})
					})
				})
				Context("for a non-administrator namespace", func() {
					BeforeEach(func() {
						vrg.Namespace = nsNames[0]
					})
					Context("whose recipe references no namespaces", func() {
						BeforeEach(func() {
							DeferCleanup(vrgDelete)
						})
						It("allows it", func() { Expect(err).ToNot(HaveOccurred()) })
					})
					Context("whose recipe references the same namespace only", func() {
						BeforeEach(func() {
							recipeVolumesDefine(volumes(vrg.Namespace))
							DeferCleanup(vrgDelete)
						})
						It("allows it", func() { Expect(err).ToNot(HaveOccurred()) })
					})
					Context("whose recipe references another namespace", func() {
						BeforeEach(func() {
							recipeVolumesDefine(volumes(nsNames[1]))
						})
						It("denies it", func() { Expect(err).To(HaveOccurred()) })
					})
				})
			})
		})
	})
	Describe("Controller", func() {
		JustBeforeEach(OncePerOrdered, func() {
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(vrgDelete)
		})
		When("a VRG, referencing a recipe,", func() {
			BeforeEach(OncePerOrdered, func() {
				vrgRecipeRefDefine(r.Name)
			})
			var pvcSelector controllers.PvcSelector
			var err error
			JustBeforeEach(func() {
				pvcSelector, err = vrgPvcSelectorGet()
			})
			Context("in an administrator namespace specifies only other namespaces", func() {
				BeforeEach(func() {
					vrg.Namespace = ramenNamespace
					nsSlices(1, nsCount)
				})
				Context("statically", func() {
					BeforeEach(func() {
						recipeVolumesDefine(volumes(nsNamesSlice...))
						recipeGroupsDefine(resources(nsNamesSlice...))
						recipeHooksDefine(hook(nsNamesSlice[0]))
						recipeCaptureWorkflowDefine(allGroupsAllHooksWorkflow())
					})
					It("includes only them in its PVC selection", func() {
						Expect(err).ToNot(HaveOccurred())
						Expect(pvcSelector.NamespaceNames).To(ConsistOf(nsNamesSlice))
					})
					It("lists only their PVCs in the VRG's status", func() {
						vrgPvcsConsistOfEventually(pvcsSlice...)
					})
				})
				Context("parametrically", func() {
					var recipeExpanded *recipe.Recipe
					BeforeEach(func() {
						const nssParameterName = "nss"
						const nssParameterRef = "$" + nssParameterName
						const ns0ParameterName = "ns0"
						const ns0ParameterRef = "$" + ns0ParameterName

						parameters := map[string][]string{
							nssParameterName: nsNamesSlice,
							ns0ParameterName: nsNamesSlice[0:1],
						}

						recipeVolumesDefine(volumes(nssParameterRef))
						vrgRecipeParametersDefine(parameters)
						recipeHooksDefine(hook(ns0ParameterRef))
					})
					JustBeforeEach(func() {
						recipeExpanded = &*r
						Expect(controllers.RecipeParametersExpand(recipeExpanded, vrgRecipeParameters(), testLogger)).To(Succeed())
					})
					It("expands a parameter list enclosed in double quotes to a single string with quotes preserved", func() {
						Skip("feature not supported")
						Expect(recipeExpanded.Spec.Hooks[0].Ops[0].Command).To(Equal(`"` + strings.Join(nsNamesSlice, ",") + `"`))
					})
					Context("with Ramen's extra-VRG namespaces feature disabled", func() {
						BeforeEach(func() {
							extraVrgNamespacesFeatureEnabledSetAndDeferRestore(false)
							skipIfAdmissionValidationDenies()
						})
						It("has an invalid PVC selector", func() {
							Expect(err).To(HaveOccurred())
						})
					})
					Context("with Ramen's extra-VRG namespaces feature enabled", func() {
						It("includes only them in its PVC selection", func() {
							Expect(err).ToNot(HaveOccurred())
							Expect(pvcSelector.NamespaceNames).To(ConsistOf(nsNamesSlice))
						})
						It("lists only their PVCs in the VRG's status", func() {
							vrgPvcsConsistOfEventually(pvcsSlice...)
						})
					})
				})
			})
			Context("in a non-administrator namespace with Ramen's extra-VRG namespaces feature enabled", func() {
				Context("whose recipe references no namespaces", func() {
					BeforeEach(func() {
						nsSlices(1, 2)
						vrg.Namespace = nsNamesSlice[0]
					})
					It("includes only the VRG's namespace in its PVC selection", func() {
						Expect(err).ToNot(HaveOccurred())
						Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))
					})
					It("lists only the VRG namespace's PVCs in the VRG's status", func() {
						vrgPvcsConsistOfEventually(pvcsSlice...)
					})
				})
				Context("whose recipe references only the same namespace", func() {
					BeforeEach(func() {
						nsSlices(1, 2)
						vrg.Namespace = nsNamesSlice[0]
						recipeVolumesDefine(volumes(vrg.Namespace))
					})
					It("includes only it in its PVC selection", func() {
						Expect(err).ToNot(HaveOccurred())
						Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))
					})
					It("lists only its PVCs in the VRG's status", func() {
						vrgPvcsConsistOfEventually(pvcsSlice...)
					})
				})
				Context("whose recipe references", Ordered, func() {
					BeforeAll(func() {
						nsSlices(1, nsCount)
						vrg.Namespace = nsNamesSlice[0]
						skipIfAdmissionValidationDenies()
					})
					Context("the same namespace and another namespace initially", func() {
						BeforeAll(func() {
							recipeVolumesDefine(volumes(nsNamesSlice...))
						})
						It("has an invalid PVC selector", func() {
							Expect(err).To(HaveOccurred())
						})
						It("sets DataReady condition's status to false and message to a recipe error", func() {
							Eventually(vrgDataReadyConditionGet).ShouldNot(BeNil())
							Expect(*vrgDataReadyPointer).To(MatchFields(IgnoreExtras, Fields{
								"Status":  Equal(metav1.ConditionFalse),
								"Reason":  Equal(controllers.VRGConditionReasonError),
								"Message": HavePrefix(recipeErrorMessagePrefix),
							}))
						})
						It("lists no PVCs in the VRG's status", func() {
							Expect(vrgPvcsGet()).To(BeEmpty())
						})
					})
					Context("only the same namespace after an update to remove other namespaces", func() {
						BeforeAll(func() {
							nsSlices(1, 2)
							Expect(apiReader.Get(ctx, client.ObjectKeyFromObject(r), r)).To(Succeed())
							recipeVolumesDefine(volumes(vrg.Namespace))
							Expect(k8sClient.Update(ctx, r)).To(Succeed())
						})
						It("includes only it in its PVC selection", func() {
							Expect(err).ToNot(HaveOccurred())
							Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))
						})
						It("sets DataReady condition's message to something besides a recipe error", func() {
							Eventually(vrgDataReadyConditionGetAndExpectNonNil, timeout, interval).Should(MatchFields(IgnoreExtras, Fields{
								"Message": Not(HavePrefix(recipeErrorMessagePrefix)),
							}))
						})
						It("lists its PVCs in the VRG's status", func() {
							vrgPvcsConsistOfEventually(pvcsSlice...)
						})
					})
				})
				Context("whose recipe references", func() {
					BeforeEach(func() {
						nsSlices(1, nsCount)
						vrg.Namespace = nsNamesSlice[0]
						skipIfAdmissionValidationDenies()
					})
					Context("recovery hooks in another namespace", func() {
						BeforeEach(func() {
							recipeHooksDefine(hook(nsNamesSlice[1]))
						})
						It("sets DataReady condition's status to false and message to a recipe error", func() {
							Eventually(vrgDataReadyConditionGet).ShouldNot(BeNil())
							Expect(*vrgDataReadyPointer).To(MatchFields(IgnoreExtras, Fields{
								"Status":  Equal(metav1.ConditionFalse),
								"Reason":  Equal(controllers.VRGConditionReasonError),
								"Message": HavePrefix(recipeErrorMessagePrefix),
							}))
						})
					})
					Context("recovery hooks in the same namespace", func() {
						BeforeEach(func() {
							recipeHooksDefine(hook(vrg.Namespace))
						})
						It("sets DataReady condition's message to something besides a recipe error", func() {
							Eventually(vrgDataReadyConditionGet).ShouldNot(BeNil())
							Eventually(*vrgDataReadyPointer).Should(MatchFields(IgnoreExtras, Fields{
								"Message": Not(HavePrefix(recipeErrorMessagePrefix)),
							}))
						})
					})
				})
			})
		})
	})
})
