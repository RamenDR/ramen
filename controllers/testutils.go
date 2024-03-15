package controllers

import (
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

// WriteKubeConfigToFile writes the kubeconfig content to a specified file path.
func WriteKubeConfigToFile(kubeConfigContent, filePath string) error {
	return os.WriteFile(filePath, []byte(kubeConfigContent), 0o600)
}

// ConvertRestConfigToKubeConfig converts a rest.Config to a kubeconfig string.
func ConvertRestConfigToKubeConfig(restConfig *rest.Config) (string, error) {
	kubeConfig := api.NewConfig()

	cluster := api.NewCluster()
	cluster.Server = restConfig.Host
	cluster.CertificateAuthorityData = restConfig.CAData

	user := api.NewAuthInfo()
	user.ClientCertificateData = restConfig.CertData
	user.ClientKeyData = restConfig.KeyData
	user.Token = restConfig.BearerToken

	context := api.NewContext()
	context.Cluster = "cluster"
	context.AuthInfo = "user"

	kubeConfig.Clusters["cluster"] = cluster
	kubeConfig.AuthInfos["user"] = user
	kubeConfig.Contexts["test"] = context
	kubeConfig.CurrentContext = "test"

	kubeConfigContent, err := clientcmd.Write(*kubeConfig)
	if err != nil {
		return "", err
	}

	return string(kubeConfigContent), nil
}
