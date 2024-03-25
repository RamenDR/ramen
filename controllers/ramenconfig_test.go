// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	}
}

func configMapDelete() error {
	for _, configMapName := range configMapNames {
		cm := &corev1.ConfigMap{}

		err := k8sClient.Get(context.TODO(), types.NamespacedName{
			Namespace: ramenNamespace,
			Name:      configMapName,
		}, cm)
		if err != nil && !errors.IsNotFound(err) {
			return err
		}

		err = k8sClient.Delete(context.TODO(), cm)
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
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
