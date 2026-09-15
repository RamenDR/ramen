// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"

	ramen "github.com/ramendr/ramen/api/v1alpha1"

	"github.com/ramendr/ramen/e2e/types"
)

func validateVRGs(ctx types.TestContext, primary, secondary *types.Cluster) error {
	if err := validateVRG(ctx, primary, ramen.Primary, ramen.PrimaryState); err != nil {
		return err
	}

	return validateVRG(ctx, secondary, ramen.Secondary, ramen.SecondaryState)
}

func validateVRG(
	ctx types.TestContext,
	cluster *types.Cluster,
	desiredState ramen.ReplicationState,
	actualState ramen.State,
) error {
	log := ctx.Logger()
	name := ctx.Name()
	namespace := vrgNamespace(ctx)

	vrg, err := getVRG(ctx, cluster, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get vrg \"%s/%s\" in cluster %q: %w",
			namespace, name, cluster.Name, err)
	}

	if vrg.Spec.ReplicationState != desiredState || vrg.Status.State != actualState {
		return fmt.Errorf("vrg \"%s/%s\" in cluster %q is %s/%s, expected %s/%s",
			namespace, name, cluster.Name,
			vrg.Spec.ReplicationState, vrg.Status.State, desiredState, actualState)
	}

	log.Debugf("vrg \"%s/%s\" is %s/%s in cluster %q",
		namespace, name, desiredState, actualState, cluster.Name)

	return nil
}

func vrgNamespace(ctx types.TestContext) string {
	if ctx.Deployer().IsDiscovered() {
		return ctx.ManagementNamespace()
	}

	return ctx.AppNamespace()
}
