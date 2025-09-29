# kubeadm container image

This image can be used to run kubeadm on macOS to get list the images
needed in `kubeadm init`.

## Build

```console
podman build -t kubeadm:latest registry/kubeadm
```

## Listing images for stable version

```console
$ podman run --rm kubeadm:latest config images list
registry.k8s.io/kube-apiserver:v1.34.1
registry.k8s.io/kube-controller-manager:v1.34.1
registry.k8s.io/kube-scheduler:v1.34.1
registry.k8s.io/kube-proxy:v1.34.1
registry.k8s.io/coredns/coredns:v1.12.1
registry.k8s.io/pause:3.10.1
registry.k8s.io/etcd:3.6.4-0
```

## Listing images for specific version

```
% podman run --rm kubeadm:latest config images list --kubernetes-version v1.34.0
registry.k8s.io/kube-apiserver:v1.34.0
registry.k8s.io/kube-controller-manager:v1.34.0
registry.k8s.io/kube-scheduler:v1.34.0
registry.k8s.io/kube-proxy:v1.34.0
registry.k8s.io/coredns/coredns:v1.12.1
registry.k8s.io/pause:3.10.1
registry.k8s.io/etcd:3.6.4-0
```
