module github.com/ramendr/ramen

go 1.15

require (
	github.com/aws/aws-sdk-go v1.38.3
	github.com/csi-addons/volume-replication-operator v0.0.0-20210406063936-e65896618f92
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-logr/logr v0.3.0
	github.com/kube-storage/volume-replication-operator v0.0.0-20210324093807-c1ce13669bae
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/open-cluster-management/api v0.0.0-20201007180356-41d07eee4294
	github.com/open-cluster-management/multicloud-operators-placementrule v1.0.1-2020-06-08-14-28-27.0.20201118195339-05a8c4c89c12
	github.com/open-cluster-management/multicloud-operators-subscription v1.2.2-2-20201130-59f96
	github.com/pkg/errors v0.9.1
	github.com/shyamsundarr/volrep-shim-operator v0.0.0-20210310121354-2f9f9b83efb6
	go.uber.org/zap v1.15.0
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.7.0
)

replace k8s.io/client-go => k8s.io/client-go v0.20.0
