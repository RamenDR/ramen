<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Local registry for minikube clusters

This directory contains configuration for running a local container registry.
The registry can be used to push custom images and consume them in minikube
clusters.

The local registry is optional and managed manually by the developer. It is
not related to the registry cache (see `test/registry-cache/`).

## Port assignment

| Port | Address | Purpose |
|------|---------|---------|
| 5050 | host.minikube.internal:5050 | Local registry (push/pull) |

## Initial setup - Linux

1. Install podman

   ```
   sudo dnf install podman
   ```

1. Allow access to registry port in the libvirt zone

   ```
   sudo cp linux/registry.xml /etc/firewalld/services/
   sudo firewall-cmd --reload
   sudo firewall-cmd --zone=libvirt --add-service=registry --permanent
   sudo firewall-cmd --reload
   ```

   This allows minikube VMs to access the local registry container running
   on the host.

1. Configure podman to allow insecure access

   ```
   sudo cp host.minikube.internal.conf /etc/containers/registries.conf.d/
   ```

1. Run the local registry container

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
   cat host.minikube.internal.conf | podman machine ssh \
       "sudo tee /etc/containers/registries.conf.d/host.minikube.internal.conf > /dev/null"
   ```

1. Restart podman machine to apply the changes

   ```
   podman machine stop
   podman machine start
   ```

1. Run the local registry container

   ```
   ./start
   ```

## Testing the local registry

```
$ curl http://host.minikube.internal:5050/v2/_catalog
{"repositories":[]}
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

```yaml
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

## Managing the local registry container

To stop the registry container run:

```
podman stop registry
```

To start the registry container (e.g. after reboot) run:

```
podman start registry
```

To remove the registry and delete the stored images:

```
podman rm registry
podman volume rm registry
```
