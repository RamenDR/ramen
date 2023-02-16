#!/bin/bash
# usage: reload_minikube_image.sh quay.io/ramendr/ramen-operator cluster2

set -e
set -x

IMAGE=${1:-quay.io/ramendr/ramen-operator}
CONTEXT=${2:-cluster1}

# if most current image already loaded, exit
CURRENT_HOST_IMAGE=$(docker images | grep "$IMAGE" | awk '{print $3}')
CURRENT_MINIKUBE_IMAGE=$(minikube ssh --profile "$CONTEXT" "docker images" | grep "$IMAGE" | awk '{print $3}')

if [[ "$CURRENT_HOST_IMAGE" == "$CURRENT_MINIKUBE_IMAGE" ]]; then
  echo "current image already loaded"
  exit 0
fi

# clean up existing image
#minikube ssh --profile=$CONTEXT "docker images" | grep $IMAGE
if [[ $(CURRENT_MINIKUBE_IMAGE | wc -l) -gt 0 ]]; then
    echo "cleanup existing image"
    minikube ssh --profile="$CONTEXT" "docker image rm $IMAGE"
fi

if [[ -e ~/.minikube/cache/images/"${IMAGE}" ]]; then
    echo "removing cached image"
    rm ~/.minikube/cache/images/"${IMAGE}"
fi 

if [[ -e "$HOME/.minikube/cache/images/${IMAGE}_latest" ]]; then
    echo "removing latest cached image"
    rm "$HOME/.minikube/cache/images/${IMAGE}_latest"
fi 

echo "loading new image"
minikube image load "$IMAGE" --profile="$CONTEXT"

echo "loaded image:"
minikube ssh --profile="$CONTEXT" "docker images | grep $IMAGE"
