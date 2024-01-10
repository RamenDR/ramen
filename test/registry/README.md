# Using local registry for minikube clusters

## Initial setup

1. Install podman

   ```
   sudo dnf install podman
   ```

1. Run the registry container

   ```
   podman run --name registry \
       --publish 5000:5000 \
       --volume registry:/var/lib/registry:Z \
       --detach \
       --replace \
       docker.io/library/registry:latest
   ```

   Use `--replace` to replace an existing container, typically left
   after reboot the host.

1. Allow access to port 5000 in the libvirt zone

   ```
   sudo firewall-cmd --zone=libvirt --add-port=5000/tcp --permanent
   sudo firewall-cmd --reload
   ```

1. Configure podman to allow insecure access

   ```
   sudo cp host.minikube.internal.conf /etc/containers/registries.conf.d/
   ```

1. Testing the registry

   ```
   $ curl host.minikube.internal:5000/v2/_catalog
   {}
   ```

## Pushing to the local registry

1. Pull the image from a remote registry

   ```
   podman pull quay.io/nirsof/cirros:0.6.2-1
   ```

1. Push to the local registry

   ```
   podman push quay.io/nirsof/cirros:0.6.2-1 host.minikube.internal:5000/nirsof/cirros:0.6.2-1
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
      url: "docker://host.minikube.internal:5000/nirsof/cirros:0.6.2-1"
```
