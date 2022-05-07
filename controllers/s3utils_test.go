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

package controllers_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/controllers"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type fakeObjectStoreGetter struct{}

const (
	bucketNameSucc  = "bucket"
	bucketNameSucc2 = bucketNameSucc + "2"
	bucketNameFail  = bucketNameSucc + "Fail"
	bucketNameFail2 = bucketNameFail + "2"
	bucketListFail  = bucketNameSucc + "ListFail"

	awsAccessKeyIDSucc = "succ"
	awsAccessKeyIDFail = "fail"
)

func (fakeObjectStoreGetter) ObjectStore(
	ctx context.Context,
	apiReader client.Reader,
	s3ProfileName string,
	callerTag string,
	log logr.Logger,
) (controllers.ObjectStorer, error) {
	s3StoreProfile, err := controllers.GetRamenConfigS3StoreProfile(ctx, apiReader, s3ProfileName)
	if err != nil {
		return nil, fmt.Errorf("failed to get profile %s for caller %s, %w", s3ProfileName, callerTag, err)
	}

	switch s3StoreProfile.S3Bucket {
	case bucketNameFail:
		fallthrough
	case bucketNameFail2:
		return nil, fmt.Errorf("bucket '%v' invalid", s3StoreProfile.S3Bucket)
	}

	accessID, _, err := controllers.GetS3Secret(ctx, apiReader, s3StoreProfile.S3SecretRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %v for caller %s, %w",
			s3StoreProfile.S3SecretRef, callerTag, err)
	}

	accessIDString := string(accessID)
	if accessIDString == awsAccessKeyIDFail {
		return nil, fmt.Errorf("AWS_ACCESS_KEY_ID '%v' invalid", accessIDString)
	}

	return fakeObjectStorer{
		name:       s3ProfileName,
		bucketName: s3StoreProfile.S3Bucket,
	}, nil
}

type fakeObjectStorer struct {
	name       string
	bucketName string
}

func (f fakeObjectStorer) fullPrefix(keyPrefix string) string {
	return f.GetName() + keyPrefix
}

func (f fakeObjectStorer) UploadPV(pvKeyPrefix, pvKeySuffix string, pv corev1.PersistentVolume) error {
	key := f.fullPrefix(pvKeyPrefix) + pv.Name
	UploadedPVs[key] = pv

	return nil
}

func (f fakeObjectStorer) GetName() string { return f.name }

func (f fakeObjectStorer) DownloadPVs(pvKeyPrefix string) ([]corev1.PersistentVolume, error) {
	fullPrefix := f.fullPrefix(pvKeyPrefix)
	pvList := []corev1.PersistentVolume{}

	for k, v := range UploadedPVs {
		if strings.HasPrefix(k, fullPrefix) {
			pvList = append(pvList, v.(corev1.PersistentVolume))
		}
	}

	return pvList, nil
}

func (f fakeObjectStorer) ListKeys(keyPrefix string) ([]string, error) {
	if f.bucketName == bucketListFail {
		return nil, fmt.Errorf("Failing bucket listing")
	}

	fullPrefix := f.fullPrefix(keyPrefix)
	keys := []string{}

	for k := range UploadedPVs {
		if strings.HasPrefix(k, fullPrefix) {
			keys = append(keys, k)
		}
	}

	return keys, nil
}

func (fakeObjectStorer) DeleteObjects(keyPrefix string) error {
	for key := range UploadedPVs {
		delete(UploadedPVs, key)
	}

	return nil
}
