#!/bin/bash
kubectl apply -f config/ramen_secret_minio.yaml 

kubectl apply -f config/ramen_config.yaml

kubectl apply -f protect/vrg_busybox_primary.yaml

kubectl apply -f protect/recipe_busybox.yaml
