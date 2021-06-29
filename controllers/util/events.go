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

package util

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// This implementation of events infrastructure is mainly based on
// the way events is handled in the ocs-operator.
// https://github.com/openshift/ocs-operator/blob/master/controllers/util/events.go
const (
	LastEventValidDuration = 10
)

const (
	// EventReasonValidationFailed is used when VolumeReplicationGroup validation fails
	EventReasonValidationFailed = "FailedValidation"

	// EventReasonPVCListFailed is used when VRG fails to get the list of PVCs
	EventReasonPVCListFailed = "PVCListFailed"

	// EventReasonVRCreateFailed is used when VRG fails to create VolRep resource
	EventReasonVRCreateFailed = "VRCreateFailed"

	// EventReasonVRCreateFailed is used when VRG fails to update VolRep resource
	EventReasonVRUpdateFailed = "VRUpdateFailed"

	// EventReasonProtectPVCFailed is used when VRG fails to protect PVC
	EventReasonProtectPVCFailed = "ProtectPVCFailed"

	// EventReasonPVUploadFailed is used when VRG fails to upload PV metadata
	EventReasonPVUploadFailed = "PVUploadFailed"

	// EventReasonPrimarySuccess is an event generated when VRG is successfully
	// processed as Primary.
	EventReasonPrimarySuccess = "PrimaryVRGProcessSuccess"

	// EventReasonSecondarySuccess is an event generated when VRG is successfully
	// processed as Primary.
	EventReasonSecondarySuccess = "SecondaryVRGProcessSuccess"

	// EventReasonSecondarySuccess is an event generated when VRG is successfully
	// processed as Primary.
	EventReasonDeleteSuccess = "VRGDeleteSuccess"
	// TODO: Add any additional events (or remove one of existing ones above) if necessary.
)

// EventReporter is custom events reporter type which allows user to limit the events
type EventReporter struct {
	recorder record.EventRecorder

	// lastReportedEvent will have a last captured event
	lastReportedEvent map[string]string

	// lastReportedEventTime will be the time of lastReportedEvent
	lastReportedEventTime map[string]time.Time
}

// NewEventReporter returns EventReporter object
func NewEventReporter(recorder record.EventRecorder) *EventReporter {
	return &EventReporter{
		recorder:              recorder,
		lastReportedEvent:     make(map[string]string),
		lastReportedEventTime: make(map[string]time.Time),
	}
}

// ReportIfNotPresent will report event if lastReportedEvent is not the same in last 10 minutes
// TODO: The duration 10 minutes can be changed to some other value if necessary
func ReportIfNotPresent(recorder *EventReporter, instance runtime.Object,
	eventType, eventReason, msg string) {
	nameSpacedName, err := getNameSpacedName(instance)
	if err != nil {
		return
	}

	eventKey := getEventKey(eventType, eventReason, msg)

	if recorder.lastReportedEvent[nameSpacedName] != eventKey ||
		recorder.lastReportedEventTime[nameSpacedName].Add(time.Second*LastEventValidDuration).Before(time.Now()) {
		recorder.lastReportedEvent[nameSpacedName] = eventKey
		recorder.lastReportedEventTime[nameSpacedName] = time.Now()
		recorder.recorder.Event(instance, eventType, eventReason, msg)
	}
}

func getNameSpacedName(instance runtime.Object) (string, error) {
	objMeta, err := meta.Accessor(instance)
	if err != nil {
		return "", fmt.Errorf("failed to access the instance from object")
	}

	return objMeta.GetNamespace() + ":" + objMeta.GetName(), nil
}

func getEventKey(eventType, eventReason, msg string) string {
	return fmt.Sprintf("%s:%s:%s", eventType, eventReason, msg)
}
