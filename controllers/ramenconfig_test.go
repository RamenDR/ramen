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
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	config "k8s.io/component-base/config/v1alpha1"
	controller_runtime "sigs.k8s.io/controller-runtime"
	controller_runtime_config "sigs.k8s.io/controller-runtime/pkg/config/v1alpha1"
)

func ramenConfigStore(ramenConfig *ramen.RamenConfig, fileName string) error {
	data, err := yaml.Marshal(ramenConfig)
	if err != nil {
		return fmt.Errorf("yaml marshal %v: %w", ramenConfig, err)
	}

	return ioutil.WriteFile(fileName, data, 0o600)
}

func ramenConfigCreate(scheme *runtime.Scheme, log logr.Logger) (*controller_runtime.Options, error) {
	dirName, err := ioutil.TempDir("", "ramen-test-")
	if err != nil {
		return nil, fmt.Errorf("temporary directory create: %w", err)
	}

	fileName := filepath.Join(dirName, "config.yaml")

	if err := ramenConfigStore(
		&ramen.RamenConfig{
			TypeMeta: metav1.TypeMeta{
				Kind:       "RamenConfig",
				APIVersion: ramen.GroupVersion.String(),
			},
			ControllerManagerConfigurationSpec: controller_runtime_config.ControllerManagerConfigurationSpec{
				LeaderElection: &config.LeaderElectionConfiguration{
					LeaderElect: new(bool),
				},
			},
			RamenControllerType: ramen.DRHub,
			S3StoreProfiles: []ramen.S3StoreProfile{
				{
					S3ProfileName:        "asdf",
					S3Bucket:             "bucket",
					S3CompatibleEndpoint: "http://192.168.39.223:30000",
					S3Region:             "us-east-1",
					S3SecretRef: v1.SecretReference{
						Name:      "s3secret",
						Namespace: "ramen-system",
					},
				},
			},
		},
		fileName,
	); err != nil {
		return nil, err
	}

	options, _ := controllers.LoadControllerConfig(fileName, scheme, log)

	return &options, nil
}
