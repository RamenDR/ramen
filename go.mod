module github.com/ramendr/ramen

go 1.16

require (
	github.com/aws/aws-sdk-go v1.38.41
	github.com/csi-addons/volume-replication-operator v0.1.0
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.15.0
	github.com/open-cluster-management/api v0.0.0-20210527013639-a6845f2ebcb1
	github.com/operator-framework/api v0.10.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/client_model v0.2.0
	github.com/stolostron/config-policy-controller v0.0.0-20220112151220-7bd880ac5703
	github.com/stolostron/governance-policy-propagator v0.0.0-20220125192743-95d49290a318
	github.com/stolostron/multicloud-operators-foundation v1.0.0-2021-10-26-20-16-14.0.20220119065659-a58b01295a6f
	github.com/stolostron/multicloud-operators-placementrule v1.2.4-0-20220112-8eedb3f
	go.uber.org/zap v1.19.0
	k8s.io/api v0.22.6
	k8s.io/apimachinery v0.22.6
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.22.1
	k8s.io/kube-openapi v0.0.0-20211109043538-20434351676c
	sigs.k8s.io/controller-runtime v0.9.7
)

replace (
	github.com/deislabs/oras => github.com/deislabs/oras v0.11.1
	github.com/kubevirt/terraform-provider-kubevirt => github.com/nirarg/terraform-provider-kubevirt v0.0.0-20201222125919-101cee051ed3
	github.com/metal3-io/baremetal-operator => github.com/openshift/baremetal-operator v0.0.0-20200715132148-0f91f62a41fe
	github.com/metal3-io/cluster-api-provider-baremetal => github.com/openshift/cluster-api-provider-baremetal v0.0.0-20190821174549-a2a477909c1d
	github.com/openshift/hive/apis => github.com/openshift/hive/apis v0.0.0-20211028175624-6c9c8e4bff7f
	github.com/terraform-providers/terraform-provider-ignition/v2 => github.com/community-terraform-providers/terraform-provider-ignition/v2 v2.1.0
	k8s.io/client-go => k8s.io/client-go v0.22.6
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20201113171705-d219536bb9fd
	kubevirt.io/client-go => kubevirt.io/client-go v0.29.0
	sigs.k8s.io/cluster-api-provider-aws => github.com/openshift/cluster-api-provider-aws v0.2.1-0.20201022175424-d30c7a274820
	sigs.k8s.io/cluster-api-provider-azure => github.com/openshift/cluster-api-provider-azure v0.1.0-alpha.3.0.20201016155852-4090a6970205
	sigs.k8s.io/cluster-api-provider-openstack => github.com/openshift/cluster-api-provider-openstack v0.0.0-20201116051540-155384b859c5
)
