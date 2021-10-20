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
	"errors"
	"reflect"

	"github.com/ramendr/ramen/controllers"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type fakeObjectStoreGetter struct{}

const (
	s3ProfileNameConnectSucc = `fakeS3Profile`
	s3ProfileNameConnectFail = s3ProfileNameConnectSucc + `ConnectFail`
)

func (fakeObjectStoreGetter) ObjectStore(
	ctx context.Context,
	apiReader client.Reader,
	s3ProfileName string,
	callerTag string,
) (controllers.ObjectStorer, error) {
	if s3ProfileName == s3ProfileNameConnectFail {
		return fakeObjectStorer{}, errors.New(`object store connection failed`)
	}

	return fakeObjectStorer{}, nil
}

type fakeObjectStorer struct{}

func (fakeObjectStorer) CreateBucket(bucket string) error { return nil }
func (fakeObjectStorer) DeleteBucket(bucket string) error { return nil }
func (fakeObjectStorer) PurgeBucket(bucket string) error  { return nil }
func (fakeObjectStorer) UploadPV(pvKeyPrefix, pvKeySuffix string, pv corev1.PersistentVolume) error {
	return nil
}

func (fakeObjectStorer) UploadTypedObject(pvKeyPrefix, keySuffix string, uploadContent interface{}) error {
	return nil
}

func (fakeObjectStorer) UploadObject(key string, uploadContent interface{}) error {
	return nil
}

func (fakeObjectStorer) VerifyPVUpload(pvKeyPrefix, pvKeySuffix string,
	verifyPV corev1.PersistentVolume) error {
	return nil
}

func (fakeObjectStorer) DownloadPVs(pvKeyPrefix string) ([]corev1.PersistentVolume, error) {
	return []corev1.PersistentVolume{}, nil
}

func (fakeObjectStorer) DownloadTypedObjects(keyPrefix string, objectType reflect.Type) (interface{}, error) {
	return nil, nil
}

func (fakeObjectStorer) ListKeys(keyPrefix string) ([]string, error) {
	return []string{}, nil
}

func (fakeObjectStorer) DownloadObject(key string, downloadContent interface{}) error {
	return nil
}
func (fakeObjectStorer) DeleteObjects(keyPrefix string) error { return nil }
