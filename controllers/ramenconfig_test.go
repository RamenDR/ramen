// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	"github.com/ghodss/yaml"
	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
)

func configMapUpdate() {
	ramenConfigYaml, err := yaml.Marshal(ramenConfig)
	Expect(err).To(Succeed())

	configMap.Data[controllers.ConfigMapRamenConfigKeyName] = string(ramenConfigYaml)

	Expect(k8sClient.Update(context.TODO(), configMap)).To(Succeed())
}

func s3ProfilesStore(s3Profiles []ramen.S3StoreProfile) {
	ramenConfig.S3StoreProfiles = s3Profiles

	configMapUpdate()
}
