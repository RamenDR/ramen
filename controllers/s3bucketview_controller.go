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

package controllers

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-logr/logr"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// S3BucketViewReconciler reconciles a S3BucketView object
type S3BucketViewReconciler struct {
	client.Client
	APIReader      client.Reader
	ObjStoreGetter ObjectStoreGetter
	Scheme         *runtime.Scheme
}

type S3BucketViewInstance struct {
	reconciler *S3BucketViewReconciler
	ctx        context.Context
	log        logr.Logger
	instance   *ramendrv1alpha1.S3BucketView
}

//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=s3bucketviews,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=s3bucketviews/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ramendr.openshift.io,resources=s3bucketviews/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the S3BucketView object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *S3BucketViewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("controllers").WithName("s3bucketview").WithValues("name", req.NamespacedName.Name)

	log.Info("s3bucketview reconcile start")

	// save all the commonly used parameters in a struct
	s := S3BucketViewInstance{
		reconciler: r,
		ctx:        ctx,
		log:        log,
		instance:   &ramendrv1alpha1.S3BucketView{},
	}

	// get S3BucketViewInstance and save to s.instance
	if err := r.Client.Get(ctx, req.NamespacedName, s.instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf("get: %w", err))
	}

	if s.instance.Status != nil {
		return ctrl.Result{}, nil
	}

	// get target profile from spec
	s3profileName := s.instance.Spec.ProfileName
	log.Info(fmt.Sprintf("targetProfileName=%s", s3profileName))

	objectStore, err := s.getObjectStore(s3profileName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during getObjectStore: %w", err)
	}

	// get namespace+VRG prefixes as list from S3. Format: unique namespaceName/vrgName pairs
	prefixNamespaceVRG, err := s.getNamespacesAndVrgPrefixesFromS3(s3profileName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during getNamespacesAndVrgPrefixesFromS3: %w", err)
	}

	// get VRG contents from S3
	vrgs, err := s.getVrgContentsFromS3(prefixNamespaceVRG, objectStore)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during getVrgContentsFromS3: %w", err)
	}

	// store results in Status field
	err = s.updateStatus(vrgs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during updateStatus: %w", err)
	}

	log.Info("s3bucketView updated successfully")

	return ctrl.Result{}, nil
}

func (s *S3BucketViewInstance) getObjectStore(s3ProfileName string) (ObjectStorer, error) {
	return s.reconciler.ObjStoreGetter.ObjectStore(s.ctx, s.reconciler.APIReader, s3ProfileName, NamespaceName(), s.log)
}

func (s *S3BucketViewInstance) getNamespacesAndVrgPrefixesFromS3(s3profileName string) ([]string, error) {
	const GetAllContents = ""

	prefixNamespaceVRG, err := s.ParseResultListFromS3Bucket(s3profileName, GetAllContents, ParseDoubleSlash)
	if err != nil {
		return prefixNamespaceVRG, fmt.Errorf("error during ParseResultListFromS3Bucket: %w", err)
	}

	for index, val := range prefixNamespaceVRG {
		s.log.Info(fmt.Sprintf("prefixNamespaceVRG[%d]=%s", index, val))
	}

	return prefixNamespaceVRG, nil
}

func (s *S3BucketViewInstance) getVrgContentsFromS3(prefixNamespaceVRG []string,
	objectStore ObjectStorer) ([]ramendrv1alpha1.VolumeReplicationGroup, error) {
	vrgsAll := make([]ramendrv1alpha1.VolumeReplicationGroup, 0)

	const NoPrefixToRemove = ""
	namespaceNamesList := getUniqueStringsFromList(prefixNamespaceVRG, ParseSingleSlash, NoPrefixToRemove)

	s.log.Info("namespaceNames:")

	for _, namespace := range namespaceNamesList {
		namespaceAndVRG := getUniqueStringsFromList(prefixNamespaceVRG, ParseRemoveSlashes, namespace)
		for _, vrgName := range namespaceAndVRG {
			// download VRGs
			prefixInS3 := fmt.Sprintf("%s/%s/", namespace, vrgName)

			vrgs, err := DownloadVRGs(objectStore, prefixInS3)
			if err != nil {
				return vrgsAll, fmt.Errorf("error during DownloadVRGs on '%s': %w", prefixInS3, err)
			}

			// add all VRGs found to list
			for i := range vrgs {
				vrg := &vrgs[i]
				s.log.Info(fmt.Sprintf("downloaded VRG with name '%s' in namespace '%s'", vrg.Name, vrg.Namespace))
				VrgTidyForList(vrg)

				vrgsAll = append(vrgsAll, *vrg)
			}
		}
	}

	return vrgsAll, nil
}

func VrgTidyForList(vrg *ramendrv1alpha1.VolumeReplicationGroup) {
	vrg.ObjectMeta = ObjectMetaEmbedded(&vrg.ObjectMeta)
}

func (s *S3BucketViewInstance) updateStatus(vrgs []ramendrv1alpha1.VolumeReplicationGroup) error {
	// store all data in Status
	s.instance.Status = &ramendrv1alpha1.S3BucketViewStatus{
		SampleTime:              metav1.Now(),
		VolumeReplicationGroups: vrgs,
	}

	// final Status update to object
	return s.reconciler.Status().Update(s.ctx, s.instance)
}

func (s *S3BucketViewInstance) ParseResultListFromS3Bucket(s3ProfileName string, prefix string,
	parseFunc func(string) string) ([]string, error) {
	itemList, err := s.GetItemsInS3BucketFromPrefix(s3ProfileName, prefix)
	if err != nil {
		return itemList, fmt.Errorf("error during GetItemsInS3BucketFromPrefix, err %w", err)
	}

	results := getUniqueStringsFromList(itemList, parseFunc, prefix)

	return results, nil
}

// look through each value in item list after parsing it, then store unique values
func getUniqueStringsFromList(itemList []string, parseFunc func(string) string, prefixToRemove string) []string {
	uniques := make(map[string]int) // map lacks "get keys" functionality
	results := make([]string, 0)    // store results in a list to avoid loop through uniques

	for _, val := range itemList {
		prefixRemoved := val // declaration here to pass linter (instead of if/else)

		if prefixToRemove != "" {
			prefixRemoved = strings.Replace(val, prefixToRemove, "", 1)

			if prefixRemoved == val { // passed invalid input; skip this
				continue
			}
		}

		parsed := parseFunc(prefixRemoved)

		_, exists := uniques[parsed]

		if !exists {
			uniques[parsed] = 0 // placeholder value only

			results = append(results, parsed)
		}
	}

	return results
}

func ParseSingleSlash(input string) string {
	split := strings.Split(input, "/")

	return split[0]
}

func ParseDoubleSlash(input string) string {
	const RequiredFields = 2

	split := strings.Split(input, "/")

	result := split[0]

	if len(split) >= RequiredFields {
		result = fmt.Sprintf("%s/%s", split[0], split[1])
	}

	// TODO: check if additional handling for invalid strings is required; e.g. return (string, ok)

	return result
}

func ParseRemoveSlashes(input string) string {
	return strings.ReplaceAll(input, "/", "")
}

func (s *S3BucketViewInstance) GetItemsInS3BucketFromPrefix(s3ProfileName string,
	lookupPrefix string) ([]string, error) {
	results := make([]string, 0)

	objectStore, err := s.reconciler.ObjStoreGetter.ObjectStore(s.ctx, s.reconciler.APIReader,
		s3ProfileName, lookupPrefix, s.log)
	if err != nil {
		return results, fmt.Errorf("error when getting object store, err %w", err)
	}

	// empty string will get all contents; may create performance issue with large S3 contents
	results, err = objectStore.ListKeys(lookupPrefix)
	if err != nil {
		return results, fmt.Errorf("%s: %w", s3ProfileName, err)
	}

	return results, nil
}

func GetMatchingS3ProfileFromRamenConfig(ramenConfig *ramendrv1alpha1.RamenConfig,
	targetS3ProfileName string) (ramendrv1alpha1.S3StoreProfile, error) {
	// look up profile information in S3 profiles list
	for _, s3Profile := range ramenConfig.S3StoreProfiles {
		if s3Profile.S3ProfileName == targetS3ProfileName {
			return s3Profile, nil
		}
	}

	return ramendrv1alpha1.S3StoreProfile{},
		errors.NewNotFound(schema.GroupResource{}, "Couldn't find target S3 profile in list of S3 profiles")
}

// SetupWithManager sets up the controller with the Manager.
func (r *S3BucketViewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.S3BucketView{}).
		Complete(r)
}
