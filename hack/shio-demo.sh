#!/bin/sh

# Infra deploy
hack/minikube-ramen.sh deploy
hack/velero-test.sh velero_deploy\ cluster1
hack/velero-test.sh velero_deploy\ cluster2
kubectl --context cluster1 -nvelero create secret generic s3secret --from-literal aws='[default]
aws_access_key_id=minio
aws_secret_access_key=minio123
'
kubectl --context cluster2 -nvelero create secret generic s3secret --from-literal aws='[default]
aws_access_key_id=minio
aws_secret_access_key=minio123
'

minikube profile list

# App deploy
kubectl --context cluster1        create namespace asdf
kubectl --context cluster1 -nasdf create configmap asdf
kubectl --context cluster1 -nasdf create secret generic asdf --from-literal=key1=value1
kubectl --context cluster1 -nasdf create deploy asdf --image busybox -- sh -c while\ true\;do\ date\;sleep\ 60\;done
kubectl --context cluster1 -nasdf create -k https://github.com/RamenDR/ocm-ramen-samples/busybox
kubectl --context cluster1 -nasdf get cm,secret,deploy,rs,po,pvc

kubectl --context cluster1 -nvelero get deploy/velero
kubectl --context cluster1 -nvelero get secret/s3secret -oyaml

# Clean?
kubectl --context cluster1 -nvelero get bsl,backup,restore
kubectl --context cluster2 -nvelero get bsl,backup,restore
mc tree cluster2
kubectl --context cluster2          get ns/asdf

# Protect
hack/minikube-ramen.sh eval\ application_sample_vrg_kubectl\ cluster1\ cluster2\ asdf\ create\\\ -oyaml
time kubectl --context cluster1 -nasdf wait vrg/bb --for condition=clusterdataprotected --timeout 2m
mc tree cluster2
mc cp -q cluster2/bucket/asdf/bb/backups/a/a-resource-list.json.gz /tmp;gzip -df /tmp/a-resource-list.json.gz;python -m json.tool </tmp/a-resource-list.json

# Recover
kubectl --context cluster2        create namespace asdf
hack/minikube-ramen.sh eval\ application_sample_vrg_kubectl\ cluster2\ cluster1\ asdf\ create\\\ -oyaml
time kubectl --context cluster2 -nasdf wait vrg/bb --for condition=clusterdataready --timeout 2m
kubectl --context cluster2 -nasdf get cm,secret,deploy,rs,po,pvc









mc cp -q cluster2/bucket/asdf/bb/restores/a/restore-a-results.gz /tmp;gzip -df /tmp/restore-a-results.gz;python -m json.tool </tmp/restore-a-results
mc cp -q cluster2/bucket/asdf/bb/restores/a/restore-a-logs.gz /tmp;gzip -df /tmp/restore-a-logs.gz; cat /tmp/restore-a-logs

# App cleanup
kubectl --context cluster1 -nasdf delete -fhack/vrg.yaml
kubectl --context cluster1 -nasdf patch vrg/bb --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
kubectl --context cluster1 -nasdf delete events --all
kubectl --context cluster2        delete namespace asdf
kubectl --context cluster2 -nasdf patch vrg/bb --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
velero --kubecontext cluster1 delete --all --confirm backups
velero --kubecontext cluster1 delete --all --confirm backup-locations
velero --kubecontext cluster1 delete --all --confirm restores
velero --kubecontext cluster2 delete --all --confirm backups
velero --kubecontext cluster2 delete --all --confirm backup-locations
velero --kubecontext cluster2 delete --all --confirm restores
velero --kubecontext cluster1 get backups
velero --kubecontext cluster1 get backup-locations
velero --kubecontext cluster1 get restores
velero --kubecontext cluster2 get backups
velero --kubecontext cluster2 get backup-locations
velero --kubecontext cluster2 get restores
mc tree cluster2
mc rm cluster2/bucket/asdf/bb/ --recursive --force

# Infra cleanup
kubectl --context cluster1 -nvelero delete secret s3secret
kubectl --context cluster2 -nvelero delete secret s3secret
hack/minikube-ramen.sh undeploy
