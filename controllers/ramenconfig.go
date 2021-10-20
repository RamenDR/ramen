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
	"fmt"
	"io/ioutil"
	"net/url"
	"os"

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	"github.com/prometheus/common/log"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

var cachedRamenConfigFileName string

func LoadControllerConfig(configFile string, scheme *runtime.Scheme,
	log logr.Logger) (options ctrl.Options, ramenConfig ramendrv1alpha1.RamenConfig) {
	if configFile == "" {
		log.Info("Ramen config file not specified")

		return
	}

	log.Info("loading Ramen configuration from ", "file", configFile)

	cachedRamenConfigFileName = configFile
	options.Scheme = scheme

	options, err := options.AndFrom(
		ctrl.ConfigFile().AtPath(configFile).OfKind(&ramenConfig))
	if err != nil {
		log.Error(err, "unable to load the config file")
		os.Exit(1)

		return
	}

	for profileName, s3Profile := range ramenConfig.S3StoreProfiles {
		log.Info("s3 profile", "key", profileName, "value", s3Profile)
	}

	return
}

// Read the RamenConfig file mounted in the local file system.  This file is
// expected to be cached in the local file system.  If reading of the
// RamenConfig file for every S3 store profile access turns out to be more
// expensive, we may need to enhance this logic to load it only when
// RamenConfig has changed.
func ReadRamenConfig() (ramenConfig ramendrv1alpha1.RamenConfig, err error) {
	if cachedRamenConfigFileName == "" {
		err = fmt.Errorf("config file not specified")

		return
	}

	log.Info("loading Ramen config file ", "name", cachedRamenConfigFileName)

	fileContents, err := ioutil.ReadFile(cachedRamenConfigFileName)
	if err != nil {
		err = fmt.Errorf("unable to load the config file %s: %w",
			cachedRamenConfigFileName, err)

		return
	}

	err = yaml.Unmarshal(fileContents, &ramenConfig)
	if err != nil {
		err = fmt.Errorf("unable to marshal the config file %s: %w",
			cachedRamenConfigFileName, err)

		return
	}

	return
}

func getRamenConfigS3StoreProfile(profileName string) (
	s3StoreProfile ramendrv1alpha1.S3StoreProfile, err error) {
	ramenConfig, err := ReadRamenConfig()
	if err != nil {
		return s3StoreProfile, err
	}

	profileExists := false

	for _, s3Profile := range ramenConfig.S3StoreProfiles {
		if s3Profile.S3ProfileName == profileName {
			s3StoreProfile = s3Profile
			profileExists = true

			break // found profile
		}
	}

	if !profileExists {
		err = fmt.Errorf("s3 profile %s not found in RamenConfig", profileName)

		return s3StoreProfile, err
	}

	s3Endpoint := s3StoreProfile.S3CompatibleEndpoint
	if s3Endpoint == "" {
		err = fmt.Errorf("s3 endpoint has not been configured in s3 profile %s",
			profileName)

		return s3StoreProfile, err
	}

	_, err = url.ParseRequestURI(s3Endpoint)
	if err != nil {
		err = fmt.Errorf("invalid s3 endpoint <%s> in "+
			"profile %s, reason: %w", s3Endpoint, profileName, err)

		return s3StoreProfile, err
	}

	s3Bucket := s3StoreProfile.S3Bucket
	if s3Bucket == "" {
		err = fmt.Errorf("s3 bucket has not been configured in s3 profile %s",
			profileName)

		return s3StoreProfile, err
	}

	return s3StoreProfile, nil
}

func getMaxConcurrentReconciles() int {
	const defaultMaxConcurrentReconciles = 1

	ramenConfig, err := ReadRamenConfig()
	if err != nil {
		return defaultMaxConcurrentReconciles
	}

	if ramenConfig.MaxConcurrentReconciles == 0 {
		return defaultMaxConcurrentReconciles
	}

	return ramenConfig.MaxConcurrentReconciles
}
