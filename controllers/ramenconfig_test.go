/*
Copyright 2022 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
