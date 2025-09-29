package hooks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ramendr/ramen/internal/controller/hooks"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
)

func TestGetHookExecutor(t *testing.T) {
	client := fake.NewFakeClient()
	reader := fake.NewFakeClient()

	executor, err := hooks.GetHookExecutor(getHookSpecForFactoryTest("check", client, reader))
	assert.Nil(t, err)

	_, ok := executor.(hooks.CheckHook)
	assert.True(t, ok)

	executor, err = hooks.GetHookExecutor(getHookSpecForFactoryTest("exec", client, reader))
	assert.Nil(t, err)

	_, ok = executor.(hooks.ExecHook)
	assert.True(t, ok)

	executor, err = hooks.GetHookExecutor(getHookSpecForFactoryTest("scale", client, reader))
	assert.Nil(t, err)

	_, ok = executor.(hooks.ScaleHook)
	assert.True(t, ok)

	executor, err = hooks.GetHookExecutor(getHookSpecForFactoryTest("undefined", client, reader))

	assert.Nil(t, executor)
	assert.EqualError(t, err, "unsupported hook type: undefined")
}

func getHookSpecForFactoryTest(hookType string, client client.WithWatch, reader client.WithWatch) hooks.HookContext {
	return hooks.HookContext{
		Hook: kubeobjects.HookSpec{
			Name:      "test",
			Namespace: "test-ns",
			Type:      hookType,
		},
		Client:         client,
		Reader:         reader,
		Scheme:         client.Scheme(),
		RecipeElements: util.RecipeElements{},
	}
}
