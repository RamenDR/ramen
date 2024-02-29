#!/bin/bash
set -e

if ! groups "$USER" | grep -qw libvirt; then
  echo "Error: User is not part of the libvirt group."
  exit 1
fi

commands=("minikube" "kubectl" "clusteradm" "subctl" "velero" "helm" "virtctl"
"virt-host-validate")

for cmd in "${commands[@]}"; do
  if ! command -v "$cmd" &> /dev/null; then
    echo "Error: $cmd could not be found in PATH."
    exit 1
  fi
done

if ! virt-host-validate qemu -q; then
  echo "Error: 'virt-host-validate qemu' did not return 0."
  exit 1
fi

echo "All prerequisites met for drenv"
