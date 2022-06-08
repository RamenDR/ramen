#!/bin/sh
# shellcheck disable=2086
minikube_minio_url()
{
	minikube --profile $1 -n minio service --url minio
}
minikube_unset()
{
	unset -f minikube_unset
	unset -f minikube_minio_url
}
