module github.com/ramendr/ramen

go 1.15

require (
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-logr/logr v0.3.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/open-cluster-management/api v0.0.0-20201007180356-41d07eee4294
	github.com/open-cluster-management/multicloud-operators-channel v1.0.1-0.20201120143200-e505a259de45
	github.com/open-cluster-management/multicloud-operators-deployable v0.2.2-pre
	github.com/open-cluster-management/multicloud-operators-placementrule v1.0.1-2020-06-08-14-28-27.0.20201118195339-05a8c4c89c12
	github.com/open-cluster-management/multicloud-operators-subscription v1.2.2-2-20201130-59f96
	github.com/pkg/errors v0.9.1
	github.com/prometheus/common v0.10.0
	github.com/shyamsundarr/volrep-shim-operator v0.0.0-20210310121354-2f9f9b83efb6 // indirect
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.7.0
)

replace k8s.io/client-go => k8s.io/client-go v0.20.0
