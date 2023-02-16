#!/bin/bash
# setup scripts. Run on each cluster
set -x
set -e

# velero
VELERO_NAMESPACE=velero
CLUSTER=$(kubectl config current-context)
MINIKUBE_IP=$(minikube ip --profile "$CLUSTER")
WORKING_DIRECTORY=$(pwd)
VELERO_INSTALL_DIRECTORY=~/Downloads/velero-v1.9.3-linux-amd64/

if [[ $(kubectl get deployment.apps/velero -n $VELERO_NAMESPACE --no-headers | wc -l) -eq 0 ]]; then
  if [[ ! -e $VELERO_INSTALL_DIRECTORY ]]; then
    set +x
    echo "Velero install directory not detected."
    echo "Download Velero executable v1.9.3 and unzip to directory $VELERO_INSTALL_DIRECTORY"
    echo "Then run command below, then run these scripts again."
    echo "cp config/credentials-velero-minikube $VELERO_INSTALL_DIRECTORY"
    exit 1
  fi
  #echo "Velero 1.9 required. See comments in this file for setup process."
    cd $VELERO_INSTALL_DIRECTORY 
    ./velero install \
    --provider aws \
    --plugins velero/velero-plugin-for-aws:v1.2.1 \
    --bucket velero \
    --secret-file ./credentials-velero-minikube \
    --use-volume-snapshots=false \
    --backup-location-config "region=minio,s3ForcePathStyle=true,s3Url=http://$MINIKUBE_IP:9000"

fi

# TODO: check minikube IP matches ramen config, opt-out if it matches

# update ramen_config
cd "$WORKING_DIRECTORY"
sed s/minikube-ip-cluster1/"$MINIKUBE_IP"/g config/ramen_config_base.yaml > config/ramen_config.yaml
sed -i s/minikube-ip-cluster2/"$(minikube ip --profile cluster2)"/g config/ramen_config.yaml

# recipe
if [[ $(kubectl get crd/recipes.ramendr.openshift.io --no-headers | wc -l) -eq 0 ]]; then 
    echo "installing recipe CRD"

    kubectl apply -f https://raw.githubusercontent.com/RamenDR/recipe/main/config/crd/bases/ramendr.openshift.io_recipes.yaml
fi

# mc
if [[ $(command -v mc | wc -l) -eq 0 ]]; then
    echo "MC (Minio Client) is required. Download here: https://github.com/minio/mc#binary-download"
fi

# mc aliases
mc alias set minio-cluster1 http://"$(minikube ip --profile cluster1)":30000 minio minio123
mc alias set minio-cluster2 http://"$(minikube ip --profile cluster2)":30000 minio minio123

# mc buckets
if [[ $(mc ls minio-cluster1 | grep velero -c) -eq 0 ]]; then
  mc mb minio-cluster1/velero
fi

if [[ $(mc ls minio-cluster2 | grep velero -c) -eq 0 ]]; then
  mc mb minio-cluster2/velero
fi

# backube
if [[ $(kubectl get crd | grep replicationdestinations.volsync.backube -c) -eq 0 ]]; then
    kubectl apply -f https://raw.githubusercontent.com/backube/volsync/117eec0a92e9b3eb0c042dc4d2bb1853ddcbe07d/config/crd/bases/volsync.backube_replicationdestinations.yaml
fi

if [[ $(kubectl get crd | grep replicationsources.volsync.backube -c) -eq 0 ]]; then
    kubectl apply -f https://raw.githubusercontent.com/backube/volsync/117eec0a92e9b3eb0c042dc4d2bb1853ddcbe07d/config/crd/bases/volsync.backube_replicationsources.yaml
fi 

# load ramen images, recipe images
cd ~/go/src/github.com/tjanssen3/ramen

if [[ $(kubectl get all -n ramen-system | wc -l) -gt 0 ]]; then
  make undeploy-dr-cluster
fi

cd "$WORKING_DIRECTORY"
bash scripts/reload_minikube_image.sh quay.io/ramendr/ramen-operator "$CLUSTER"
bash scripts/reload_minikube_image.sh quay.io/ramendr/ramen-dr-cluster-operator-bundle "$CLUSTER"
minikube image load controller:latest --profile="$CLUSTER"

