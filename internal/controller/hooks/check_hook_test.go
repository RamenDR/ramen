// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	rmnv1 "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
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
		jsonPathExprs: "{$.status.conditions[0].status} == {True}",
		result:        true,
		jsonText:      jsonDeployment,
	},
	{
		jsonPathExprs: "{$.spec.replicas} == 1",
		result:        false,
		jsonText:      jsonPod,
	},
	/* The json expression that can be provided as a condition in the check hook spec follows the format

	<expression1> op <expression2>
	With some of the conditions below:

	1. Both the expressions should individually comply to be a valid json expression.
	2. An expression should be enclosed with curly braces {}.
	3. An expression could either be a json expression that can be evaluated from the pod/deployment/statefulset json
	   content or a literal which can be either string or number.
	Some improvements needs to be done which are added as sub-issues linked to this one.

	Adding the commented TCs which are to pass when the improvements are done.
	*/
	{
		jsonPathExprs: "{$.status.conditions[0].status} == True",
		result:        false,
		jsonText:      jsonStatefulset,
	},
	{
		jsonPathExprs: "{$.spec.replicas} == {1}",
		result:        true,
		jsonText:      jsonPod,
	},
	{
		jsonPathExprs: "{$.status.conditions[0].status} == {\"True\"}",
		result:        true,
		jsonText:      jsonStatefulset,
	},
	{
		jsonPathExprs: "{$.status.conditions[?(@.type==\"Progressing\")].status} == {True}",
		result:        true,
		jsonText:      jsonPod,
	},
	{
		jsonPathExprs: "{$.spec.replicas} == {1} || {$.status.conditions[0].status} == {True}",
		result:        true,
		jsonText:      jsonPod,
	},
	{
		jsonPathExprs: "{$.spec.replicas} == {1} && {$.status.conditions[0].status} == {True}",
		result:        true,
		jsonText:      jsonPod,
	},
}

var testCasesObjectData = []testCasesObject{
	{
		hook:    getHookSpec("Pod", "{$.status.phase} == {'Running'}"),
		result:  true,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Pod", "{$.status.phase} == Running"),
		result:  false,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Pod", "{$.status.phase} == {Running}"),
		result:  true,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Pod", "{$.status.conditions[0].type} == {'Ready'}"),
		result:  true,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Pod", "{$.status.containerStatuses[0].restartCount} == {2}"),
		result:  true,
		jsonObj: getPodContent(),
	},
	{
		hook:    getHookSpec("Pod", "{$.status.containerStatuses[0].restartCount} == 2"),
		result:  false,
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
	{
		hook:    getHookSpec("Deployment", "{$.status.conditions[0].status} == {True}"),
		result:  true,
		jsonObj: getDeploymentContent(),
	},
	{
		hook: getHookSpec("Deployment", "{$.spec.replicas} == {$.status.readyReplicas}"+
			" || {$.spec.replicas} == {$.status.updatedReplicas} && {$.spec.revisionHistoryLimit} == {10}"),
		result:  true,
		jsonObj: getDeploymentContent(),
	},
	{
		hook: getHookSpec("Deployment", "({$.spec.replicas} == {$.status.readyReplicas}) && (({$.spec.replicas} =="+
			" {$.status.updatedReplicas}) || ({$.spec.revisionHistoryLimit} != {11}))"),
		result:  true,
		jsonObj: getDeploymentContent(),
	},
	{
		hook: getHookSpec("Deployment", "(({$.spec.replicas} == {$.status.readyReplicas}) && ({$.spec.replicas} =="+
			" {$.status.updatedReplicas})) || ({$.spec.revisionHistoryLimit} != {11})"),
		result:  true,
		jsonObj: getDeploymentContent(),
	},
}

func setupFakeClient(t *testing.T) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	err = appsv1.AddToScheme(scheme)
	assert.NoError(t, err)

	err = rmnv1.AddToScheme(scheme)
	assert.NoError(t, err)

	// nolint:errcheck
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Pod{}, "metadata.name", func(obj client.Object) []string {
			return []string{obj.(*corev1.Pod).Name}
		}).
		WithIndex(&appsv1.Deployment{}, "metadata.name", func(obj client.Object) []string {
			return []string{obj.(*unstructured.Unstructured).GetName()}
		}).Build()
	assert.NotNil(t, fakeClient)

	return fakeClient
}

func getHookSpec(resourceType, condition string) *kubeobjects.HookSpec {
	return &kubeobjects.HookSpec{
		Name:           "test-hook",
		SelectResource: resourceType,
		Chk: kubeobjects.Check{
			Name:      "test-check",
			Condition: condition,
			OnError:   "continue",
		},
	}
}

func TestExecutCheckHookForPodWithNoSelector(t *testing.T) {
	fakeClient := setupFakeClient(t)

	assert.NotNil(t, fakeClient)

	pod := getPodSpec("busybox")
	err := fakeClient.Create(context.Background(), pod)
	assert.Nil(t, err)

	cHook := hooks.CheckHook{
		Hook:   getHookSpec("pod", "{$.status.phase} == {Running}"),
		Reader: fakeClient,
	}

	log := zap.New(zap.UseDevMode(true))
	err = cHook.Execute(log)
	assert.NotNil(t, err)
}

func TestExecutCheckHookForPodWithNameSelector(t *testing.T) {
	fakeClient := setupFakeClient(t)
	assert.NotNil(t, fakeClient)

	pod := getPodSpec("busybox")
	pod.Status.Phase = "Running"
	err := fakeClient.Create(context.Background(), pod)
	assert.Nil(t, err)

	pod = getPodSpec("busybox1")
	err = fakeClient.Create(context.Background(), pod)
	assert.Nil(t, err)

	hook := getHookSpec("pod", "{$.status.phase} == {Running}")
	hook.NameSelector = "busybox"

	cHook := hooks.CheckHook{
		Hook:   hook,
		Reader: fakeClient,
	}

	log := zap.New(zap.UseDevMode(true))
	err = cHook.Execute(log)
	assert.Nil(t, err)
}

func TestExecuteCheckHookForDeployment(t *testing.T) {
	fakeClient := setupFakeClient(t)
	assert.NotNil(t, fakeClient)

	dep := getDeploymentContent()

	err := fakeClient.Create(context.Background(), dep)
	assert.Nil(t, err)

	hook := getHookSpec("deployment", "{$.spec.replicas} == {$.status.replicas}")
	hook.NameSelector = "test-deploy"

	cHook := hooks.CheckHook{
		Hook:   hook,
		Reader: fakeClient,
	}

	log := zap.New(zap.UseDevMode(true))
	err = cHook.Execute(log)
	assert.Nil(t, err)
}

func TestExecuteCheckHookForStatefulSet(t *testing.T) {
	fakeClient := setupFakeClient(t)
	assert.NotNil(t, fakeClient)

	ss := getStatefulSetContent()
	ss.Labels = map[string]string{
		"appname": "test",
	}

	err := fakeClient.Create(context.Background(), ss)
	assert.Nil(t, err)

	hook := getHookSpec("statefulset", "{$.spec.replicas} != {$.status.readyReplicas}")
	hook.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"appname": "test",
		},
	}

	cHook := hooks.CheckHook{
		Hook:   hook,
		Reader: fakeClient,
	}

	log := zap.New(zap.UseDevMode(true))
	err = cHook.Execute(log)
	assert.Nil(t, err)
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

			actualRes, err := hooks.EvaluateCheckHookExp(test.jsonPathExprs, jsonData)
			if err != nil {
				t.Logf("EvaluateCheckHookExp() = %v", err)
			}

			if actualRes != test.result {
				t.Errorf("EvaluateCheckHookExp(), got %v want %v", actualRes, test.result)
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
			actualRes, err := hooks.EvaluateCheckHookForObjects(objs, test.hook, log)
			if err != nil {
				t.Logf("EvaluateCheckHookExp() = %v", err)
			}

			if actualRes != test.result {
				t.Errorf("EvaluateCheckHookExp(), got %v want %v", actualRes, test.result)
			}
		})
	}
}

func TestSkipHookIfNotPresentForStatefulset(t *testing.T) {
	fakeClient := setupFakeClient(t)
	assert.NotNil(t, fakeClient)

	hook := getHookSpec("statefulset", "{$.spec.replicas} != {$.status.readyReplicas}")
	hook.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"appname": "test",
		},
	}
	hook.SkipHookIfNotPresent = true
	hook.Timeout = 10

	cHook := hooks.CheckHook{
		Hook:   hook,
		Reader: fakeClient,
	}

	log := zap.New(zap.UseDevMode(true))
	err := cHook.Execute(log)
	assert.Nil(t, err)
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
		/* Same comments as above */
		{
			name: "String expression",
			args: args{
				expr: "{True}",
			},
			want: true,
		},
		{
			name: "String expression 1",
			args: args{
				expr: "{\"Ready\"}",
			},
			want: true,
		},
		{
			name: "Number expression",
			args: args{
				expr: "{10}",
			},
			want: true,
		},
		{
			name: "Number expression",
			args: args{
				expr: "10",
			},
			want: false,
		},
		{
			name: "Simple expression",
			args: args{
				expr: "$.spec.replicas",
			},
			want: false,
		},
		{
			name: "no $ at the start",
			args: args{
				expr: "{.spec.replicas}",
			},
			want: false,
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
			want: true,
		},
		{
			name: "spec 3b",
			args: args{
				expr: "{False}",
			},
			want: true,
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
				expr: "{$.store.book[?(@.price > 10)].title==$.store.book[0].title}",
			},
			want: true,
		},
		{
			name: "expression with > operator",
			args: args{
				expr: "{$.store.book[?(@.author CONTAINS 'Smith')].price>20}",
			},
			want: true,
		},
		{
			name: "expression with >= operator",
			args: args{
				expr: "{$.user.age>=$.minimum.age}",
			},
			want: true,
		},
		{
			name: "expression with < operator",
			args: args{
				expr: "{$.user.age<$.maximum.age}",
			},
			want: true,
		},
		{
			name: "expression with <= operator",
			args: args{
				expr: "{$.user.age<=$.maximum.age}",
			},
			want: true,
		},
		{
			name: "expression with != operator",
			args: args{
				expr: "{$.user.age!=$.maximum.age}",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		test := tt

		t.Run(test.name, func(t *testing.T) {
			if got := hooks.IsValidJSONPathExpression(test.args.expr); got != test.want {
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
			ReadyReplicas:   1,
			UpdatedReplicas: 1,
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
