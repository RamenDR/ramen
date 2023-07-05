// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"
)

func configMapUpdate() {
	ramenConfigYaml, err := yaml.Marshal(ramenConfig)
	Expect(err).NotTo(HaveOccurred())

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		key := types.NamespacedName{
			Namespace: ramenNamespace,
			Name:      controllers.HubOperatorConfigMapName,
		}

		err := k8sClient.Get(context.TODO(), key, configMap)
		if err != nil {
			return err
		}

		configMap.Data[controllers.ConfigMapRamenConfigKeyName] = string(ramenConfigYaml)

		return k8sClient.Update(context.TODO(), configMap)
	})

	Expect(retryErr).NotTo(HaveOccurred())
}

func s3ProfilesStore(s3Profiles []ramen.S3StoreProfile) {
	ramenConfig.S3StoreProfiles = s3Profiles

	configMapUpdate()
}
