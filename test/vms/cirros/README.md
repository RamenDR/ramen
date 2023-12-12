# Cirros test VM

This directory provides scripts and container configuration for creating
a container with cirros vm disk image. This VM can be use to test
KubeVirt DR integration.

## Requirements

For Fedora install these packages:

```
sudo dnf install \
    cloud-utils \
    guestfs-tools \
    podman \
    qemu \
    qemu-img \
```

## Configuration

To push the image to your private registry or modify other setting use
the environment variables:

```
export REPOSITORY=my-quay-user
```

See the Makefile for available variables.

## Create VM image

```
make
```

This builds the image `quay.io/my-repo/cirros:latest`.

## Test the VM image

```
make vm
```

This create a test image on top of the cirros vm disk, and starts
qemu-kvm and virt-viewer. You can log in and inspect the VM contents.

You can stop the VM and start it again using `make vm` to test shutdown
and startup behavior.

## Pushing image to your repo

```
make push
```

This pushes the built container to your repo.

## Cleaning up

```
make clean
```
