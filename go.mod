module github.com/ramendr/ramen

go 1.22.6

// This replace should always be here for ease of development.
replace github.com/ramendr/ramen/api => ./api

require (
	github.com/aws/aws-sdk-go v1.44.289
	github.com/backube/volsync v0.7.1
	github.com/csi-addons/kubernetes-csi-addons v0.8.0
	github.com/go-logr/logr v1.3.0
	github.com/google/uuid v1.3.1
	github.com/kubernetes-csi/external-snapshotter/client/v7 v7.0.0
	github.com/onsi/ginkgo/v2 v2.13.0
	github.com/onsi/gomega v1.30.0
	github.com/open-cluster-management-io/api v0.0.0-00010101000000-000000000000
	github.com/open-cluster-management/api v0.0.0-20210527013639-a6845f2ebcb1
	github.com/operator-framework/api v0.17.6
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.16.0
	github.com/ramendr/ramen/api v0.0.0-20240117171503-e11c56eac24d
	github.com/ramendr/recipe v0.0.0-20230817160432-729dc7fd8932
	github.com/stolostron/multicloud-operators-foundation v0.0.0-20220824091202-e9cd9710d009
	github.com/stolostron/multicloud-operators-placementrule v1.2.4-1-20220311-8eedb3f.0.20230828200208-cd3c119a7fa0
	github.com/vmware-tanzu/velero v1.9.1
	go.uber.org/zap v1.26.0
	golang.org/x/exp v0.0.0-20230522175609-2e198f4a06a1
	golang.org/x/time v0.3.0
	k8s.io/api v0.29.0
	k8s.io/apiextensions-apiserver v0.28.3
	k8s.io/apimachinery v0.29.0
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.29.0
	k8s.io/kube-openapi v0.0.0-20231010175941-2dd684a91f00
	open-cluster-management.io/config-policy-controller v0.12.0
	open-cluster-management.io/governance-policy-propagator v0.12.0
	sigs.k8s.io/controller-runtime v0.16.3
	sigs.k8s.io/yaml v1.3.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/csi-addons/spec v0.2.1-0.20230606140122-d20966d2e444 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/evanphx/json-patch/v5 v5.6.0 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-logr/zapr v1.2.4 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20230510103437-eeec1cb781c3 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kubernetes-csi/external-snapshotter/client/v6 v6.2.0 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pelletier/go-toml/v2 v2.0.6 // indirect
	github.com/prometheus/client_model v0.4.0 // indirect
	github.com/prometheus/common v0.44.0 // indirect
	github.com/prometheus/procfs v0.10.1 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/spf13/afero v1.9.3 // indirect
	github.com/spf13/cast v1.5.0 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/spf13/viper v1.15.0 // indirect
	github.com/subosito/gotenv v1.4.2 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/net v0.18.0 // indirect
	golang.org/x/oauth2 v0.11.0 // indirect
	golang.org/x/sys v0.14.0 // indirect
	golang.org/x/term v0.14.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/tools v0.12.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231106174013-bbf56f31fb17 // indirect
	google.golang.org/grpc v1.59.0 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.110.1 // indirect
	k8s.io/utils v0.0.0-20230726121419-3b25d923346b // indirect
	open-cluster-management.io/api v0.11.1-0.20230905055724-cf1ead467a83 // indirect
	open-cluster-management.io/multicloud-operators-subscription v0.12.0 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
)

// replace directives to accommodate for stolostron
replace k8s.io/client-go v12.0.0+incompatible => k8s.io/client-go v0.29.0

replace github.com/open-cluster-management-io/api => open-cluster-management.io/api v0.10.0
