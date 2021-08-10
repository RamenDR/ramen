/*
Copyright 2021 The RamenDR authors.

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

package controllers

import (
	"io/ioutil"
	"os"

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
// cachedRamenConfig ramendrv1alpha1.RamenConfig
)

func LoadControllerConfig(configFile string, scheme *runtime.Scheme,
	log logr.Logger) (options ctrl.Options, ramenConfig ramendrv1alpha1.RamenConfig) {
	if configFile == "" {
		log.Info("Ramen config file not specified")

		return
	}

	log.Info("loading Ramen config file ", "name", configFile)

	options.Scheme = scheme

	options, err := options.AndFrom(
		ctrl.ConfigFile().
			AtPath(configFile).
			OfKind(&ramenConfig))
	if err != nil {
		log.Error(err, "unable to load the config file")
		os.Exit(1)

		return
	}

	// cachedRamenConfig = ramenConfig
	for profileName, s3Profile := range ramenConfig.S3StoreProfiles {
		log.Info("s3 profile", "key", profileName, "value", s3Profile)
	}

	return
}

func LoadRamenConfig(configFile string, log logr.Logger) (
	ramenConfig ramendrv1alpha1.RamenConfig, ok bool) {
	if configFile == "" {
		log.Info("Ramen config file not specified")

		return
	}

	log.Info("loading Ramen config file ", "name", configFile)

	fileContents, err := ioutil.ReadFile(configFile)
	if err != nil {
		log.Error(err, "unable to load the config file")

		return
	}

	err = yaml.Unmarshal(fileContents, &ramenConfig)
	if err != nil {
		log.Error(err, "unable to marshal the config file")

		return
	}

	// cachedRamenConfig = ramenConfig
	ok = true

	return
}
