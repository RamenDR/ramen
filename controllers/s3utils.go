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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	errorswrapper "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Example usage:
// func example_code() {
// *** setup a new s3 object store ***
// s3endpoint := "http://127.0.0.1:9000"
// s3secretname := types.namespacedname{name: s3secretname, namespace: parent.namespace}

// s3conn, err := connecttos3endpoint(ctx, reconciler, s3endpoint, s3secretname)
// if err != nil {
// 	return err
// }
// *** create a new bucket ***
// bucket := "subname-namespace" // should be all lowercase
// if err := s3Conn.createBucket(bucket); err != nil {
// 	return err
// }

// *** Upload objects, optionally using a key prefix to easily find the objects later ***
// for i := 1; i < 10; i++ {
// 	pvKey := fmt.Sprintf("PersistentVolumes/pv%v", i)
// 	uploadPV := corev1.PersistentVolume{}
// 	uploadPV.Name = pvKey
// 	uploadPV.Spec.StorageClassName = "gold"
// 	uploadPV.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
// 	if err := s3Conn.uploadObject(bucket, pvKey, uploadPV); err != nil {
// 		return err
// 	}
// }

// *** Find objects in the bucket, optionally supplying a key prefix
// keyPrefix := "v1.PersistentVolumes/"
// if list, err := s3Conn.listKeys(bucket, keyPrefix); err != nil {
// 	return err
// } else {
// 	for _, key := range list {
// 		fmt.Printf("%v ", key)
// 	}
// }

// *** Download from the given bucket an object with the given key
// keyPrefix := "v1.PersistentVolumes/"
// key := keyPrefix + "pv2"
// var downloadPV corev1.PersistentVolume
// if err := s3Conn.downloadObject(bucket, key, &downloadPV); err != nil {
// 	return err
// }
// }

// ObjectStoreGetter interface is exported because test clients
// use this interface.
type ObjectStoreGetter interface {
	// objectStore returns an object that satisfies objectStorer interface
	objectStore(ctx context.Context, r client.Reader,
		s3Profile string, callerTag string) (objectStorer, error)
}

type objectStorer interface {
	createBucket(bucket string) error
	uploadPV(bucket string, pvKeySuffix string,
		pv corev1.PersistentVolume) error
	uploadTypedObject(bucket string, keySuffix string,
		uploadContent interface{}) error
	uploadObject(bucket string, key string,
		uploadContent interface{}) error
	verifyPVUpload(bucket string, pvKeySuffix string,
		verifyPV corev1.PersistentVolume) error
	downloadPVs(bucket string) (pvList []corev1.PersistentVolume, err error)
	downloadTypedObjects(bucket string,
		objectType reflect.Type) (interface{}, error)
	listKeys(bucket string, keyPrefix string) (keys []string, err error)
	downloadObject(bucket string, key string, downloadContent interface{}) error
	deleteObject(bucket, keyPrefix string) error
}

// S3ObjectStoreGetter returns a concrete type that implements
// the ObjectStoreGetter interface, allowing the concrete type
// to be not exported.
func S3ObjectStoreGetter() ObjectStoreGetter {
	return s3ObjectStoreGetter{}
}

// s3ObjectStoreGetter is a private concrete type that implements
// the ObjectStoreGetter interface.
type s3ObjectStoreGetter struct{}

// objectStore returns an S3 object store that satisfies the objectStorer
// interface,  with a downloader and an uploader client connections, by either
// creating a new connection or returning a previously established connection
// for the given s3 profile.  Returns an error if s3 profile does not exists,
// secret is not configured, or if client session creation fails.
func (s3ObjectStoreGetter) objectStore(ctx context.Context,
	r client.Reader, s3ProfileName string,
	callerTag string) (objectStorer, error) {
	s3StoreProfile, err := getRamenConfigS3StoreProfile(s3ProfileName)
	if err != nil {
		return nil, fmt.Errorf("error %w in profile %s; caller %s",
			err, s3ProfileName, callerTag)
	}

	// Use cached connection, if one exists
	s3Endpoint := s3StoreProfile.S3CompatibleEndpoint
	if s3ObjectStore, ok := s3ConnectionMap[s3Endpoint]; ok {
		return s3ObjectStore, nil
	}

	accessID, secretAccessKey, err := getS3Secret(ctx, r, s3StoreProfile.S3SecretRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %v; caller %s, %w",
			s3StoreProfile.S3SecretRef, callerTag, err)
	}

	s3Region := s3StoreProfile.S3Region

	// Create an S3 client session
	s3Session, err := session.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentials(string(accessID),
			string(secretAccessKey), ""),
		Endpoint:         aws.String(s3Endpoint),
		Region:           aws.String(s3Region),
		DisableSSL:       aws.Bool(true),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create new session for %s; caller %s, %w",
			s3Endpoint, callerTag, err)
	}

	// Create a client session
	s3Client := s3.New(s3Session)

	// Also create S3 uploader and S3 downloader which can be safely used
	// concurrently across goroutines, whereas, the s3 client session
	// does not support concurrent writers.
	s3Uploader := s3manager.NewUploaderWithClient(s3Client)
	s3Downloader := s3manager.NewDownloaderWithClient(s3Client)
	s3BatchDeleter := s3manager.NewBatchDeleteWithClient(s3Client)
	s3Conn := &s3ObjectStore{
		session:      s3Session,
		client:       s3Client,
		uploader:     s3Uploader,
		downloader:   s3Downloader,
		batchDeleter: s3BatchDeleter,
		s3Endpoint:   s3Endpoint,
		callerTag:    callerTag,
	}
	s3ConnectionMap[s3Endpoint] = s3Conn

	return s3Conn, nil
}

func getS3Secret(ctx context.Context, r client.Reader,
	secretRef corev1.SecretReference) (
	s3AccessID, s3SecretAccessKey []byte, err error) {
	secret := corev1.Secret{}
	if err := r.Get(ctx,
		types.NamespacedName{Namespace: secretRef.Namespace, Name: secretRef.Name},
		&secret); err != nil {
		return nil, nil, fmt.Errorf("failed to get secret %v, %w",
			secretRef, err)
	}

	s3AccessID = secret.Data["AWS_ACCESS_KEY_ID"]
	s3SecretAccessKey = secret.Data["AWS_SECRET_ACCESS_KEY"]

	return
}

type s3ObjectStore struct {
	session      *session.Session
	client       *s3.S3
	uploader     *s3manager.Uploader
	downloader   *s3manager.Downloader
	batchDeleter *s3manager.BatchDelete
	s3Endpoint   string
	callerTag    string
}

// S3 object store map with s3Endpoint as the key to serve as cache
var s3ConnectionMap = map[string]*s3ObjectStore{}

// createBucket creates the given bucket; does not return an error if the bucket
// exists already.
func (s *s3ObjectStore) createBucket(bucket string) (err error) {
	if bucket == "" {
		return fmt.Errorf("empty bucket name for "+
			"endpoint %s caller %s", s.s3Endpoint, s.callerTag)
	}

	defer func() {
		if r := recover(); r != nil {
			// change the named return err value
			err = fmt.Errorf("create bucket recovered for %s, with %v",
				bucket, r)
		}
	}()

	cbInput := &s3.CreateBucketInput{Bucket: &bucket}
	if err = cbInput.Validate(); err != nil {
		return fmt.Errorf("create bucket input validation failed for %s, err %w",
			bucket, err)
	}

	_, err = s.client.CreateBucket(cbInput)
	if err != nil {
		var aerr awserr.Error
		if errorswrapper.As(err, &aerr) {
			switch aerr.Code() {
			case s3.ErrCodeBucketAlreadyExists:
			case s3.ErrCodeBucketAlreadyOwnedByYou:
			default:
				return fmt.Errorf("failed to create bucket %s, %w",
					bucket, err)
			}
		}
	}

	return nil
}

// uploadPV uploads the given PV to the given bucket with a key of
// "v1.PersistentVolume/<pvKeySuffix>".
// - OK to call UploadPV() concurrently from multiple goroutines safely.
// - Expects the given bucket to be already present
func (s *s3ObjectStore) uploadPV(bucket string, pvKeySuffix string,
	pv corev1.PersistentVolume) error {
	return s.uploadTypedObject(bucket, pvKeySuffix /* key suffix */, pv)
}

// uploadTypedObject uploads to the given bucket the given uploadContent with a
// key of <objectType/keySuffix>, where objectType is the type of the
// uploadContent parameter. OK to call UploadTypedObject() concurrently from
// multiple goroutines safely.
// - Expects the given bucket to be already present
func (s *s3ObjectStore) uploadTypedObject(bucket string, keySuffix string,
	uploadContent interface{}) error {
	keyPrefix := reflect.TypeOf(uploadContent).String() + "/"
	key := keyPrefix + keySuffix

	return s.uploadObject(bucket, key, uploadContent)
}

// uploadObject uploads the given object to the given bucket with the given key.
// - OK to call UploadObject() concurrently from multiple goroutines safely.
// - Upload may fail due to many reasons: RequestError (connection error),
//   NoSuchBucket, NoSuchKey, InvalidParameter (e.g., empty key), etc.
// - Multiple consecutive forward slashes in the key are sqaushed to
//   a single forward slash, for each such occurrence
// - Any formatting changes to this method should also be reflected in the
//   downloadObject() method
// - Expects the given bucket to be already present
func (s *s3ObjectStore) uploadObject(bucket string, key string,
	uploadContent interface{}) error {
	encodedUploadContent := &bytes.Buffer{}

	gzWriter := gzip.NewWriter(encodedUploadContent)
	if err := json.NewEncoder(gzWriter).Encode(uploadContent); err != nil {
		return fmt.Errorf("failed to json encode %s:%s, %w",
			bucket, key, err)
	}

	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer of %s:%s, %w",
			bucket, key, err)
	}

	if _, err := s.uploader.Upload(&s3manager.UploadInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   encodedUploadContent,
	}); err != nil {
		return fmt.Errorf("failed to upload data of %s:%s, %w",
			bucket, key, err)
	}

	return nil
}

// verifyPVUpload verifies that the PV in the input matches the PV object
// with the given keySuffix in the given bucket.
func (s *s3ObjectStore) verifyPVUpload(bucket string, pvKeySuffix string,
	verifyPV corev1.PersistentVolume) error {
	var downloadedPV corev1.PersistentVolume

	keyPrefix := reflect.TypeOf(verifyPV).String() + "/"
	key := keyPrefix + pvKeySuffix

	err := s.downloadObject(bucket, key, &downloadedPV)
	if err != nil {
		return fmt.Errorf("unable to downloadObject for caller %s from "+
			"endpoint %s bucket %s key %s, %w",
			s.callerTag, s.s3Endpoint, bucket, key, err)
	}

	if !reflect.DeepEqual(verifyPV, downloadedPV) {
		return fmt.Errorf("failed to verify PV for caller %s want %v got %v",
			s.callerTag, verifyPV, downloadedPV)
	}

	return nil
}

// downloadPVs downloads all PVs in the given bucket.
// - Downloads objects with key prefix:  "v1.PersistentVolume/"
// - If bucket doesn't exists, will return ErrCodeNoSuchBucket "NoSuchBucket"
func (s *s3ObjectStore) downloadPVs(bucket string) (
	pvList []corev1.PersistentVolume, err error) {
	result, err := s.downloadTypedObjects(bucket,
		reflect.TypeOf(corev1.PersistentVolume{}))
	if err != nil {
		return nil, fmt.Errorf("unable to download: %s, %w", bucket, err)
	}

	pvList, ok := result.([]corev1.PersistentVolume)
	if !ok {
		return nil, fmt.Errorf("unable to download PV type: got %T", result)
	}

	return pvList, nil
}

// downloadTypedObjects downloads all objects of the given objectType that have
// a key prefix as the given objectType.
// - Example key prefix:  v1.PersistentVolumeClaim/
// - Objects being downloaded should meet the decoding expectations of
// 	 the downloadObject() method.
// - Returns a []objectType
func (s *s3ObjectStore) downloadTypedObjects(bucket string,
	objectType reflect.Type) (interface{}, error) {
	keyPrefix := objectType.String() + "/"

	keys, err := s.listKeys(bucket, keyPrefix)
	if err != nil {
		return nil, fmt.Errorf("unable to listKeys of type %v "+
			"from endpoint %s bucket %s keyPrefix %s, %w",
			objectType, s.s3Endpoint, bucket, keyPrefix, err)
	}

	objects := reflect.MakeSlice(reflect.SliceOf(objectType),
		len(keys), len(keys))

	for i := range keys {
		objectReceiver := objects.Index(i).Addr().Interface()
		if err := s.downloadObject(bucket, keys[i], objectReceiver); err != nil {
			return nil, fmt.Errorf("unable to downloadObject from "+
				"endpoint %s bucket %s key %s, %w",
				s.s3Endpoint, bucket, keys[i], err)
		}
	}

	// Return []objectType
	return objects.Interface(), nil
}

// listKeys lists the keys (of objects) with the given keyPrefix in the given bucket.
// - If bucket doesn't exists, will return ErrCodeNoSuchBucket "NoSuchBucket"
// - Refer to aws documentation of s3.ListObjectsV2Input for more list options
func (s *s3ObjectStore) listKeys(bucket string, keyPrefix string) (
	keys []string, err error) {
	var nextContinuationToken *string

	for gotAllObjects := false; !gotAllObjects; {
		result, err := s.client.ListObjectsV2(&s3.ListObjectsV2Input{
			Bucket:            &bucket,
			Prefix:            &keyPrefix,
			ContinuationToken: nextContinuationToken,
		})
		if err != nil {
			return nil,
				fmt.Errorf("failed to list objects in bucket %s:%s, %w",
					bucket, keyPrefix, err)
		}

		for _, entry := range result.Contents {
			keys = append(keys, *entry.Key)
		}

		if *result.IsTruncated {
			nextContinuationToken = result.NextContinuationToken
		} else {
			gotAllObjects = true
		}
	}

	return
}

// downloadObject downloads an object from the given bucket with the given key,
// unzips, decodes the json blob and stores the downloaded object in the
// downloadContent parameter.  The caller is expected to use the correct type of
// downloadContent parameter.
// - OK to call DownloadObject() concurrently from multiple goroutines safely.
// - Assumes that the object in S3 store are json blobs that have been then
//   gzipped and hence, will unzip & decode the json blobs before returning it.
// - Only those type field name in the downloaded json blob that are also
//   present in the downloadContent type will be filled; other fields will be
// 	 dropped without returning any error.  More info at documentation of
//   json.Unmarshall().
// - Download may fail due to many reasons: RequestError (connection error),
//   NoSuchBucket, NoSuchKey, invalid gzip header, json unmarshall error,
//   InvalidParameter (e.g., empty key), etc.
func (s *s3ObjectStore) downloadObject(bucket string, key string,
	downloadContent interface{}) error {
	writerAt := &aws.WriteAtBuffer{}
	if _, err := s.downloader.Download(writerAt, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}); err != nil {
		return fmt.Errorf("failed to download data of %s:%s, %w",
			bucket, key, err)
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(writerAt.Bytes()))
	if err != nil && !errorswrapper.Is(err, io.EOF) {
		return fmt.Errorf("failed to unzip data of %s:%s, %w",
			bucket, key, err)
	}

	if err := json.NewDecoder(gzReader).Decode(downloadContent); err != nil {
		return fmt.Errorf("failed to decode json decoder of %s:%s, %w",
			bucket, key, err)
	}

	if err := gzReader.Close(); err != nil {
		return fmt.Errorf("failed to close gzip reader of %s:%s, %w",
			bucket, key, err)
	}

	return nil
}

// deleteObject() deletes from the given bucket any objects that have the given
// the keyPrefix.  If the bucket doesn't exists, will return
// ErrCodeNoSuchBucket "NoSuchBucket".
func (s *s3ObjectStore) deleteObject(bucket string, keyPrefix string) (
	err error) {
	keys, err := s.listKeys(bucket, keyPrefix)
	if err != nil {
		return fmt.Errorf("unable to listKeys in deleteObjects "+
			"from endpoint %s bucket %s keyPrefix %s, %w",
			s.s3Endpoint, bucket, keyPrefix, err)
	}

	numObjects := len(keys)
	delObjects := make([]s3manager.BatchDeleteObject, numObjects)

	for i, key := range keys {
		delObjects[i] = s3manager.BatchDeleteObject{
			Object: &s3.DeleteObjectInput{
				Key:    aws.String(key),
				Bucket: aws.String(bucket),
			},
		}
	}

	if err = s.batchDeleter.Delete(aws.BackgroundContext(), &s3manager.DeleteObjectsIterator{
		Objects: delObjects,
	}); err != nil {
		return fmt.Errorf("unable to deleteObjects "+
			"from endpoint %s bucket %s keyPrefix %s, %w",
			s.s3Endpoint, bucket, keyPrefix, err)
	}

	return
}

// constructBucketName returns a bucket name formed using the input namespace
// and name, separating the two with a hypen.
// - The input namespace and name may have dots or hyphens.
// - Bucket names must be between 3 and 63 characters long.
// - Bucket names can consist only of lowercase letters, numbers, dots (.), and hyphens (-).
// - Bucket names must begin and end with a letter or number.
// - Bucket names must not be formatted as an IP address (for example, 192.168.5.4).
// - Bucket names must be unique within a partition. A partition is a grouping of Regions.
//   AWS currently has three partitions: aws (Standard Regions), aws-cn (China Regions),
//   and aws-us-gov (AWS GovCloud [US] Regions).
// - Buckets used with Amazon S3 Transfer Acceleration can't have dots (.) in their names.
// Source: https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
func constructBucketName(namespace, name string) (bucket string) {
	bucket = namespace + "-" + name

	return
}
