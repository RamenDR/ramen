// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"
)

var configMapNames = []string{
	controllers.HubOperatorConfigMapName,
	controllers.DrClusterOperatorConfigMapName,
}

func configMapCreate(ramenConfig *ramen.RamenConfig) {
	for _, configMapName := range configMapNames {
		configMap, err := controllers.ConfigMapNew(ramenNamespace, configMapName, ramenConfig)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Create(context.TODO(), configMap)).To(Succeed())
		DeferCleanup(k8sClient.Delete, context.TODO(), configMap)
	}
}

func configMapUpdate() {
	ramenConfigYaml, err := yaml.Marshal(ramenConfig)
	Expect(err).NotTo(HaveOccurred())

	for _, configMapName := range configMapNames {
		configMapUpdate1(configMapName, ramenConfigYaml)
	}
}

func configMapUpdate1(configMapName string, ramenConfigYaml []byte) {
	configMap := &corev1.ConfigMap{}

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		key := types.NamespacedName{
			Namespace: ramenNamespace,
			Name:      configMapName,
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
