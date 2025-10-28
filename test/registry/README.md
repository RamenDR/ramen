<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Using local registry for minikube clusters

## Initial setup - Linux

1. Install podman

   ```
   sudo dnf install podman
   ```

1. Allow access to port 5000 in the libvirt zone

   ```
   sudo firewall-cmd --zone=libvirt --add-port=5000/tcp --permanent
   sudo firewall-cmd --reload
   ```

1. Configure podman to allow insecure access

   ```
   sudo cp host.minikube.internal.conf /etc/containers/registries.conf.d/
   ```

1. Run the registry container

   ```
   ./start
   ```

   To run the registry as system service on Linux see
   [systemd service](#systemd-service).

### Systemd service

To create a registry service running at boot, install the provided
systemd units and start the service.

```
sudo cp systemd/registry.* /etc/containers/systemd/
sudo systemctl daemon-reload
sudo systemctl start registry.service
```

> [!NOTE]
> The service does not need to be enabled.

## Initial setup - macOS

1. Install podman

   ```
   brew install podman
   ```

1. Start podman machine

   ```
   podman machine start
   ```

1. Allow podman to accept incoming connections

   ```
   version="$(podman version --format '{{.Version}}')"
   gvproxy="/opt/homebrew/Cellar/podman/$version/libexec/podman/gvproxy"
   sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add "$gvproxy"
   sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblock "$gvproxy"
   ```

1. Configure podman to allow insecure local registry

   ```
   cat registry/host.lima.internal.conf | podman machine ssh \
       "sudo tee /etc/containers/registries.conf.d/host.lima.internal.conf > /dev/null"
   ```

1. Restart podman machine to apply the changes

   ```
   podman machine stop
   podman machine start
   ```

1. Run the registry container

   ```
   ./start
   ```

### Mirroring k8s images to the local registry

To mirror k8s images to the local registry run:

```
./mirror-k8s
```

This pulls the images used by `kubeadm init` to podman, and push them to the
local registry. You must run this before start the drenv with the
`--local-registry` option.

## Testing the registry

```
$ curl host.minikube.internal:5050/v2/_catalog
{}
```

## Pushing to the local registry

1. Pull the image from a remote registry

   ```
   podman pull quay.io/nirsof/cirros:0.6.2-1
   ```

1. Push to the local registry

   ```
   podman push quay.io/nirsof/cirros:0.6.2-1 localhost:5050/nirsof/cirros:0.6.2-1
   ```

## Using images from the local registry

Example source.yaml:

```
---
apiVersion: cdi.kubevirt.io/v1beta1
kind: VolumeImportSource
metadata:
  name: cirros-source
spec:
  source:
    registry:
      url: "docker://host.minikube.internal:5050/nirsof/cirros:0.6.2-1"
```

> [!NOTE]
> For macOS the registry address is `host.lima.internal:5050` instead of
> `host.minikube.internal`.

## Managing the registry container

To stop the registry container run:

```
podman stop registry
```

To start the registry container (e.g. after reboot) run:

```
podman start registry
```

To remove the registry and delete the cached images:

```
podman rm registry
podman volume rm registry
```
