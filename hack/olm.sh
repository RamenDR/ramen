#!/bin/sh
# shellcheck disable=1090,2086,1091

OLM_BASE_URL="https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v0.19.1"

. "$(dirname $0)"/until_true_or_n.sh

olm_deploy()
{
	kubectl --context $1 apply -f $OLM_BASE_URL/crds.yaml
	kubectl --context $1 wait --for condition=established -f $OLM_BASE_URL/crds.yaml

	kubectl --context $1 apply -f $OLM_BASE_URL/olm.yaml
	kubectl --context $1 rollout status -w -n olm deployment/olm-operator
	kubectl --context $1 rollout status -w -n olm deployment/catalog-operator

	until_true_or_n 300 eval test \"\$\(kubectl --context $1 get -n olm csv/packageserver -o jsonpath='{.status.phase}'\)\" = Succeeded

	kubectl --context $1 rollout status -w -n olm deployment/packageserver

	kubectl --context $1 delete -n olm catalogsources.operators.coreos.com/operatorhubio-catalog
}

olm_undeploy()
{
	kubectl --context $1 delete -n olm csv/packageserver #apiservices.apiregistration.k8s.io/v1.packages.operators.coreos.com
	kubectl --context $1 delete --ignore-not-found -f $OLM_BASE_URL/olm.yaml
	kubectl --context $1 delete -f $OLM_BASE_URL/crds.yaml
}

olm_unset()
{
#	unset -f until_true_or_n
	unset -f olm_unset
	unset -f olm_undeploy
	unset -f olm_deploy
}
