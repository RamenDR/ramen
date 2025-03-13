// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
	Recipe "github.com/ramendr/recipe/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
)

var ErrWorkflowNotFound = fmt.Errorf("backup or restore workflow not found")

func kubeObjectsCaptureInterval(kubeObjectProtectionSpec *ramen.KubeObjectProtectionSpec) time.Duration {
	if kubeObjectProtectionSpec.CaptureInterval == nil {
		return ramen.KubeObjectProtectionCaptureIntervalDefault
	}

	return kubeObjectProtectionSpec.CaptureInterval.Duration
}

func kubeObjectsCapturePathNamesAndNamePrefix(
	namespaceName, vrgName string, captureNumber int64, kubeObjects kubeobjects.RequestsManager,
) (string, string, string) {
	const numberBase = 10
	number := strconv.FormatInt(captureNumber, numberBase)
	pathName := s3PathNamePrefix(namespaceName, vrgName) + "kube-objects/" + number + "/"

	return pathName,
		pathName + kubeObjects.ProtectsPath(),
		// TODO fix: may exceed name capacity
		namespaceName + "--" + vrgName + "--" + number
}

func kubeObjectsCaptureName(prefix, groupName, s3ProfileName string) string {
	return prefix + "--" + groupName + "--" + s3ProfileName
}

func kubeObjectsRecoverNamePrefix(vrgNamespaceName, vrgName string) string {
	return vrgNamespaceName + "--" + vrgName
}

func kubeObjectsRecoverName(prefix string, groupNumber int) string {
	return prefix + "--" + strconv.Itoa(groupNumber)
}

func (v *VRGInstance) kubeObjectsProtectPrimary(result *ctrl.Result) {
	v.kubeObjectsProtect(result, kubeObjectsCaptureStartConditionallyPrimary,
		func() {},
	)
}

type (
	captureStartConditionally     func(*VRGInstance, *ctrl.Result, int64, time.Duration, time.Duration, func())
	captureInProgressStatusUpdate func()
)

func (v *VRGInstance) kubeObjectsProtect(
	result *ctrl.Result,
	captureStartConditionally captureStartConditionally,
	captureInProgressStatusUpdate captureInProgressStatusUpdate,
) {
	if v.kubeObjectProtectionDisabled("capture") {
		// set the in-memory condition to nil to indicate that we don't need
		// kube objects protected for this vrg
		v.kubeObjectsCaptureStatusNil()

		return
	}

	// TODO tolerate and remove
	if len(v.s3StoreAccessors) == 0 {
		v.log.Info("Kube objects capture store list empty")

		result.Requeue = true // TODO remove; watch config map instead

		return
	}

	vrg := v.instance
	status := &vrg.Status.KubeObjectProtection

	captureToRecoverFrom := status.CaptureToRecoverFrom
	if captureToRecoverFrom == nil {
		v.log.Info("Kube objects capture-to-recover-from nil")
		v.kubeObjectsCaptureStatusFalse(VRGConditionReasonUploading, "Kube objects initial capture in-progress")

		captureToRecoverFrom = &ramen.KubeObjectsCaptureIdentifier{}
	}

	v.kubeObjectsCaptureStartOrResumeOrDelay(result,
		captureStartConditionally,
		captureInProgressStatusUpdate,
		captureToRecoverFrom,
	)
}

func (v *VRGInstance) kubeObjectsCaptureStartOrResumeOrDelay(
	result *ctrl.Result,
	captureStartConditionally captureStartConditionally,
	captureInProgressStatusUpdate captureInProgressStatusUpdate,
	captureToRecoverFrom *ramen.KubeObjectsCaptureIdentifier,
) {
	veleroNamespaceName := v.veleroNamespaceName()
	vrg := v.instance
	interval := kubeObjectsCaptureInterval(vrg.Spec.KubeObjectProtection)
	number := 1 - captureToRecoverFrom.Number
	log := v.log.WithValues("number", number)
	pathName, capturePathName, namePrefix := kubeObjectsCapturePathNamesAndNamePrefix(
		vrg.Namespace, vrg.Name, number, v.reconciler.kubeObjects)
	labels := util.OwnerLabels(vrg)

	requests, err := v.reconciler.kubeObjects.ProtectRequestsGet(
		v.ctx, v.reconciler.APIReader, veleroNamespaceName, labels)
	if err != nil {
		log.Error(err, "Kube objects capture in-progress query error")
		v.kubeObjectsCaptureFailed("KubeObjectsCaptureRequestsQueryError", err.Error())

		result.Requeue = true

		return
	}

	captureStartOrResume := func(generation int64, startOrResume string) {
		log.Info("Kube objects capture "+startOrResume, "generation", generation)
		v.kubeObjectsCaptureStartOrResume(result,
			captureStartConditionally,
			captureInProgressStatusUpdate,
			number, pathName, capturePathName, namePrefix, veleroNamespaceName, interval,
			generation,
			kubeobjects.RequestsMapKeyedByName(requests),
			log,
		)
	}

	if count := requests.Count(); count > 0 {
		captureStartOrResume(requests.Get(0).Object().GetGeneration(), "resume")

		return
	}

	captureStartConditionally(
		v, result, captureToRecoverFrom.StartGeneration, time.Since(captureToRecoverFrom.StartTime.Time), interval,
		func() {
			if v.kubeObjectsCapturesDelete(result, number, capturePathName) != nil {
				return
			}

			captureStartOrResume(vrg.GetGeneration(), "start")
		},
	)
}

func kubeObjectsCaptureStartConditionallyPrimary(
	v *VRGInstance, result *ctrl.Result,
	captureStartGeneration int64, captureStartTimeSince, captureStartInterval time.Duration,
	captureStart func(),
) {
	if delay := captureStartInterval - captureStartTimeSince; delay > 0 {
		v.log.Info("delaying kube objects capture start as per capture interval", "delay", delay,
			"interval", captureStartInterval)
		calibrateRequeueAfterTime(result, delay, v.log)
		v.kubeObjectsCaptureStatus(metav1.ConditionTrue, VRGConditionReasonUploaded,
			kubeObjectsClusterDataProtectedTrueMessage)

		return
	}

	captureStart()
}

func (v *VRGInstance) kubeObjectsCapturesDelete(
	result *ctrl.Result, captureNumber int64, pathName string,
) error {
	// current s3 profiles may differ from those at capture time
	for _, s3StoreAccessor := range v.s3StoreAccessors {
		if err := s3StoreAccessor.ObjectStorer.DeleteObjectsWithKeyPrefix(pathName); err != nil {
			v.log.Error(err, "Kube objects capture s3 objects delete error",
				"number", captureNumber,
				"profile", s3StoreAccessor.S3ProfileName,
			)
			v.kubeObjectsCaptureFailed("KubeObjectsReplicaDeleteError", err.Error())

			result.Requeue = true

			return err
		}
	}

	return nil
}

const (
	vrgGenerationKey            = "ramendr.openshift.io/vrg-generation"
	vrgGenerationNumberBase     = 10
	vrgGenerationNumberBitCount = 64
)

// nolint: funlen
func (v *VRGInstance) kubeObjectsCaptureStartOrResume(
	result *ctrl.Result,
	captureStartConditionally captureStartConditionally,
	captureInProgressStatusUpdate captureInProgressStatusUpdate,
	captureNumber int64,
	pathName, capturePathName, namePrefix, veleroNamespaceName string,
	interval time.Duration,
	generation int64,
	requests map[string]kubeobjects.Request,
	log logr.Logger,
) {
	captureSteps := v.recipeElements.CaptureWorkflow

	annotations := map[string]string{vrgGenerationKey: strconv.FormatInt(generation, vrgGenerationNumberBase)}
	labels := util.OwnerLabels(v.instance)

	allEssentialStepsFailed, err := v.executeCaptureSteps(result, pathName, capturePathName, namePrefix,
		veleroNamespaceName, captureInProgressStatusUpdate, annotations, requests, log)
	if err != nil {
		result.Requeue = true

		return
	}

	if allEssentialStepsFailed {
		result.Requeue = true

		v.kubeObjectsCaptureFailed("KubeObjectsCaptureError", "Kube objects capture failed")

		return
	}

	firstRequest := getFirstRequest(captureSteps, requests, namePrefix, v.s3StoreAccessors[0].S3ProfileName)
	if firstRequest == nil {
		result.Requeue = true

		v.log.Info("Kube objects capture first request not found")

		return
	}

	v.kubeObjectsCaptureComplete(
		result,
		captureStartConditionally,
		captureNumber,
		veleroNamespaceName,
		interval,
		labels,
		firstRequest.StartTime(),
		firstRequest.Object().GetAnnotations(),
	)
}

//nolint:gocognit,funlen,cyclop
func (v *VRGInstance) executeCaptureSteps(result *ctrl.Result, pathName, capturePathName, namePrefix,
	veleroNamespaceName string, captureInProgressStatusUpdate captureInProgressStatusUpdate,
	annotations map[string]string, requests map[string]kubeobjects.Request, log logr.Logger,
) (bool, error) {
	captureSteps := v.recipeElements.CaptureWorkflow
	failOn := v.recipeElements.CaptureFailOn
	allEssentialStepsFailed := true
	essentialStepsCount := 0
	requestsProcessedCount := 0
	requestsCompletedCount := 0
	labels := util.OwnerLabels(v.instance)

	for groupNumber, captureGroup := range captureSteps {
		var err error

		var loopCount int

		cg := captureGroup
		log1 := log.WithValues("group", groupNumber, "name", cg.Name)
		isEssentialStep := cg.GroupEssential != nil && *cg.GroupEssential

		if cg.IsHook {
			executor, err1 := hooks.GetHookExecutor(cg.Hook)
			if err1 != nil {
				// continue if hook type is not supported. Supported types are "check" and "exec"
				log1.Info("Hook type not supported", "hook", cg.Hook)

				continue
			}

			err = executor.Execute(v.reconciler.Client, log1)
		}

		if !cg.IsHook {
			loopCount, err = v.kubeObjectsGroupCapture(
				result, cg, pathName, capturePathName, namePrefix, veleroNamespaceName,
				captureInProgressStatusUpdate,
				labels, annotations, requests, log,
			)
			requestsCompletedCount += loopCount
		}

		if err != nil {
			if shouldStopExecution(failOn, isEssentialStep) {
				v.kubeObjectsCaptureFailed("KubeObjectsWorkflowError", err.Error())

				return false, err
			}

			allEssentialStepsFailed = allEssentialStepsFailed && isEssentialStep

			continue
		}

		if isEssentialStep {
			// shows that at least one essential step has succeeded
			allEssentialStepsFailed = false
			essentialStepsCount++
		}

		requestsProcessedCount += len(v.s3StoreAccessors)
		if requestsCompletedCount < requestsProcessedCount {
			log1.Info("Kube objects group capturing", "complete", requestsCompletedCount, "total", requestsProcessedCount)

			return allEssentialStepsFailed, fmt.Errorf("kube objects group capturing incomplete")
		}
	}

	if essentialStepsCount == 0 {
		allEssentialStepsFailed = false
	}

	return allEssentialStepsFailed, nil
}

func (v *VRGInstance) kubeObjectsGroupCapture(
	result *ctrl.Result,
	captureGroup kubeobjects.CaptureSpec,
	pathName, capturePathName, namePrefix, veleroNamespaceName string,
	captureInProgressStatusUpdate captureInProgressStatusUpdate,
	labels, annotations map[string]string, requests map[string]kubeobjects.Request,
	log logr.Logger,
) (requestsCompletedCount int, reqErr error) {
	for _, s3StoreAccessor := range v.s3StoreAccessors {
		requestName := kubeObjectsCaptureName(namePrefix, captureGroup.Name, s3StoreAccessor.S3ProfileName)
		log1 := log.WithValues("profile", s3StoreAccessor.S3ProfileName)

		request, ok := requests[requestName]
		if !ok {
			if _, err := v.reconciler.kubeObjects.ProtectRequestCreate(
				v.ctx, v.reconciler.Client, v.log,
				s3StoreAccessor.S3CompatibleEndpoint, s3StoreAccessor.S3Bucket, s3StoreAccessor.S3Region,
				pathName, s3StoreAccessor.VeleroNamespaceSecretKeyRef, s3StoreAccessor.CACertificates,
				captureGroup.Spec, veleroNamespaceName, requestName,
				labels, annotations,
			); err != nil {
				log1.Error(err, "Kube objects group capture request submit error")

				result.Requeue = true
				reqErr = fmt.Errorf("kube objects group capture error: %v", err)

				continue
			}

			captureInProgressStatusUpdate()
			log1.Info("Kube objects group capture request submitted")
		} else {
			err := request.Status(v.log)
			if err == nil {
				log1.Info("Kube objects group captured", "start", request.StartTime(), "end", request.EndTime())

				requestsCompletedCount++

				continue
			}

			if errors.Is(err, kubeobjects.RequestProcessingError{}) {
				log1.Info("Kube objects group capturing", "state", err.Error())

				continue
			}

			log1.Error(err, "Kube objects group capture error")
			v.kubeObjectsCaptureAndCaptureRequestDelete(request, s3StoreAccessor, capturePathName, log1)
			v.kubeObjectsCaptureStatusFalse("KubeObjectsCaptureError", err.Error())

			result.Requeue = true
			reqErr = fmt.Errorf("kube objects group capture error: %v", err)

			continue
		}
	}

	return requestsCompletedCount, reqErr
}

func (v *VRGInstance) kubeObjectsCaptureAndCaptureRequestDelete(
	request kubeobjects.Request, s3StoreAccessor s3StoreAccessor,
	pathName string, log logr.Logger,
) {
	v.kubeObjectsCaptureDeleteAndLog(s3StoreAccessor, pathName, request.Name(), log)

	if err := request.Deallocate(v.ctx, v.reconciler.Client, v.log); err != nil {
		log.Error(err, "Kube objects capture request deallocate error")
	}
}

func (v *VRGInstance) kubeObjectsCaptureDeleteAndLog(
	s3StoreAccessor s3StoreAccessor, pathName, requestName string, log logr.Logger,
) {
	if err := s3StoreAccessor.ObjectStorer.DeleteObjectsWithKeyPrefix(pathName + requestName + "/"); err != nil {
		log.Error(err, "Kube objects capture delete error")
	}
}

func (v *VRGInstance) kubeObjectsCaptureComplete(
	result *ctrl.Result,
	captureStartConditionally captureStartConditionally,
	captureNumber int64, veleroNamespaceName string, interval time.Duration,
	labels map[string]string, startTime metav1.Time, annotations map[string]string,
) {
	vrg := v.instance
	captureToRecoverFromIdentifier := &vrg.Status.KubeObjectProtection.CaptureToRecoverFrom

	startGeneration, err := strconv.ParseInt(
		annotations[vrgGenerationKey], vrgGenerationNumberBase, vrgGenerationNumberBitCount)
	if err != nil {
		v.log.Error(err, "Kube objects capture generation string to int64 conversion error")
	}

	captureToRecoverFromIdentifierCurrent := *captureToRecoverFromIdentifier
	*captureToRecoverFromIdentifier = &ramen.KubeObjectsCaptureIdentifier{
		Number:    captureNumber,
		StartTime: startTime,
		EndTime:   metav1.Now(),
		// Actual EndTime is last request's EndTime but it is okay to use the current time
		StartGeneration: startGeneration,
	}

	v.vrgObjectProtectThrottled(
		result,
		func() {
			v.kubeObjectsCaptureIdentifierUpdateComplete(
				result,
				captureStartConditionally,
				*captureToRecoverFromIdentifier,
				veleroNamespaceName,
				interval,
				labels,
			)
		},
		func() {
			*captureToRecoverFromIdentifier = captureToRecoverFromIdentifierCurrent
		},
	)
}

func (v *VRGInstance) kubeObjectsCaptureIdentifierUpdateComplete(
	result *ctrl.Result,
	captureStartConditionally captureStartConditionally,
	captureToRecoverFromIdentifier *ramen.KubeObjectsCaptureIdentifier,
	veleroNamespaceName string,
	interval time.Duration,
	labels map[string]string,
) {
	if err := v.reconciler.kubeObjects.ProtectRequestsDelete(
		v.ctx, v.reconciler.Client, veleroNamespaceName, labels,
	); err != nil {
		v.log.Error(err, "Kube objects capture requests delete error",
			"number", captureToRecoverFromIdentifier.Number)
		v.kubeObjectsCaptureFailed("KubeObjectsCaptureRequestsDeleteError", err.Error())

		result.Requeue = true

		return
	}

	v.kubeObjectsCaptureStatus(metav1.ConditionTrue, VRGConditionReasonUploaded,
		kubeObjectsClusterDataProtectedTrueMessage)

	captureStartTimeSince := time.Since(captureToRecoverFromIdentifier.StartTime.Time)
	v.log.Info("Kube objects captured", "recovery point", captureToRecoverFromIdentifier,
		"duration", captureStartTimeSince)
	captureStartConditionally(
		v, result, captureToRecoverFromIdentifier.StartGeneration, captureStartTimeSince, interval,
		func() {
			v.log.Info("Kube objects capture schedule to run immediately")
			delaySetMinimum(result)
		},
	)
}

func (v *VRGInstance) kubeObjectsCaptureFailed(reason, message string) {
	v.kubeObjectsCaptureStatusFalse(reason, message)
}

func (v *VRGInstance) kubeObjectsCaptureStatusFalse(reason, message string) {
	v.kubeObjectsCaptureStatus(metav1.ConditionFalse, reason, message)
}

func (v *VRGInstance) kubeObjectsCaptureStatusNil() {
	v.kubeObjectsProtected = nil
}

func (v *VRGInstance) kubeObjectsCaptureStatus(status metav1.ConditionStatus, reason, message string) {
	v.kubeObjectsProtected = &metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Status:             status,
		ObservedGeneration: v.instance.Generation,
		Reason:             reason,
		Message:            message,
	}
}

func (v *VRGInstance) getVRGFromS3Profile(s3ProfileName string) (*ramen.VolumeReplicationGroup, error) {
	pathName := s3PathNamePrefix(v.instance.Namespace, v.instance.Name)

	objectStore, _, err := v.reconciler.ObjStoreGetter.ObjectStore(
		v.ctx, v.reconciler.APIReader, s3ProfileName, v.namespacedName, v.log)
	if err != nil {
		return nil, fmt.Errorf("object store inaccessible for profile %v: %v", s3ProfileName, err)
	}

	vrg := &ramen.VolumeReplicationGroup{}
	if err := vrgObjectDownload(objectStore, pathName, vrg); err != nil {
		return nil, fmt.Errorf("vrg download failed, vrg namespace:%v, vrg name: %v, s3Profile: %v, error: %v",
			v.instance.Namespace, v.instance.Name, s3ProfileName, err)
	}

	return vrg, nil
}

func (v *VRGInstance) skipIfS3ProfileIsForTest() bool {
	for _, s3ProfileName := range v.instance.Spec.S3Profiles {
		if s3ProfileName == NoS3StoreAvailable {
			v.log.Info("NoS3 available to fetch")

			return true
		}
	}

	return false
}

func (v *VRGInstance) kubeObjectsRecoverFromS3(result *ctrl.Result, accessor s3StoreAccessor) error {
	s3ProfileName := accessor.S3ProfileName

	sourceVrg, err := v.getVRGFromS3Profile(s3ProfileName)
	if err != nil {
		return fmt.Errorf("kube objects source VRG get error: %v", err)
	}

	captureToRecoverFromIdentifier := sourceVrg.Status.KubeObjectProtection.CaptureToRecoverFrom
	if captureToRecoverFromIdentifier == nil {
		return fmt.Errorf("kube objects source VRG capture-to-recover-from identifier nil: %v", err)
	}

	v.instance.Status.KubeObjectProtection.CaptureToRecoverFrom = captureToRecoverFromIdentifier
	log := v.log.WithValues("number", captureToRecoverFromIdentifier.Number, "profile", s3ProfileName)

	return v.kubeObjectsRecoveryStartOrResume(result, s3ProfileName, captureToRecoverFromIdentifier, log)
}

func (v *VRGInstance) kubeObjectsRecover(result *ctrl.Result) error {
	if v.kubeObjectProtectionDisabled("recovery") {
		return nil
	}

	if v.instance.Spec.Action == "" {
		v.log.Info("Skipping kube objects restore in fresh deployment case")

		return nil
	}

	if len(v.s3StoreAccessors) == 0 {
		v.log.Info("No S3 profiles configured")

		result.Requeue = true

		return fmt.Errorf("no S3Profiles configured")
	}

	if v.skipIfS3ProfileIsForTest() {
		return nil
	}

	for _, s3StoreAccessor := range v.s3StoreAccessors {
		if err := v.kubeObjectsRecoverFromS3(result, s3StoreAccessor); err != nil {
			v.log.Info("Kube objects restore error", "profile", s3StoreAccessor.S3ProfileName, "error", err)

			continue
		}

		v.log.Info("Kube objects restore complete", "profile", s3StoreAccessor.S3ProfileName)

		return nil
	}

	result.Requeue = true

	return fmt.Errorf("kube objects restore error, will retry")
}

func (v *VRGInstance) findS3StoreAccessor(s3ProfileName string) (s3StoreAccessor, error) {
	for _, s3StoreAccessor := range v.s3StoreAccessors {
		if s3StoreAccessor.S3StoreProfile.S3ProfileName == s3ProfileName {
			return s3StoreAccessor, nil
		}
	}

	return s3StoreAccessor{},
		fmt.Errorf("s3StoreProfile (%s) not found in s3StoreAccessor list", s3ProfileName)
}

func (v *VRGInstance) getRecoverOrProtectRequest(
	captureRequests, recoverRequests map[string]kubeobjects.Request,
	s3StoreAccessor s3StoreAccessor, sourceVrgNamespaceName, sourceVrgName string,
	captureToRecoverFromIdentifier *ramen.KubeObjectsCaptureIdentifier,
	groupNumber int, recoverGroup kubeobjects.RecoverSpec,
	labels map[string]string, log logr.Logger,
) (kubeobjects.Request, bool, func() (kubeobjects.Request, error), func(kubeobjects.Request)) {
	vrg := v.instance
	annotations := map[string]string{}

	if recoverGroup.BackupName == ramen.ReservedBackupName {
		backupSequenceNumber := 1 - captureToRecoverFromIdentifier.Number // is this a good way to do this?
		pathName, capturePathName, namePrefix := kubeObjectsCapturePathNamesAndNamePrefix(
			vrg.Namespace, vrg.Name, backupSequenceNumber, v.reconciler.kubeObjects)
		backupName := fmt.Sprintf("%s-restore-%d", recoverGroup.BackupName, groupNumber)
		captureName := kubeObjectsCaptureName(namePrefix, backupName, s3StoreAccessor.S3ProfileName)
		request, ok := captureRequests[captureName]

		return request, ok, func() (kubeobjects.Request, error) {
				return v.reconciler.kubeObjects.ProtectRequestCreate(
					v.ctx, v.reconciler.Client, v.log,
					s3StoreAccessor.S3CompatibleEndpoint, s3StoreAccessor.S3Bucket, s3StoreAccessor.S3Region, pathName,
					s3StoreAccessor.VeleroNamespaceSecretKeyRef,
					s3StoreAccessor.CACertificates,
					recoverGroup.Spec, v.veleroNamespaceName(),
					captureName,
					labels, annotations)
			},
			func(request kubeobjects.Request) {
				v.kubeObjectsCaptureAndCaptureRequestDelete(request, s3StoreAccessor, capturePathName, log)
			}
	}

	recoverNamePrefix := kubeObjectsRecoverNamePrefix(vrg.Namespace, vrg.Name)
	recoverName := kubeObjectsRecoverName(recoverNamePrefix, groupNumber)
	recoverRequest, ok := recoverRequests[recoverName]

	return recoverRequest, ok, func() (kubeobjects.Request, error) {
			pathName, _, captureNamePrefix := kubeObjectsCapturePathNamesAndNamePrefix(
				sourceVrgNamespaceName, sourceVrgName, captureToRecoverFromIdentifier.Number, v.reconciler.kubeObjects)
			captureName := kubeObjectsCaptureName(captureNamePrefix, recoverGroup.BackupName, s3StoreAccessor.S3ProfileName)
			captureRequest := captureRequests[captureName]

			return v.reconciler.kubeObjects.RecoverRequestCreate(
				v.ctx, v.reconciler.Client, v.log,
				s3StoreAccessor.S3CompatibleEndpoint, s3StoreAccessor.S3Bucket, s3StoreAccessor.S3Region, pathName,
				s3StoreAccessor.VeleroNamespaceSecretKeyRef,
				s3StoreAccessor.CACertificates,
				recoverGroup, v.veleroNamespaceName(),
				captureName, captureRequest,
				recoverName,
				labels, annotations)
		},
		func(request kubeobjects.Request) {
			if err := request.Deallocate(v.ctx, v.reconciler.Client, v.log); err != nil {
				log.Error(err, "Kube objects group recover request deallocate error")
			}
		}
}

func (v *VRGInstance) getCaptureRequests() (map[string]kubeobjects.Request, error) {
	captureRequestsStruct, err := v.reconciler.kubeObjects.ProtectRequestsGet(
		v.ctx, v.reconciler.APIReader, v.veleroNamespaceName(), util.OwnerLabels(v.instance))
	if err != nil {
		return nil, fmt.Errorf("kube objects capture requests query error: %v", err)
	}

	return kubeobjects.RequestsMapKeyedByName(captureRequestsStruct), nil
}

func (v *VRGInstance) getRecoverRequests() (map[string]kubeobjects.Request, error) {
	recoverRequestsStruct, err := v.reconciler.kubeObjects.RecoverRequestsGet(
		v.ctx, v.reconciler.APIReader, v.veleroNamespaceName(), util.OwnerLabels(v.instance))
	if err != nil {
		return nil, fmt.Errorf("kube objects recover requests query error: %v", err)
	}

	return kubeobjects.RequestsMapKeyedByName(recoverRequestsStruct), nil
}

func (v *VRGInstance) kubeObjectsRecoveryStartOrResume(
	result *ctrl.Result, s3ProfileName string,
	captureToRecoverFromIdentifier *ramen.KubeObjectsCaptureIdentifier,
	log logr.Logger,
) error {
	captureRequests, err := v.getCaptureRequests()
	if err != nil {
		return err
	}

	recoverRequests, err := v.getRecoverRequests()
	if err != nil {
		return err
	}

	steps := v.recipeElements.RecoverWorkflow
	requests := make([]kubeobjects.Request, len(steps))
	labels := util.OwnerLabels(v.instance)

	s3StoreAccessor, err := v.findS3StoreAccessor(s3ProfileName)
	if err != nil {
		return fmt.Errorf("kube objects recovery couldn't build s3StoreAccessor: %v", err)
	}

	allEssentialStepsFailed, err := v.executeRecoverSteps(result, s3StoreAccessor, captureToRecoverFromIdentifier,
		captureRequests, recoverRequests, requests, log)
	if err != nil {
		result.Requeue = true

		return fmt.Errorf("kube objects recovery error: %w", err)
	}

	if allEssentialStepsFailed {
		result.Requeue = true

		return fmt.Errorf("workflow execution failed during restore")
	}

	startTime := getRequestsStartTime(requests)
	duration := time.Since(startTime.Time)
	log.Info("Kube objects recovered", "groups", len(steps), "start", startTime, "duration", duration)

	return v.kubeObjectsRecoverRequestsDelete(result, v.veleroNamespaceName(), labels)
}

// nolint: gocognit,cyclop
func (v *VRGInstance) executeRecoverSteps(result *ctrl.Result, s3StoreAccessor s3StoreAccessor,
	captureToRecoverFromIdentifier *ramen.KubeObjectsCaptureIdentifier, captureRequests,
	recoverRequests map[string]kubeobjects.Request, requests []kubeobjects.Request, log logr.Logger,
) (bool, error) {
	failOn := v.recipeElements.RestoreFailOn
	allEssentialStepsFailed := true
	essentialStepsCount := 0
	labels := util.OwnerLabels(v.instance)

	recoverSteps := v.recipeElements.RecoverWorkflow
	for groupNumber, recoverGroup := range recoverSteps {
		var err error

		rg := recoverGroup
		log1 := log.WithValues("group", groupNumber, "name", rg.BackupName)
		isEssentialStep := rg.GroupEssential != nil && *rg.GroupEssential

		if rg.IsHook {
			executor, err1 := hooks.GetHookExecutor(rg.Hook)
			if err1 != nil {
				// continue if hook type is not supported. Supported types are "check" and "exec"
				log1.Info("Hook type not supported", "hook", rg.Hook)

				continue
			}

			err = executor.Execute(v.reconciler.Client, log1)
		}

		if !rg.IsHook {
			err = v.executeRecoverGroup(result, s3StoreAccessor,
				captureToRecoverFromIdentifier, captureRequests,
				recoverRequests, labels, groupNumber, rg,
				requests, log1)
		}

		if err != nil {
			if shouldStopExecution(failOn, isEssentialStep) {
				return false, err
			}

			allEssentialStepsFailed = allEssentialStepsFailed && isEssentialStep

			continue
		}

		if isEssentialStep {
			// shows that at least one essential step has succeeded
			allEssentialStepsFailed = false
			essentialStepsCount++
		}
	}

	if essentialStepsCount == 0 {
		allEssentialStepsFailed = false
	}

	return allEssentialStepsFailed, nil
}

// function considers failOn and essential parameters and returns
// stopExecution: should further execution be stopped
func shouldStopExecution(failOn string, essential bool) bool {
	switch failOn {
	case WorkflowAnyError:
		return true
	case WorkflowEssentialError:
		return essential
	case WorkflowFullError:
		return false
	}

	return false
}

func (v *VRGInstance) executeRecoverGroup(result *ctrl.Result, s3StoreAccessor s3StoreAccessor,
	captureToRecoverFromIdentifier *ramen.KubeObjectsCaptureIdentifier,
	captureRequests, recoverRequests map[string]kubeobjects.Request,
	labels map[string]string, groupNumber int,
	rg kubeobjects.RecoverSpec, requests []kubeobjects.Request, log1 logr.Logger,
) error {
	sourceVrgName := v.instance.Name
	sourceVrgNamespaceName := v.instance.Namespace
	request, ok, submit, cleanup := v.getRecoverOrProtectRequest(
		captureRequests, recoverRequests, s3StoreAccessor,
		sourceVrgNamespaceName, sourceVrgName,
		captureToRecoverFromIdentifier,
		groupNumber, rg, labels, log1,
	)

	var err error

	if !ok {
		_, err = submit()
		if err == nil {
			log1.Info("Kube objects group recover request submitted")

			return errors.New("kube objects group recover request submitted")
		}
	} else {
		err = request.Status(v.log)
		if err == nil {
			log1.Info("Kube objects group recovered", "start", request.StartTime(), "end", request.EndTime())
			requests[groupNumber] = request

			return nil
		}
	}

	if errors.Is(err, kubeobjects.RequestProcessingError{}) {
		log1.Info("Kube objects group recovering", "state", err.Error())

		return err
	}

	log1.Error(err, "Kube objects group recover error")

	if ok {
		cleanup(request)
	}

	result.Requeue = true

	return err
}

func (v *VRGInstance) kubeObjectsRecoverRequestsDelete(
	result *ctrl.Result, veleroNamespaceName string, labels map[string]string,
) error {
	if err := v.reconciler.kubeObjects.RecoverRequestsDelete(
		v.ctx, v.reconciler.Client, veleroNamespaceName, labels,
	); err != nil {
		v.log.Error(err, "Kube objects recover requests delete error")

		result.Requeue = true

		return err
	}

	v.log.Info("Kube objects recover requests deleted")

	return nil
}

func (v *VRGInstance) veleroNamespaceName() string {
	if v.ramenConfig.KubeObjectProtection.VeleroNamespaceName != "" {
		return v.ramenConfig.KubeObjectProtection.VeleroNamespaceName
	}

	return VeleroNamespaceNameDefault
}

func (v *VRGInstance) kubeObjectProtectionDisabled(caller string) bool {
	vrgDisabled := v.instance.Spec.KubeObjectProtection == nil
	cmDisabled := v.ramenConfig.KubeObjectProtection.Disabled
	disabled := vrgDisabled || cmDisabled

	status := "enabled"
	if disabled {
		status = "disabled"
	}

	msg := fmt.Sprintf("Kube object protection configuration is %v for operation %s", status, caller)
	v.log.Info(msg, "is disabled in vrg", vrgDisabled, "is disabled in configMap", cmDisabled)

	return disabled
}

func (v *VRGInstance) kubeObjectsProtectionDelete(result *ctrl.Result) error {
	if v.kubeObjectProtectionDisabled("deletion") {
		return nil
	}

	vrg := v.instance

	return v.kubeObjectsRecoverRequestsDelete(
		result,
		v.veleroNamespaceName(),
		util.OwnerLabels(vrg),
	)
}

func kubeObjectsRequestsWatch(
	b *builder.Builder, scheme *runtime.Scheme, kubeObjects kubeobjects.RequestsManager,
) *builder.Builder {
	watch := func(request kubeobjects.Request) {
		util.OwnsAcrossNamespaces(
			b,
			scheme,
			request.Object(),
			builder.WithPredicates(util.ResourceVersionUpdatePredicate{}),
		)
	}

	watch(kubeObjects.ProtectRequestNew())
	watch(kubeObjects.RecoverRequestNew())

	return b
}

func getCaptureGroups(recipe Recipe.Recipe) ([]kubeobjects.CaptureSpec, string, error) {
	workflow, err := getBackupWorkflow(recipe)
	if err != nil {
		return nil, "", err
	}

	if err := validateWorkflow(workflow); err != nil {
		return nil, "", err
	}

	resources := make([]kubeobjects.CaptureSpec, len(workflow.Sequence))

	for index, resource := range workflow.Sequence {
		for resourceType, resourceName := range resource {
			captureInstance, err := getResourceAndConvertToCaptureGroup(recipe, resourceType, resourceName)
			if err != nil {
				if errors.Is(err, ErrVolumeCaptureNotSupported) {
					// we only use the volumes group for determining the label selector
					// ignore it in the capture sequence
					continue
				}

				return resources, workflow.FailOn, err
			}

			resources[index] = *captureInstance
		}
	}

	return resources, workflow.FailOn, nil
}

func getRecoverGroups(recipe Recipe.Recipe) ([]kubeobjects.RecoverSpec, string, error) {
	workflow, err := getRestoreWorkflow(recipe)
	if err != nil {
		return nil, "", err
	}

	if err := validateWorkflow(workflow); err != nil {
		return nil, "", err
	}

	resources := make([]kubeobjects.RecoverSpec, len(workflow.Sequence))

	for index, resource := range workflow.Sequence {
		// group: map[string]string, e.g. "group": "groupName", or "hook": "hookName"
		for resourceType, resourceName := range resource {
			captureInstance, err := getResourceAndConvertToRecoverGroup(recipe, resourceType, resourceName)
			if err != nil {
				if errors.Is(err, ErrVolumeRecoverNotSupported) {
					// we only use the volumes group for determining the label selector
					// ignore it in the capture sequence
					continue
				}

				return resources, workflow.FailOn, err
			}

			resources[index] = *captureInstance
		}
	}

	return resources, workflow.FailOn, nil
}

var (
	ErrVolumeCaptureNotSupported = errors.New("volume capture not supported")
	ErrVolumeRecoverNotSupported = errors.New("volume recover not supported")
)

func getResourceAndConvertToCaptureGroup(
	recipe Recipe.Recipe, resourceType, name string) (*kubeobjects.CaptureSpec, error,
) {
	// check hooks OR groups
	if resourceType == "group" {
		for _, group := range recipe.Spec.Groups {
			if group.Name == name {
				return convertRecipeGroupToCaptureSpec(*group)
			}
		}

		if name == recipe.Spec.Volumes.Name {
			return nil, ErrVolumeCaptureNotSupported
		}

		return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Group.Name"}, name)
	}

	if resourceType == "hook" {
		prefix, suffix, err := validateAndGetHookDetails(name)
		if err != nil {
			return nil, k8serrors.NewInternalError(err)
		}

		hook, err := getHookFromRecipe(&recipe, prefix)
		if err != nil {
			return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
		}

		return convertRecipeHookToCaptureSpec(*hook, suffix)
	}

	return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
}

// resource: could be Group or Hook
func getResourceAndConvertToRecoverGroup(
	recipe Recipe.Recipe, resourceType, name string) (*kubeobjects.RecoverSpec, error,
) {
	if resourceType == "group" {
		for _, group := range recipe.Spec.Groups {
			if group.Name == name {
				return convertRecipeGroupToRecoverSpec(*group)
			}
		}

		if name == recipe.Spec.Volumes.Name {
			return nil, ErrVolumeRecoverNotSupported
		}

		return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Group.Name"}, name)
	}

	if resourceType == "hook" {
		prefix, suffix, err := validateAndGetHookDetails(name)
		if err != nil {
			return nil, k8serrors.NewInternalError(err)
		}

		hook, err := getHookFromRecipe(&recipe, prefix)
		if err != nil {
			return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
		}

		return convertRecipeHookToRecoverSpec(*hook, suffix)
	}

	return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec"}, resourceType)
}

func validateAndGetHookDetails(name string) (string, string, error) {
	if strings.Count(name, "/") != 1 {
		return "", "", errors.New("invalid format: hook name provided should be of the form part1/part2")
	}

	parts := strings.Split(name, "/")

	return parts[0], parts[1], nil
}

// from the recipe, get the hook based on the prefix before "/"
func getHookFromRecipe(recipe *Recipe.Recipe, prefix string) (*Recipe.Hook, error) {
	for _, hook := range recipe.Spec.Hooks {
		if hook.Name == prefix {
			return hook, nil
		}
	}

	return nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "Recipe.Spec.Hook.Name"}, prefix)
}

// TODO: complete functionality - add Hook support to KubeResourcesSpec, then copy in Velero object creation
func convertRecipeHookToCaptureSpec(
	hook Recipe.Hook, suffix string) (*kubeobjects.CaptureSpec, error,
) {
	hookSpec := getHookSpecFromHook(hook, suffix)
	hookName := hook.Name + "-" + suffix

	captureSpec := kubeobjects.CaptureSpec{
		Name: hookName,
		// TODO: Check why pod is hardcoded in the included resources?
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedNamespaces: []string{hook.Namespace},
				IncludedResources:  []string{"pod"},
				ExcludedResources:  []string{},
				Hook:               hookSpec,
				IsHook:             true,
			},
			LabelSelector:           hookSpec.LabelSelector,
			IncludeClusterResources: new(bool),
		},
	}

	return &captureSpec, nil
}

func convertRecipeHookToRecoverSpec(hook Recipe.Hook, suffix string) (*kubeobjects.RecoverSpec, error) {
	hookSpec := getHookSpecFromHook(hook, suffix)

	// A RecoverSpec with KubeResourcesSpec.IsHook set to true is never sent to
	// Velero. It will only be used by Ramen to execute the hook.
	// We don't need a backup name for it.
	return &kubeobjects.RecoverSpec{
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedNamespaces: []string{hook.Namespace},
				IncludedResources:  []string{"pod"},
				ExcludedResources:  []string{},
				Hook:               hookSpec,
				IsHook:             true,
			},
			LabelSelector:           hookSpec.LabelSelector,
			IncludeClusterResources: new(bool),
		},
	}, nil
}

// TODO: Return error as well or ensure that other than exec and check hooks are
// handled properly.
func getHookSpecFromHook(hook Recipe.Hook, suffix string) kubeobjects.HookSpec {
	// based on hook.type check of the hook is chks or ops
	if hook.Type == "exec" {
		return getOpHookSpec(&hook, suffix)
	} else if hook.Type == "check" {
		return getChkHookSpec(&hook, suffix)
	}

	return kubeobjects.HookSpec{}
}

func getChkHookSpec(hook *Recipe.Hook, suffix string) kubeobjects.HookSpec {
	for _, chk := range hook.Chks {
		if chk.Name == suffix {
			return kubeobjects.HookSpec{
				Name:           hook.Name,
				Namespace:      hook.Namespace,
				Type:           hook.Type,
				SelectResource: hook.SelectResource,
				LabelSelector:  hook.LabelSelector,
				NameSelector:   hook.NameSelector,
				Timeout:        chk.Timeout,
				OnError:        chk.OnError,
				Chk: kubeobjects.Check{
					Name:      suffix,
					Condition: chk.Condition,
				},
				Essential: hook.Essential,
			}
		}
	}

	return kubeobjects.HookSpec{}
}

func getOpHookSpec(hook *Recipe.Hook, suffix string) kubeobjects.HookSpec {
	for _, op := range hook.Ops {
		if op.Name == suffix {
			return kubeobjects.HookSpec{
				Name:           hook.Name,
				Namespace:      hook.Namespace,
				Type:           hook.Type,
				Timeout:        hook.Timeout,
				OnError:        hook.OnError,
				SelectResource: hook.SelectResource,
				LabelSelector:  hook.LabelSelector,
				NameSelector:   hook.NameSelector,
				SinglePodOnly:  hook.SinglePodOnly,
				Op: kubeobjects.Operation{
					Name:      suffix,
					Container: op.Container,
					Command:   op.Command,
					InverseOp: op.InverseOp,
				},
			}
		}
	}

	return kubeobjects.HookSpec{}
}

func convertRecipeGroupToRecoverSpec(group Recipe.Group) (*kubeobjects.RecoverSpec, error) {
	backupName := group.Name
	if group.BackupRef != "" {
		backupName = group.BackupRef
	}

	return &kubeobjects.RecoverSpec{
		BackupName: backupName,
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedNamespaces: group.IncludedNamespaces,
				IncludedResources:  group.IncludedResourceTypes,
				ExcludedResources:  group.ExcludedResourceTypes,
			},
			LabelSelector:           group.LabelSelector,
			OrLabelSelectors:        []*metav1.LabelSelector{},
			IncludeClusterResources: group.IncludeClusterResources,
		},
	}, nil
}

func convertRecipeGroupToCaptureSpec(group Recipe.Group) (*kubeobjects.CaptureSpec, error) {
	captureSpec := kubeobjects.CaptureSpec{
		Name: group.Name,
		// TODO: add backupRef/backupName here?
		Spec: kubeobjects.Spec{
			KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
				IncludedNamespaces: group.IncludedNamespaces,
				IncludedResources:  group.IncludedResourceTypes,
				ExcludedResources:  group.ExcludedResourceTypes,
				GroupEssential:     group.Essential,
			},
			LabelSelector:           group.LabelSelector,
			OrLabelSelectors:        []*metav1.LabelSelector{},
			IncludeClusterResources: group.IncludeClusterResources,
		},
	}

	return &captureSpec, nil
}

func getFirstRequest(groups []kubeobjects.CaptureSpec, requests map[string]kubeobjects.Request,
	namePrefix string, s3ProfileName string,
) kubeobjects.Request {
	for _, group := range groups {
		cg := group

		if cg.IsHook {
			continue
		}

		// else it is a resource group
		return requests[kubeObjectsCaptureName(namePrefix, cg.Name, s3ProfileName)]
	}

	return nil
}

func getBackupWorkflow(recipe Recipe.Recipe) (*Recipe.Workflow, error) {
	for _, w := range recipe.Spec.Workflows {
		if w.Name == Recipe.BackupWorkflowName {
			return w, nil
		}
	}

	return nil, ErrWorkflowNotFound
}

func getRestoreWorkflow(recipe Recipe.Recipe) (*Recipe.Workflow, error) {
	for _, w := range recipe.Spec.Workflows {
		if w.Name == Recipe.RestoreWorkflowName {
			return w, nil
		}
	}

	return nil, ErrWorkflowNotFound
}

func validateWorkflow(workflow *Recipe.Workflow) error {
	if len(workflow.Sequence) == 0 {
		return nil
	}

	var workflowHasResourceTypeGroup bool

	for _, resource := range workflow.Sequence {
		for resourceType := range resource {
			if resourceType == "group" {
				workflowHasResourceTypeGroup = true
			}
		}
	}

	if !workflowHasResourceTypeGroup {
		return fmt.Errorf("a workflow must contain at least one group")
	}

	return nil
}

func getRequestsStartTime(requests []kubeobjects.Request) metav1.Time {
	for _, request := range requests {
		if request != nil {
			return request.StartTime()
		}
	}

	return metav1.Time{}
}
