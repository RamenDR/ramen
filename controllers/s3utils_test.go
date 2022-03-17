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
	"reflect"
	"strings"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/controllers"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type fakeObjectStoreGetter struct{}

const (
	bucketNameSucc         = "bucket"
	bucketNameSucc2        = bucketNameSucc + "2"
	bucketNameFail         = bucketNameSucc + "Fail"
	bucketNameFail2        = bucketNameFail + "2"
	bucketListFail         = bucketNameSucc + "ListFail"
	bucketNameUploadAwsErr = bucketNameFail + "UploadAwsErr"

	awsAccessKeyIDSucc = "succ"
	awsAccessKeyIDFail = "fail"
)

var fakeObjectStorers = make(map[string]fakeObjectStorer)

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

	objectStorer, ok := fakeObjectStorers[s3ProfileName]
	if !ok {
		objectStorer = fakeObjectStorer{
			name:       s3ProfileName,
			url:        s3StoreProfile.S3CompatibleEndpoint,
			bucketName: s3StoreProfile.S3Bucket,
			objects:    make(map[string]interface{}),
		}
		fakeObjectStorers[s3ProfileName] = objectStorer
	}

	return objectStorer, nil
}

type fakeObjectStorer struct {
	name       string
	url        string
	bucketName string
	objects    map[string]interface{}
}

func (f fakeObjectStorer) AddressComponent1() string { return f.url }
func (f fakeObjectStorer) AddressComponent2() string { return f.bucketName }

func (f fakeObjectStorer) UploadObject(key string, object interface{}) error {
	if f.bucketName == bucketNameUploadAwsErr {
		return awserr.New(s3.ErrCodeInvalidObjectState, "fake error uploading object", fmt.Errorf("fake error"))
	}

	f.objects[key] = object

	return nil
}

func (f fakeObjectStorer) DownloadObject(key string, objectPointer interface{}) error {
	reflect.ValueOf(objectPointer).Elem().Set(reflect.ValueOf(f.objects[key]))

	return nil
}

func (f fakeObjectStorer) ListKeys(keyPrefix string) ([]string, error) {
	if f.bucketName == bucketListFail {
		return nil, fmt.Errorf("Failing bucket listing")
	}

	keys := []string{}

	for k := range f.objects {
		if strings.HasPrefix(k, keyPrefix) {
			keys = append(keys, k)
		}
	}

	return keys, nil
}

func (f fakeObjectStorer) DeleteObjects(keyPrefix string) error {
	for key := range f.objects {
		if strings.HasPrefix(key, keyPrefix) {
			delete(f.objects, key)
		}
	}

	return nil
}
