// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type testCases struct {
	jsonPathExprs string
	result        bool
	jsonText      []byte
}

type testCasesObject struct {
	hook    *kubeobjects.HookSpec
	result  bool
	jsonObj client.Object
}

var jsonDeployment = []byte(`{
    "kind": "Deployment",
    "spec": {
        "progressDeadlineSeconds": 600,
        "replicas": 1,
        "revisionHistoryLimit": 10
    },
    "status": {
        "replicas": 1,
        "conditions": [
            {
                "status": "True",
                "type": "Progressing"
            },
            {
                "status": "True",
                "type": "Available"
            }
        ]
    }
    }`)

var jsonPod = []byte(`{
		"kind": "Pod",
		"spec": {
			"progressDeadlineSeconds": 600,
			"replicas": 1,
			"revisionHistoryLimit": 10
		},
		"status": {
			"replicas": 1,
			"conditions": [
				{
					"status": "True",
					"type": "Progressing"
				},
				{
					"status": "True",
					"type": "Available"
				}
			]
		}
		}`)

var jsonStatefulset = []byte(`{
			"kind": "Statefulset",
			"spec": {
				"progressDeadlineSeconds": 600,
				"replicas": 1,
				"revisionHistoryLimit": 10
			},
			"status": {
				"replicas": 1,
				"conditions": [
					{
						"status": "True",
						"type": "Progressing"
					},
					{
						"status": "True",
						"type": "Available"
					}
				]
			}
			}`)

var rep int32 = 1

var testCasesData = []testCases{
	{
		jsonPathExprs: "{$.status.conditions[0].status} == True",
		result:        true,
		jsonText:      jsonDeployment,
	},
	{
		jsonPathExprs: "{$.spec.replicas} == 1",
		result:        true,
		jsonText:      jsonPod,
	},
	{
		jsonPathExprs: "{$.status.conditions[0].status} == {True}",
		result:        false,
		jsonText:      jsonStatefulset,
	},
}

var testCasesObjectData = []testCasesObject{
	{
		hook:    getHookSpec("Pod", "{$.status.phase} == {'Running'}"),
		result:  true,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Pod", "{$.status.conditions[0].type} == {'Ready'}"),
		result:  true,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Pod", "{$.status.containerStatuses[0].restartCount} == 2"),
		result:  true,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Deployment", "{$.spec.replicas} == {$.status.readyReplicas}"),
		result:  true,
		jsonObj: getDeploymentContent(),
	},
	{
		hook:    getHookSpec("Statefulset", "{$.status.readyReplicas} == {$.status.currentReplicas}"),
		result:  true,
		jsonObj: getStatefulSetContent(),
	},
}

func getHookSpec(resourceType, condition string) *kubeobjects.HookSpec {
	return &kubeobjects.HookSpec{
		Name:           "test-hook",
		SelectResource: resourceType,
		Chk: kubeobjects.Check{
			Name:      "test-check",
			Condition: condition,
		},
	}
}

func TestEvaluateCheckHookExp(t *testing.T) {
	for i, tt := range testCasesData {
		test := tt

		t.Run(strconv.Itoa(i), func(t *testing.T) {
			var jsonData map[string]interface{}

			err := json.Unmarshal(tt.jsonText, &jsonData)
			if err != nil {
				t.Error(err)
			}

			_, err = util.EvaluateCheckHookExp(test.jsonPathExprs, jsonData)
			if (err == nil) != test.result {
				t.Errorf("EvaluateCheckHookExp() = %v, want %v", err, test.result)
			}
		})
	}
}

func TestEvaluateCheckHookForObjects(t *testing.T) {
	log := zap.New(zap.UseDevMode(true))

	for i, tt := range testCasesObjectData {
		test := tt
		objs := []client.Object{test.jsonObj}

		t.Run(strconv.Itoa(i), func(t *testing.T) {
			_, err := util.EvaluateCheckHookForObjects(objs, test.hook, log)
			if (err == nil) != test.result {
				t.Errorf("EvaluateCheckHookExpObject() = %v, want %v", err, test.result)
			}
		})
	}
}

func Test_isValidJsonPathExpression(t *testing.T) {
	type args struct {
		expr string
	}

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Simple expression",
			args: args{
				expr: "$.spec.replicas",
			},
			want: true,
		},
		{
			name: "no $ at the start",
			args: args{
				expr: "{.spec.replicas}",
			},
			want: true,
		},
		{
			name: "element in array",
			args: args{
				expr: "{$.status.conditions[0].status}",
			},
			want: true,
		},
		{
			name: "spec 1",
			args: args{
				expr: "{$.status.readyReplicas}",
			},
			want: true,
		},
		{
			name: "spec 2",
			args: args{
				expr: "{$.status.containerStatuses[0].ready}",
			},
			want: true,
		},
		{
			name: "spec 3a",
			args: args{
				expr: "{True}",
			},
			want: false,
		},
		{
			name: "spec 3b",
			args: args{
				expr: "{False}",
			},
			want: false,
		},
		{
			name: "spec 3c",
			args: args{
				expr: "{true}",
			},
			want: true,
		},
		{
			name: "spec 3d",
			args: args{
				expr: "{false}",
			},
			want: true,
		},
		{
			name: "Spec 4",
			args: args{
				expr: "{$.spec.replicas}",
			},
			want: true,
		},
		{
			name: "expression with == operator",
			args: args{
				expr: "$.store.book[?(@.price > 10)].title==$.store.book[0].title",
			},
			want: true,
		},
		{
			name: "expression with > operator",
			args: args{
				expr: "$.store.book[?(@.author CONTAINS 'Smith')].price>20",
			},
			want: true,
		},
		{
			name: "expression with >= operator",
			args: args{
				expr: "$.user.age>=$.minimum.age",
			},
			want: true,
		},
		{
			name: "expression with < operator",
			args: args{
				expr: "$.user.age<$.maximum.age",
			},
			want: true,
		},
		{
			name: "expression with <= operator",
			args: args{
				expr: "$.user.age<=$.maximum.age",
			},
			want: true,
		},
		{
			name: "expression with != operator",
			args: args{
				expr: "$.user.age!=$.maximum.age",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		test := tt

		t.Run(test.name, func(t *testing.T) {
			if got := util.IsValidJSONPathExpression(test.args.expr); got != test.want {
				t.Errorf("IsValidJSONPathExpression() = %v, want %v", got, test.want)
			}
		})
	}
}

func getPodContent() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "test-image",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "test-container",
					Ready:        true,
					RestartCount: 2,
				},
			},
		},
	}
}

func getDeploymentContent() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deploy",
			Namespace: "test-namespace",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &rep,
		},
		Status: appsv1.DeploymentStatus{
			Replicas: 1,
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentProgressing,
					Status: corev1.ConditionTrue,
				},
			},
			ReadyReplicas: 1,
		},
	}
}

func getStatefulSetContent() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-statefulset",
			Namespace: "test-namespace",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &rep,
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas:   3,
			CurrentReplicas: 3,
		},
	}
}
