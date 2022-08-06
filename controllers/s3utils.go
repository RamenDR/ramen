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
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// We have seen that the valid errors from the s3 servers take up to 2 minutes.
// Let the timeout be greater than that.
var s3Timeout = time.Second * 125

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
// if err := s3Conn.CreateBucket(bucket); err != nil {
// 	return err
// }

// *** Upload objects, optionally using a key prefix to easily find the objects later ***
// for i := 1; i < 10; i++ {
// 	pvKey := fmt.Sprintf("PersistentVolumes/pv%v", i)
// 	uploadPV := corev1.PersistentVolume{}
// 	uploadPV.Name = pvKey
// 	uploadPV.Spec.StorageClassName = "gold"
// 	uploadPV.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
// 	if err := s3Conn.UploadObject(bucket, pvKey, uploadPV); err != nil {
// 		return err
// 	}
// }

// *** Find objects in the bucket, optionally supplying a key prefix
// keyPrefix := "v1.PersistentVolumes/"
// if list, err := s3Conn.ListKeys(bucket, keyPrefix); err != nil {
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
	// ObjectStore returns an object that satisfies ObjectStorer interface
	ObjectStore(ctx context.Context, r client.Reader,
		s3Profile string, callerTag string, log logr.Logger,
	) (ObjectStorer, ramen.S3StoreProfile, error)
}

type ObjectStorer interface {
	UploadObject(key string, object interface{}) error
	DownloadObject(key string, objectPointer interface{}) error
	ListKeys(keyPrefix string) (keys []string, err error)
	DeleteObjects(keyPrefix string) error
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

// ObjectStore returns an S3 object store that satisfies the ObjectStorer
// interface,  with a downloader and an uploader client connections, by either
// creating a new connection or returning a previously established connection
// for the given s3 profile.  Returns an error if s3 profile does not exists,
// secret is not configured, or if client session creation fails.
func (s3ObjectStoreGetter) ObjectStore(ctx context.Context,
	r client.Reader, s3ProfileName string,
	callerTag string, log logr.Logger,
) (ObjectStorer, ramen.S3StoreProfile, error) {
	s3StoreProfile, err := GetRamenConfigS3StoreProfile(ctx, r, s3ProfileName)
	if err != nil {
		return nil, s3StoreProfile, fmt.Errorf("failed to get profile %s for caller %s, %w",
			s3ProfileName, callerTag, err)
	}

	accessID, secretAccessKey, err := GetS3Secret(ctx, r, s3StoreProfile.S3SecretRef)
	if err != nil {
		return nil, s3StoreProfile, fmt.Errorf("failed to get secret %v for caller %s, %w",
			s3StoreProfile.S3SecretRef, callerTag, err)
	}

	s3Endpoint := s3StoreProfile.S3CompatibleEndpoint
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
		return nil, s3StoreProfile, fmt.Errorf("failed to create new session for %s for caller %s, %w",
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
		s3Bucket:     s3StoreProfile.S3Bucket,
		callerTag:    callerTag,
		name:         s3ProfileName,
	}

	return s3Conn, s3StoreProfile, nil
}

func GetS3Secret(ctx context.Context, r client.Reader,
	secretRef corev1.SecretReference) (
	s3AccessID, s3SecretAccessKey []byte, err error) {
	secret := corev1.Secret{}
	namepacedName := types.NamespacedName{Namespace: "", Name: secretRef.Name}

	if secretRef.Namespace == "" {
		namepacedName.Namespace = NamespaceName()
	} else {
		namepacedName.Namespace = secretRef.Namespace
	}

	if err := r.Get(ctx, namepacedName, &secret); err != nil {
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
	s3Bucket     string
	callerTag    string
	name         string
}

// CreateBucket creates the given bucket; does not return an error if the bucket
// exists already.
func (s *s3ObjectStore) CreateBucket(bucket string) (err error) {
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

// DeleteBucket deletes the S3 bucket.  Fails to delete if the bucket contains
// any objects.
func (s *s3ObjectStore) DeleteBucket(bucket string) (
	err error) {
	if bucket == "" {
		return fmt.Errorf("empty bucket name for "+
			"endpoint %s caller %s", s.s3Endpoint, s.callerTag)
	}

	defer func() {
		if r := recover(); r != nil {
			// change the named return err value
			err = fmt.Errorf("delete bucket recovered for %s, with %v",
				bucket, r)
		}
	}()

	dbInput := &s3.DeleteBucketInput{Bucket: &bucket}
	if err = dbInput.Validate(); err != nil {
		return fmt.Errorf("delete bucket input validation failed for %s, err %w",
			bucket, err)
	}

	_, err = s.client.DeleteBucket(dbInput)
	if err != nil && !isAwsErrCodeNoSuchBucket(err) {
		return fmt.Errorf("failed to delete bucket %s, %w",
			bucket, err)
	}

	return nil
}

// PurgeBucket empties the content of the given bucket.
func (s *s3ObjectStore) PurgeBucket(bucket string) (
	err error) {
	if bucket == "" {
		return fmt.Errorf("empty bucket name for "+
			"endpoint %s caller %s", s.s3Endpoint, s.callerTag)
	}

	defer func() {
		if r := recover(); r != nil {
			// change the named return err value
			err = fmt.Errorf("purge bucket recovered for %s, with %v",
				bucket, r)
		}
	}()

	keys, err := s.ListKeys("")
	if err != nil {
		if isAwsErrCodeNoSuchBucket(err) {
			return nil // Not an error
		}

		return fmt.Errorf("unable to ListKeys "+
			"from endpoint %s bucket %s, %w",
			s.s3Endpoint, bucket, err)
	}

	for _, key := range keys {
		err = s.DeleteObjects(key)
		if err != nil {
			return fmt.Errorf("failed to delete object %s in bucket %s, %w",
				key, bucket, err)
		}
	}

	err = s.DeleteBucket(bucket)
	if err != nil {
		return fmt.Errorf("failed to delete bucket %s, %w",
			bucket, err)
	}

	return nil
}

func S3KeyPrefix(namespacedName string) string {
	return namespacedName + "/"
}

func typedKey(prefix, suffix string, typ reflect.Type) string {
	return prefix + typ.String() + "/" + suffix
}

// UploadPV uploads the given PV to the bucket with a key of
// "<pvKeyPrefix><v1.PersistentVolume/><pvKeySuffix>".
// - pvKeyPrefix should have any required delimiters like '/'
// - OK to call UploadPV() concurrently from multiple goroutines safely.
func UploadPV(s ObjectStorer, pvKeyPrefix, pvKeySuffix string,
	pv corev1.PersistentVolume) error {
	return uploadTypedObject(s, pvKeyPrefix, pvKeySuffix, pv)
}

// uploadTypedObject uploads to the bucket the given uploadContent with a
// key of <keyPrefix><objectType/>keySuffix>, where objectType is the type of the
// uploadContent parameter. OK to call uploadTypedObject() concurrently from
// multiple goroutines safely.
// - keyPrefix should have any required delimiters like '/'
func uploadTypedObject(s ObjectStorer, keyPrefix, keySuffix string,
	uploadContent interface{}) error {
	key := typedKey(keyPrefix, keySuffix, reflect.TypeOf(uploadContent))

	return s.UploadObject(key, uploadContent)
}

func downloadTypedObject(s ObjectStorer, keyPrefix, keySuffix string, objectPointer interface{},
) error {
	return s.DownloadObject(typedKey(keyPrefix, keySuffix, reflect.TypeOf(objectPointer).Elem()), objectPointer)
}

func DeleteTypedObjects(s ObjectStorer, keyPrefix, keySuffix string, object interface{},
) error {
	return s.DeleteObjects(typedKey(keyPrefix, keySuffix, reflect.TypeOf(object)))
}

// UploadObject uploads the given object to the bucket with the given key.
// - OK to call UploadObject() concurrently from multiple goroutines safely.
// - Upload may fail due to many reasons: RequestError (connection error),
//   NoSuchBucket, NoSuchKey, InvalidParameter (e.g., empty key), etc.
// - Multiple consecutive forward slashes in the key are sqaushed to
//   a single forward slash, for each such occurrence
// - Any formatting changes to this method should also be reflected in the
//   DownloadObject() method
func (s *s3ObjectStore) UploadObject(key string,
	uploadContent interface{}) error {
	encodedUploadContent := &bytes.Buffer{}
	bucket := s.s3Bucket

	gzWriter := gzip.NewWriter(encodedUploadContent)
	if err := json.NewEncoder(gzWriter).Encode(uploadContent); err != nil {
		return fmt.Errorf("failed to json encode %s:%s, %w",
			bucket, key, err)
	}

	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer of %s:%s, %w",
			bucket, key, err)
	}

	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(s3Timeout))
	defer cancel()

	if _, err := s.uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   encodedUploadContent,
	}); err != nil {
		return fmt.Errorf("failed to upload data of %s:%s, %w",
			bucket, key, err)
	}

	return nil
}

// VerifyPVUpload verifies that the PV in the input matches the PV object
// with the given keySuffix in the bucket.
func VerifyPVUpload(s ObjectStorer, pvKeyPrefix, pvKeySuffix string,
	verifyPV corev1.PersistentVolume) error {
	var downloadedPV corev1.PersistentVolume

	if err := downloadTypedObject(s, pvKeyPrefix, pvKeySuffix, &downloadedPV); err != nil {
		return errorswrapper.WithMessage(err, "VerifyPVUpload")
	}

	if !reflect.DeepEqual(verifyPV, downloadedPV) {
		return fmt.Errorf("failed to verify PV want %v got %v",
			verifyPV, downloadedPV)
	}

	return nil
}

// downloadPVs downloads all PVs in the bucket.
// - Downloads PVs with the given key prefix.
// - If bucket doesn't exists, will return ErrCodeNoSuchBucket "NoSuchBucket"
func downloadPVs(s ObjectStorer, pvKeyPrefix string) (
	pvList []corev1.PersistentVolume, err error) {
	err = DownloadTypedObjects(s, pvKeyPrefix, &pvList)

	return
}

func DownloadVRGs(s ObjectStorer, pvKeyPrefix string) (
	vrgList []ramen.VolumeReplicationGroup, err error) {
	err = DownloadTypedObjects(s, pvKeyPrefix, &vrgList)

	return
}

// DownloadTypedObjects downloads all objects of the given type that have
// the given key prefix followed by the given object's type keyInfix.
// - Example key prefix:  namespace/vrgName/
//   Example key infix:  v1.PersistentVolumeClaim/
//   Example new key prefix: namespace/vrgName/v1.PersistentVolumeClaim/
// - Objects being downloaded should meet the decoding expectations of
//   the DownloadObject() method.
func DownloadTypedObjects(s ObjectStorer, keyPrefix string, objectsPointer interface{},
) error {
	objectsValue := reflect.ValueOf(objectsPointer).Elem()
	objectType := objectsValue.Type().Elem()
	newKeyPrefix := typedKey(keyPrefix, "", objectType)

	keys, err := s.ListKeys(newKeyPrefix)
	if err != nil {
		return fmt.Errorf("unable to ListKeys of type %v keyPrefix %s, %w",
			objectType, newKeyPrefix, err)
	}

	objects := reflect.MakeSlice(reflect.SliceOf(objectType),
		len(keys), len(keys))

	for i := range keys {
		objectReceiver := objects.Index(i).Addr().Interface()
		if err := s.DownloadObject(keys[i], objectReceiver); err != nil {
			return fmt.Errorf("unable to DownloadObject of key %s, %w",
				keys[i], err)
		}
	}

	objectsValue.Set(objects)

	return nil
}

// ListKeys lists the keys (of objects) with the given keyPrefix in the bucket.
// - If bucket doesn't exists, will return ErrCodeNoSuchBucket "NoSuchBucket"
// - Refer to aws documentation of s3.ListObjectsV2Input for more list options
func (s *s3ObjectStore) ListKeys(keyPrefix string) (
	keys []string, err error) {
	var nextContinuationToken *string

	bucket := s.s3Bucket

	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(s3Timeout))
	defer cancel()

	for gotAllObjects := false; !gotAllObjects; {
		result, err := s.client.ListObjectsV2WithContext(ctx, &s3.ListObjectsV2Input{
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

	return keys, nil
}

// DownloadObject downloads an object from the bucket with the given key,
// unzips, decodes the json blob and stores the downloaded object in the
// downloadContent parameter.  The caller is expected to use the correct type of
// downloadContent parameter.
// - OK to call DownloadObject() concurrently from multiple goroutines safely.
// - Assumes that the object in S3 store are json blobs that have been then
//   gzipped and hence, will unzip & decode the json blobs before returning it.
// - Only those type field name in the downloaded json blob that are also
//   present in the downloadContent type will be filled; other fields will be
//   dropped without returning any error.  More info at documentation of
//   json.Unmarshall().
// - Download may fail due to many reasons: RequestError (connection error),
//   NoSuchBucket, NoSuchKey, invalid gzip header, json unmarshall error,
//   InvalidParameter (e.g., empty key), etc.
func (s *s3ObjectStore) DownloadObject(key string,
	downloadContent interface{}) error {
	bucket := s.s3Bucket
	writerAt := &aws.WriteAtBuffer{}

	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(s3Timeout))
	defer cancel()

	if _, err := s.downloader.DownloadWithContext(ctx, writerAt, &s3.GetObjectInput{
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

// DeleteObjects() deletes from the bucket any objects that have the given
// the keyPrefix.  If the bucket doesn't exists, will return
// ErrCodeNoSuchBucket "NoSuchBucket".
func (s *s3ObjectStore) DeleteObjects(keyPrefix string) (
	err error) {
	bucket := s.s3Bucket

	keys, err := s.ListKeys(keyPrefix)
	if err != nil {
		return fmt.Errorf("unable to ListKeys in DeleteObjects "+
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

	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(s3Timeout))
	defer cancel()

	if err = s.batchDeleter.Delete(ctx, &s3manager.DeleteObjectsIterator{
		Objects: delObjects,
	}); err != nil {
		return fmt.Errorf("unable to DeleteObjects "+
			"from endpoint %s bucket %s keyPrefix %s, %w",
			s.s3Endpoint, bucket, keyPrefix, err)
	}

	return nil
}

// isAwsErrCodeNoSuchBucket returns true if the given input `err` has wrapped
// the awserr.ErrCodeNoSuchBucket anywhere in its chain of errors.
func isAwsErrCodeNoSuchBucket(err error) bool {
	var aerr awserr.Error
	if errorswrapper.As(err, &aerr) {
		if aerr.Code() == s3.ErrCodeNoSuchBucket {
			return true
		}
	}

	return false
}
