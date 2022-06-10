# VRG Type Sequence

## Overview

RamenDR can use Velero to capture Kubernetes object information. By default,
Velero will back up all object types in arbitrary order. However, many
applications have internal dependencies that require specific ordering by
object type. The VRG Type Sequencer attempts to address this deficiency by
taking multiple partial backups, with the order defined a the user.

## Example Use Case: Backup

### Backup Overview

Take an example Backup that requires the following sequence:

1) Deployments first
2) Any resource types that match suffix *.cpd.ibm.com
3) Secrets and ConfigMaps after that (order unimportant)
4) Anything else after that

### YAML example

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: volumereplicationgroup-sample
spec:
  ...

  # Type Sequence section
  KubeObjectProtection:
    ResourceBackupOrder:
      - ["Deployments"]
      - ["*.cpd.ibm.com"]
      - ["ConfigMap",
        "Secret"]
      - [".*"]
```

## Example Use Case: Restore

### Restore Overview

Take an example Restore that requires the following sequence:

1) Secrets and ConfigMaps before anything else (but in any order)
2) Any resource matching the suffix cpd.ibm.com
3) Any resource that isn't a Deployment
4) Anything else

### YAML example

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: volumereplicationgroup-sample
spec:
  ...

  # Type Sequence section
  KubeObjectProtection:
    ResourceRestoreOrder:
      - ["Secret",
        "ConfigMap"]
      - ["*cpd.ibm.com"]
      - ["!Deployments"]
      - [".*"]
```

## Technical info

This will take several Velero backups in a sequence. The S3 contents are
organized as follows for the example above:

```bash
/s3bucket
    /bucketPrefix
        /namespaceName
            /vrgName
                /backupTypeSequence
                    /0
                        /v1.Deployments
                    /1
                        /v1alpha1.custom1.cpd.ibm.com
                        /v1alpha1.custom2.cpd.ibm.com
                        /v1alpha1.custom3.cpd.ibm.com
                    /2
                        /v1.ConfigMap
                        /v1.Secret
                    /3
                        / # everything else here
```

## Design points

1. Apps that span multiple namespaces: VRG backup type sequencing is limited
   to the same namespace as the VRG itself. If an application spans multiple
   namespaces, then a type sequence should be specified on each VRG in each
   namespace.
