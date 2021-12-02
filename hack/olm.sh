#!/bin/sh
# shellcheck disable=1090,2086
. "$(dirname $0)"/until_true_or_n.sh
olm_kubectl()
{
	kubectl --context $1 $2 -f https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v0.19.1/$3.yaml
}
olm_crds_kubectl()
{
	olm_kubectl $1 "$2" crds
}
olm_operators_kubectl()
{
	olm_kubectl $1 "$2" olm
}
olm_operators_deploy()
{
	olm_operators_kubectl $1 apply
	kubectl --context $1 rollout status -w -n olm deployment/olm-operator
	kubectl --context $1 rollout status -w -n olm deployment/catalog-operator
	until_true_or_n 30 eval test \"\$\(kubectl --context $1 get -n olm csv/packageserver -o jsonpath='{.status.phase}'\)\" = Succeeded
	kubectl --context $1 rollout status -w -n olm deployment/packageserver
}
olm_deploy()
{
	olm_crds_kubectl $1 apply
	olm_crds_kubectl $1 wait\ --for\ condition=established
	olm_operators_deploy $1
	kubectl --context $1 delete -n olm catalogsources.operators.coreos.com/operatorhubio-catalog
}
olm_undeploy()
{
	kubectl --context $1 delete -n olm csv/packageserver #apiservices.apiregistration.k8s.io/v1.packages.operators.coreos.com
	olm_operators_kubectl $1 delete\ --ignore-not-found
	olm_crds_kubectl $1 delete
}
olm_unset()
{
#	unset -f until_true_or_n
	unset -f olm_unset
	unset -f olm_undeploy
	unset -f olm_deploy
	unset -f olm_operators_deploy
	unset -f olm_operators_kubectl
	unset -f olm_crds_kubectl
	unset -f olm_kubectl
}
