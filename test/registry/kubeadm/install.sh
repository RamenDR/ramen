#!/bin/sh

set -eu

release="$(curl -fsSL https://dl.k8s.io/release/stable.txt)"
arch="$(uname -m | sed -e s/aarch64/arm64/ -e s/x86_64/amd64/)"

curl -fsSLO "https://dl.k8s.io/release/${release}/bin/linux/${arch}/kubeadm"
chmod +x kubeadm
