// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"reflect"

	"github.com/go-logr/logr"
	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FinalizePlacement prepares a Placement for scheduling by OCM.
// NOTE: We Keep the cluster.open-cluster-management.io/experimental-scheduling-disable: "true"
// annotation as is, so if the placment is managed by gitops and our changes are overridden, OCM
// will not change the placement, resulting in data loss. Since we change user owned resource, we
// log every spec change.
func FinalizePlacement(p *clrapiv1beta1.Placement, cluster string, log logr.Logger) {
	var desiredNumberOfClusters int32 = 1

	if p.Spec.NumberOfClusters == nil || *p.Spec.NumberOfClusters != desiredNumberOfClusters {
		log.Info("NOTE: modifying Placement numberOfClusters to 1",
			"namespace", p.Namespace, "placementrule", p.Name)

		p.Spec.NumberOfClusters = &desiredNumberOfClusters
	}

	var desiredPredicates []clrapiv1beta1.ClusterPredicate

	if cluster != "" {
		desiredPredicates = []clrapiv1beta1.ClusterPredicate{
			{
				RequiredClusterSelector: clrapiv1beta1.ClusterSelector{
					LabelSelector: metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "name",
								Operator: "In",
								Values:   []string{cluster},
							},
						},
					},
				},
			},
		}
	}

	if !reflect.DeepEqual(p.Spec.Predicates, desiredPredicates) {
		log.Info("NOTE: modifying placement predicates to select current cluster",
			"namespace", p.Namespace, "placement", p.Name, "cluster", cluster)

		p.Spec.Predicates = desiredPredicates
	}
}

// FinalizePlacementRule prepares a PlacementRule for scheduling by OCM.
// NOTE: We keep schedulerName: ramen as is, so if the placment rule is managed by gitops and our
// changes are overridden, OCM will not change the placement, resulting in data loss. Since we
// change user owned resource, we log every spec change.
func FinalizePlacementRule(pr *plrv1.PlacementRule, cluster string, log logr.Logger) {
	var desiredClusterReplicas int32 = 1

	if pr.Spec.ClusterReplicas == nil || *pr.Spec.ClusterReplicas != desiredClusterReplicas {
		log.Info("NOTE: modifying PlacementRule clusterReplicas to 1",
			"namespace", pr.Namespace, "placementrule", pr.Name)

		pr.Spec.ClusterReplicas = &desiredClusterReplicas
	}

	var desiredClusterSelector *metav1.LabelSelector

	if cluster != "" {
		desiredClusterSelector = &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "name",
					Operator: "In",
					Values:   []string{cluster},
				},
			},
		}
	}

	if !reflect.DeepEqual(pr.Spec.ClusterSelector, desiredClusterSelector) {
		log.Info("NOTE: modifying PlacementRule clusterSelector to select current cluster",
			"namespace", pr.Namespace, "placementrule", pr.Name, "cluster", cluster)

		pr.Spec.ClusterSelector = desiredClusterSelector
	}
}
