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
	"fmt"

	"github.com/ghodss/yaml"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func configMapUpdate(
	ctx context.Context,
	client client.Client,
	configMap *corev1.ConfigMap,
	ramenConfig *ramen.RamenConfig,
) error {
	ramenConfigYaml, err := yaml.Marshal(ramenConfig)
	if err != nil {
		return fmt.Errorf("config map yaml marshal %w", err)
	}

	configMap.Data[controllers.ConfigMapRamenConfigKeyName] = string(ramenConfigYaml)

	return client.Update(ctx, configMap)
}

func s3ProfilesStore(
	ctx context.Context,
	apiReader client.Reader,
	client client.Client,
	s3Profiles []ramen.S3StoreProfile,
) error {
	configMap, ramenConfig, err := controllers.ConfigMapGet(ctx, apiReader)
	if err != nil {
		return fmt.Errorf("config map get %w", err)
	}

	ramenConfig.S3StoreProfiles = s3Profiles

	return configMapUpdate(ctx, client, configMap, ramenConfig)
}
