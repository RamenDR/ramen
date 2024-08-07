#!/bin/bash
set -e

os=$(uname -s)

if [[ "$os" == "Linux" ]]
then
  if ! groups "$USER" | grep -qw libvirt; then
    echo "Error: User is not part of the libvirt group."
    exit 1
  fi
fi

commands=("minikube" "kubectl" "clusteradm" "subctl" "velero" "helm" "virtctl"
"kustomize" "mc")

linux_only_commands=("virt-host-validate")

for cmd in "${commands[@]}"; do
  if ! command -v "$cmd" &> /dev/null; then
    echo "Error: $cmd could not be found in PATH."
    exit 1
  fi
done


if [[ "$os" == "Linux" ]]
then
  for cmd in "${linux_only_commands[@]}"; do
    if ! command -v "$cmd" &> /dev/null; then
      echo "Error: $cmd could not be found in PATH."
      exit 1
    fi
  done
fi

if [[ "$os" == "Linux" ]]
then
  if ! virt-host-validate qemu -q; then
    echo "Error: 'virt-host-validate qemu' did not return 0."
    exit 1
  fi
fi

echo "All prerequisites met for drenv"
