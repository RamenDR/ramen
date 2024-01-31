// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	volrep "github.com/csi-addons/kubernetes-csi-addons/apis/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	ocmclv1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	argocdv1alpha1hack "github.com/ramendr/ramen/pkg/argocd"
	Recipe "github.com/ramendr/recipe/api/v1alpha1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/client-go/kubernetes/scheme"
	cpcv1 "open-cluster-management.io/config-policy-controller/api/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func AddSchemes() error {
	err := ocmworkv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = ocmclv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = plrv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = viewv1beta1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = cpcv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = gppv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = ramendrv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = Recipe.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = volrep.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = volsyncv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = snapv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = velero.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = clrapiv1beta1.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	err = argocdv1alpha1hack.AddToScheme(scheme.Scheme)
	if err != nil {
		return err
	}

	return nil
}
