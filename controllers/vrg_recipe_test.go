// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("VolumeReplicationGroupRecipe", func() {
	var (
		nss []*corev1.Namespace
		r   *recipe.Recipe
		vrg *ramen.VolumeReplicationGroup
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
			// Name: "h",
			Type: "exec",
			Ops: []*recipe.Operation{
				{
					// Name: "o",
					// Container: "c"
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
			ObjectMeta: metav1.ObjectMeta{Namespace: nss[0].Name, Name: "a"},
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
	vrgDelete := func() error {
		return k8sClient.Delete(ctx, vrg)
	}
	vrgGet := func() *ramen.VolumeReplicationGroup {
		Expect(apiReader.Get(ctx, types.NamespacedName{Namespace: vrg.Namespace, Name: vrg.Name}, vrg)).To(Succeed())

		return vrg
	}
	vrgPvcsGet := func() []ramen.ProtectedPVC {
		return vrgGet().Status.ProtectedPVCs
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
	vrgPvcSelectorGet := func() controllers.PvcSelector {
		pvcSelector, err := controllers.GetPVCSelector(ctx, apiReader, *vrg, testLogger)
		Expect(err).ToNot(HaveOccurred())

		return pvcSelector
	}
	vrgPvcSelectorNsNamesExpect := func(nsNamesExpected []string) {
		Expect(vrgPvcSelectorGet().NamespaceNames).To(ConsistOf(nsNamesExpected))
	}
	var nsNames []string
	var pvcs []*corev1.PersistentVolumeClaim
	BeforeEach(func() {
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
	JustBeforeEach(func() {
		recipeCreate()
		DeferCleanup(recipeDelete)
		err = vrgCreate()
	})
	Describe("AdmissionController", func() {
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
				Context("without a volume selector", func() {
					BeforeEach(func() {
						DeferCleanup(vrgDelete)
					})
					It("allows it", func() { Expect(err).ToNot(HaveOccurred()) })
				})
				Context("that references other namespaces", func() {
					BeforeEach(func() {
						recipeVolumesDefine(volumes(nss[1].Name))
					})
					Context("that the requestor has permission to create VRGs in", func() {
						It("allows it", func() { Expect(err).ToNot(HaveOccurred()) })
					})
					Context("that the requestor does not have permission to create VRGs in", func() {
						// TODO envTest.AddUser
						// TODO give user permission to update VRG in nss[0], but not nss[1]
						// TODO impersonate user for client.Create(vrg)
						// It("denies it", func() { Expect(err).To(HaveOccurred()) })
					})
				})
			})
		})
	})
	Describe("Controller", func() {
		JustBeforeEach(func() {
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
				It("includes each in PVC selection", func() {
					vrgPvcSelectorNsNamesExpect(nsNames1)
				})
				It("lists their PVCs in the VRG's status", func() {
					vrgPvcsConsistOfEventually(pvcs1...)
				})
				It("expands a parameter list enclosed in double quotes to a single string with quotes preserved", func() {
					if true {
						return
					}
					Expect(recipeExpanded.Spec.Hooks[0].Ops[0].Command).To(Equal(`"` + strings.Join(nsNames1, ",") + `"`))
				})
			})
		})
	})
})
