// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
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

var fakeObjectStorers = make(map[string]*fakeObjectStorer)

func (fakeObjectStoreGetter) ObjectStore(
	ctx context.Context,
	apiReader client.Reader,
	s3ProfileName string,
	callerTag string,
	log logr.Logger,
) (controllers.ObjectStorer, ramen.S3StoreProfile, error) {
	s3StoreProfile, err := controllers.GetRamenConfigS3StoreProfile(ctx, apiReader, s3ProfileName)
	if err != nil {
		return nil, s3StoreProfile, fmt.Errorf("failed to get profile %s for caller %s, %w", s3ProfileName, callerTag, err)
	}

	switch s3StoreProfile.S3Bucket {
	case bucketNameFail:
		fallthrough
	case bucketNameFail2:
		return nil, s3StoreProfile, fmt.Errorf("bucket '%v' invalid", s3StoreProfile.S3Bucket)
	}

	accessID, _, err := controllers.GetS3Secret(ctx, apiReader, s3StoreProfile.S3SecretRef)
	if err != nil {
		return nil, s3StoreProfile, fmt.Errorf("failed to get secret %v for caller %s, %w",
			s3StoreProfile.S3SecretRef, callerTag, err)
	}

	accessIDString := string(accessID)
	if accessIDString == awsAccessKeyIDFail {
		return nil, s3StoreProfile, fmt.Errorf("AWS_ACCESS_KEY_ID '%v' invalid", accessIDString)
	}

	objectStorer, ok := fakeObjectStorers[s3ProfileName]
	if !ok {
		objectStorer = &fakeObjectStorer{
			name:       s3ProfileName,
			bucketName: s3StoreProfile.S3Bucket,
			objects:    make(map[string]interface{}),
		}
		fakeObjectStorers[s3ProfileName] = objectStorer
	}

	return objectStorer, s3StoreProfile, nil
}

type fakeObjectStorer struct {
	name       string
	bucketName string
	objects    map[string]interface{}
	mutex      sync.Mutex
}

func (f *fakeObjectStorer) UploadObject(key string, object interface{}) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if f.bucketName == bucketNameUploadAwsErr {
		return awserr.New(s3.ErrCodeInvalidObjectState, "fake error uploading object", fmt.Errorf("fake error"))
	}

	f.objects[key] = object

	return nil
}

func (f *fakeObjectStorer) DownloadObject(key string, objectPointer interface{}) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	object, ok := f.objects[key]

	objectDestination := reflect.ValueOf(objectPointer).Elem()
	Expect(objectDestination.CanSet()).To(BeTrue())

	if !ok {
		return fs.ErrNotExist
	}

	objectSource := reflect.ValueOf(object)
	Expect(objectSource.IsValid()).To(BeTrue())
	objectDestination.Set(objectSource)

	return nil
}

func (f *fakeObjectStorer) ListKeys(keyPrefix string) ([]string, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

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

func (f *fakeObjectStorer) DeleteObject(key string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	delete(f.objects, key)

	return nil
}

func (f *fakeObjectStorer) DeleteObjects(keys ...string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	for _, key := range keys {
		delete(f.objects, key)
	}

	return nil
}

func (f *fakeObjectStorer) DeleteObjectsWithKeyPrefix(keyPrefix string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	for key := range f.objects {
		if strings.HasPrefix(key, keyPrefix) {
			delete(f.objects, key)
		}
	}

	return nil
}

var _ = Describe("FakeObjectStorer", func() {
	var objectStorer controllers.ObjectStorer
	object := "o"
	const (
		key  = "k"
		key1 = key + key
		key2 = key1 + key
	)
	BeforeEach(func() {
		objectStorer = objectStorers[objS3ProfileNumber]
	})
	Context("DownloadObject", func() {
		BeforeEach(func() {
			Expect(objectStorer.UploadObject(key, object)).To(Succeed())
		})
		It("should download an uploaded object", func() {
			var object1 string
			Expect(objectStorer.DownloadObject(key, &object1)).To(Succeed())
			Expect(object1).To(Equal(object))
		})
		It("should not download a non-uploaded object", func() {
			var object1 string
			Expect(objectStorer.DownloadObject(key1, &object1)).To(MatchError(fs.ErrNotExist))
		})
	})
	Context("DeleteObject", func() {
		BeforeEach(func() {
			Expect(objectStorer.UploadObject(key, object)).To(Succeed())
			Expect(objectStorer.UploadObject(key1, object)).To(Succeed())
			Expect(objectStorer.DeleteObject(key1)).To(Succeed())
		})
		It("should delete an uploaded object", func() {
			var object1 string
			Expect(objectStorer.DownloadObject(key1, &object1)).To(MatchError(fs.ErrNotExist))
		})
		It("should not delete an uploaded object with same prefix as specified key", func() {
			var object1 string
			Expect(objectStorer.DownloadObject(key, &object1)).To(Succeed())
		})
		It("should return nil if an object with specified key was not uploaded", func() {
			Expect(objectStorer.DeleteObject(key2)).To(Succeed())
		})
	})
})
