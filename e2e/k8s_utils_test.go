package e2e_test

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func (ctx *TestContext) HubClient() kubernetes.Interface {
	return ctx.Clusters["hub"].k8sClientSet
}

func (ctx *TestContext) C1Client() kubernetes.Interface {
	return ctx.Clusters["c1"].k8sClientSet
}

func (ctx *TestContext) C2Client() kubernetes.Interface {
	return ctx.Clusters["c2"].k8sClientSet
}

func getClientSetFromKubeConfigPath(kubeconfigPath string) (kubernetes.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	k8sClientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return k8sClientSet, nil
}
