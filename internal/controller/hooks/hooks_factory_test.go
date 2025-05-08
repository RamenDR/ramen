package hooks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetHookExecutor(t *testing.T) {
	client := fake.NewFakeClient()

	executor, err := hooks.GetHookExecutor(getHookSpecForFactoryTest("check"), client, client.Scheme(),
		util.RecipeElements{})
	assert.Nil(t, err)

	_, ok := executor.(hooks.CheckHook)
	assert.True(t, ok)

	executor, err = hooks.GetHookExecutor(getHookSpecForFactoryTest("exec"), client, client.Scheme(),
		util.RecipeElements{})
	assert.Nil(t, err)

	_, ok = executor.(hooks.ExecHook)
	assert.True(t, ok)

	executor, err = hooks.GetHookExecutor(getHookSpecForFactoryTest("undefined"), client, client.Scheme(),
		util.RecipeElements{})

	assert.Nil(t, executor)
	assert.EqualError(t, err, "unsupported hook type")
}

func getHookSpecForFactoryTest(hookType string) kubeobjects.HookSpec {
	return kubeobjects.HookSpec{
		Name:      "test",
		Namespace: "test-ns",
		Type:      hookType,
	}
}
