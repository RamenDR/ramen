// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegatypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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
		enabled := ramenConfig.KubeObjectProtection.ExtraVrgNamespacesFeatureEnabled
		ramenConfig.KubeObjectProtection.ExtraVrgNamespacesFeatureEnabled = enable
		configMapUpdate()
		DeferCleanup(func() {
			ramenConfig.KubeObjectProtection.ExtraVrgNamespacesFeatureEnabled = enabled
			configMapUpdate()
		})
	}
	var (
		nss                 []*corev1.Namespace
		r                   *recipe.Recipe
		vrg                 *ramen.VolumeReplicationGroup
		vrgDataReadyPointer *metav1.Condition
		vrgDataReady        metav1.Condition
	)

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
	pvcCreate := func(ns *corev1.Namespace) *corev1.PersistentVolumeClaim {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns.Name,
				Name:      "a",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Mi"),
					},
				},
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
	hook := func(command ...string) *recipe.Hook {
		return &recipe.Hook{
			Type: "exec",
			Ops: []*recipe.Operation{
				{
					Command: command,
				},
			},
		}
	}
	recipeDefine := func() {
		r = &recipe.Recipe{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: nss[0].Name,
				Name:      "r",
			},
			Spec: recipe.RecipeSpec{},
		}
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
	recipeCreate := func() {
		Expect(k8sClient.Create(ctx, r)).To(Succeed())
	}
	recipeDelete := func() error {
		return k8sClient.Delete(ctx, r)
	}
	vrgDefine := func() {
		vrg = &ramen.VolumeReplicationGroup{
			ObjectMeta: metav1.ObjectMeta{Namespace: ramenNamespace, Name: "a"},
			Spec: ramen.VolumeReplicationGroupSpec{
				S3Profiles:       []string{controllers.NoS3StoreAvailable},
				ReplicationState: ramen.Primary,
				Async: &ramen.VRGAsyncSpec{
					SchedulingInterval: "0m",
				},
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
		Eventually(vrgPvcsGet).Should(ConsistOf(vrgPvcNamesMatchPvcs(pvcs...)))
	}
	vrgPvcSelectorGet := func() (controllers.PvcSelector, error) {
		return controllers.GetPVCSelector(ctx, apiReader, *vrg, *ramenConfig, testLogger)
	}
	vrgPvcSelectorNsNamesExpect := func(nsNamesExpected []string) {
		pvcSelector, err := vrgPvcSelectorGet()
		Expect(err).ToNot(HaveOccurred())
		Expect(pvcSelector.NamespaceNames).To(ConsistOf(nsNamesExpected))
	}
	skipIfAdmissionValidateAndCommitAreAtomicIs := func(condition bool, message string) {
		if !condition {
			Skip(message)
		}
	}
	skipIfAdmissionValidationDenies := func() {
		skipIfAdmissionValidateAndCommitAreAtomicIs(true, "VRG admission validation denies")
	}

	var nsNames []string
	var pvcs []*corev1.PersistentVolumeClaim
	BeforeEach(OncePerOrdered, func() {
		nss = []*corev1.Namespace{nsCreate(), nsCreate(), nsCreate()}
		nsNames = make([]string, len(nss))
		pvcs = make([]*corev1.PersistentVolumeClaim, len(nss))
		for i, ns := range nss {
			DeferCleanup(nsDelete, ns)
			nsNames[i] = ns.Name
			pvcs[i] = pvcCreate(ns)
			DeferCleanup(pvcDelete, pvcs[i])
		}
		recipeDefine()
		vrgDefine()
	})
	var err error
	JustBeforeEach(OncePerOrdered, func() {
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
							recipeVolumesDefine(volumes(nss[0].Name))
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
						vrg.Namespace = nss[0].Name
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
							recipeVolumesDefine(volumes(nss[1].Name))
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
		When("a VRG references a recipe that specifies multiple namespaces", func() {
			var nsNames1 []string
			var pvcs1 []*corev1.PersistentVolumeClaim
			const nsNumberStart = 1
			BeforeEach(func() {
				nsNames1 = nsNames[nsNumberStart:]
				pvcs1 = pvcs[nsNumberStart:]
				vrgRecipeRefDefine(r.Name)
			})
			Context("statically", func() {
				BeforeEach(func() {
					recipeVolumesDefine(volumes(nsNames1...))
					recipeGroupsDefine(resources(nsNames1...))
					recipeHooksDefine(hook(nsNames1...))
				})
				It("includes each in its PVC selection", func() {
					vrgPvcSelectorNsNamesExpect(nsNames1)
				})
				It("lists their PVCs in the VRG's status", func() {
					vrgPvcsConsistOfEventually(pvcs1...)
				})
			})
			Context("parametrically", func() {
				var recipeExpanded *recipe.Recipe
				BeforeEach(func() {
					const parameterName = "ns"
					const parameterRef = "$" + parameterName

					parameters := map[string][]string{parameterName: nsNames1}

					recipeVolumesDefine(volumes(parameterRef))
					vrgRecipeParametersDefine(parameters)
					recipeHooksDefine(hook(parameterRef))

					recipeExpanded = &*r
					Expect(controllers.RecipeParametersExpand(recipeExpanded, parameters, testLogger)).To(Succeed())
				})
				It("includes each in its PVC selection", func() {
					vrgPvcSelectorNsNamesExpect(nsNames1)
				})
				It("lists their PVCs in the VRG's status", func() {
					vrgPvcsConsistOfEventually(pvcs1...)
				})
				It("expands a parameter list enclosed in double quotes to a single string with quotes preserved", func() {
					Skip("feature not supported")
					Expect(recipeExpanded.Spec.Hooks[0].Ops[0].Command).To(Equal(`"` + strings.Join(nsNames1, ",") + `"`))
				})
			})
		})
		When("a VRG, referencing a recipe,", func() {
			BeforeEach(OncePerOrdered, func() {
				vrgRecipeRefDefine(r.Name)
			})
			Context("in an administrator namespace", func() {
				BeforeEach(func() {
					vrg.Namespace = ramenNamespace
				})
				Context("whose recipe references another namespace", func() {
					var extraVrgNamespaceNames []string
					BeforeEach(func() {
						extraVrgNamespaceNames = []string{nss[0].Name}
						recipeVolumesDefine(volumes(extraVrgNamespaceNames...))
					})
					var pvcSelector controllers.PvcSelector
					var err error
					JustBeforeEach(func() {
						pvcSelector, err = vrgPvcSelectorGet()
					})
					Context("and the extra-VRG namespaces feature is disabled", func() {
						BeforeEach(func() {
							extraVrgNamespacesFeatureEnabledSetAndDeferRestore(false)
							skipIfAdmissionValidationDenies()
						})
						It("returns an error", Label(), func() {
							Expect(err).To(HaveOccurred())
						})
					})
					Context("and the extra-VRG namespaces feature is enabled", func() {
						It("includes each in its PVC selection", func() {
							Expect(err).ToNot(HaveOccurred())
							Expect(pvcSelector.NamespaceNames).To(ConsistOf(extraVrgNamespaceNames))
						})
					})
				})
			})
			Context("in a non-administrator namespace and the extra-VRG namespaces feature is enabled", func() {
				var extraVrgNamespaceName string
				BeforeEach(OncePerOrdered, func() {
					vrg.Namespace = nss[0].Name
					extraVrgNamespaceName = nss[1].Name
				})
				var pvcSelector controllers.PvcSelector
				var err error
				JustBeforeEach(func() {
					pvcSelector, err = vrgPvcSelectorGet()
				})
				Context("whose recipe references no namespaces", func() {
					It("s PVC selector includes only the VRG's namespace", func() {
						Expect(err).ToNot(HaveOccurred())
						Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))
					})
				})
				Context("whose recipe references only the same namespace", func() {
					BeforeEach(func() {
						recipeVolumesDefine(volumes(vrg.Namespace))
					})
					It("s PVC selector includes only the VRG's namespace", func() {
						Expect(err).ToNot(HaveOccurred())
						Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))
					})
				})
				Context("whose recipe references", Ordered, func() {
					Context("the same namespace and another namespace initially", func() {
						BeforeAll(func() {
							recipeVolumesDefine(volumes(vrg.Namespace, extraVrgNamespaceName))
							skipIfAdmissionValidationDenies()
						})
						It("s PVC selector is invalid", func() {
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
					})
					Context("only the same namespace after an update to remove another namespace", func() {
						BeforeAll(func() {
							Expect(apiReader.Get(ctx, client.ObjectKeyFromObject(r), r)).To(Succeed())
							recipeVolumesDefine(volumes(vrg.Namespace))
							Expect(k8sClient.Update(ctx, r)).To(Succeed())
						})
						It("s PVC selector includes only the VRG's namespace", func() {
							Expect(err).ToNot(HaveOccurred())
							Expect(pvcSelector.NamespaceNames).To(ConsistOf(vrg.Namespace))
						})
						It("sets DataReady condition's message to something besides a recipe error", func() {
							Eventually(vrgDataReadyConditionGetAndExpectNonNil).Should(MatchFields(IgnoreExtras, Fields{
								"Message": Not(HavePrefix(recipeErrorMessagePrefix)),
							}))
						})
					})
				})
			})
		})
	})
})
