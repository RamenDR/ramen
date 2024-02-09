# Kube Resource Protection / Recipe Installation

## Requirements

To enable Kube Resource Protection, a system must have access to:

1. S3 store (e.g. Minio/Noobaa) installed and configured
1. S3 store credentials
1. Velero/OADP installed and configured
1. Velero credentials

## Setup

### Ramen ConfigMap

The Ramen ConfigMap is stored in the `ramen-system` namespace (or `openshift-dr-system`
on OpenShift) with name `ramen-dr-cluster-operator-config`. This ConfigMap contains
s3 profile information like this:

```yaml
data:
  ramen_manager_config.yaml: |
    s3StoreProfiles:
    - s3ProfileName: minio-cluster1
      s3Bucket: velero
      s3CompatibleEndpoint: http://192.168.39.136:30000
      s3Region: us-east-1
      s3SecretRef:
        name: minio-s3
      VeleroNamespaceSecretKeyRef:
        key: cloud-credentials
        name: cloud
```

For every s3 profile, two Secrets will need to be created: one with s3 credentials,
one with Velero/OADP credentials. This means for two S3 profiles, four Secrets are
required, and so on.

### S3 Secret setup

How to set up the S3 Secret:

1. Name: needs to match Ramen ConfigMap's s3StoreProfile `s3SecretRef.name`. From
  the example above, this Secret is named `minio-s3`.
1. Namespace: the Secret needs to be in the same namespace as the Ramen operator.
  This will be `ramen-system` or `openshift-dr-system` in most cases. If there's
  any doubt about this, search for the deployment called `ramen-dr-cluster-operator`,
  and use that Namespace.
1. Contents: `data` should contain two keys: `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`,
  both of which are base64 encoded. If the s3 login is `minio`, the base64 encoding
  is `bWluaW8=`. If the s3 password is `minio123`, the base64 encoding is `bWluaW8xMjM=`.
  Here is an example file showing all of these in use:

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: minio-s3  # match to Ramen ConfigMap's s3Profile.s3SecretRef.name
  namespace: ramen-system  # must be in the same namespace as Ramen operator
type: Opaque
data:
  AWS_ACCESS_KEY_ID: bWluaW8=
  AWS_SECRET_ACCESS_KEY: bWluaW8xMjM=
```

### Velero Secret setup

How to set up the Velero secret:

1. Name: needs to match Ramen ConfigMap's `veleroNamespaceSecretKeyRef.name`. In
  the example above, this Secret is named `cloud`.
1. Namespace: the Secret needs to be in the same namespace as the Ramen operator.
  This will be `ramen-system` or `openshift-dr-system` in most cases.
1. Contents: data should contain one key, and that name should match the Ramen ConfigMap's
  `veleroNamespaceSecretKeyRef.key` value. In the example above, this is `cloud-credentials`.
  The contents of this key are base64 encoded into a single value, but the format
  of the plain text is:

Filename=`cloud-credentials`

```
[default]
aws_access_key_id = minioUser
aws_secret_access_key = minioLogin
```

To create a Secret in the Ramen namespace with `cloud-credentials` as the key,
use this command:

`kubectl create secret generic cloud --from-file=cloud-credentials -n ramen-system`

Using `kubectl get secret/cloud -n ramen-system -o yaml` to examine the contents:

```yaml
apiVersion: v1
data:
  cloud_credentials: W2RlZmF1bHRdCmF3c19hY2Nlc3Nfa2V5X2lkID0gbWluaW9Vc2VyCmF3c19zZWNyZXRfYWNjZXNzX2tleSA9IG1pbmlvTG9naW4KCg==
kind: Secret
metadata:
  creationTimestamp: "2023-06-12T18:39:12Z"
  name: cloud  # match to Ramen ConfigMap's s3Profile.veleroNamespaceSecretKeyRef.name
  namespace: ramen-system  # must be in the same namespace as Ramen operator
  resourceVersion: "23042901"
  uid: 860ce0ae-d428-4a84-81d8-349544f92049
type: Opaque
```

Now Ramen should be able to access Velero.

### Using an alternate Velero namespace

By default, Ramen expects Velero to be installed in the `velero` namespace. If
Velero is installed in another namespace, it is necessary to define this in the
Ramen ConfigMap in the `kubeObjectProtection.veleroNamespaceName` field. For a
system that uses OADP installed in the `openshift-adp` Namespace, the Ramen ConfigMap
setting would look like this:

```yaml
data:
  ramen_manager_config.yaml: |
    kubeObjectProtection:
      veleroNamespaceName: openshift-adp
```
