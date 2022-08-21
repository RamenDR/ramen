# Kubernetes Resource Protection

## Overview

Kubernetes applications rely on Kubernetes API resources to function within a
cluster.  For example, deployments can specify the program components of an
application, configmaps modify how a user wants to run an application, and
custom resources can control the overall operation of the application through
application operators.  Protecting Kubernetes resources against data loss and
disasters in an application independent manner, greatly simplifies Kubernetes
application development for applications that require those data protection
services.

## Selective Protection and Recovery of Kubernetes Resources for Disaster Recovery

To minimize recovery time, an application can pre-deploy some of its Kubernetes
resources on the recovery cluster.  Such an application will want a method to
avoid duplicate recovery of its Kubernetes resources to ensure the benefits of
pre-deployment.  Some Kubernetes resrouces are created dynamically by Kubernetes
itself and an application does not need to preserve the history of those
resources, so protection is not required.  Kubernetes events are a prime example
of such resources.  In the case of events applications may need a method to
avoid protection and recovery all together.  Hence the Ramen Volume Replication
Group (VRG) custom resource for disaster recovery provides a flexible mechanism
to support these examples and has generalized the mechanism for other use cases.

The general technique of the selective protection and recovery mechanism is to
filter Kubernetes resources by kind and by label.  Resources can be selectively
protected and recovered by kind using an include and exclude mechanism provided
within the VRG.  In addition to include and exclude, the standard Kubernetes
label selector mechanism is used to protect specific resources.

## Kubernetes Resource Protection and Recovery Order

Kubernetes applications are supposed to be architected to run in a system where
a desired state is specified and over time that desired state is continuously
attempted, achieved, and maintained.  It is up to the application to deal with
asynchronous behavior required by this set and attempt, achieve, maintain
architecture.  However, in highly complex applications with stringent user
expectations the level of asynchrony can become unmanageable.  First, the scope
of asynchrony can break the ability of programmers to anticipate all sequences
of events.  Second, the scope of asynchrony can be the source of failed
dependencies resulting in backoff retry loops which can violate the application
Recovery Time Objective (RTO).  Restoring resources in a prescribed order can
avoid both of these problems.  So the Ramen VRG provides a mechanism to support
capturing and restoring resources in a prescribed order.

## An Example Kubernetes Resource Protection Specification

```yaml
    spec:
        kubeObjectProtection:
            # backup section
            captureInterval: 30m
            captureOrder:
                - name: config  # backup Names should be unique
                    includedResources: ["ConfigMap", "Secret"]
                - name: custom
                    includedResources: ["sample1.myapp.mycompany.com", "sample.myapp.mycompany.com", "sample3.myapp.mycompany.com"]
                    labelSelector: "myAppPersist"
                    # includeClusterResources: false # by default
                - name: deployments
                    includedResources: ["Deployment"]
                - name: everything
                    includeClusterResources: true
                    excludedResources: [""]  # include everything with no history, even resources in other backups
            # restore section
            recoverOrder:
                - backupName: config # API server required matching to backup struct
                    includedResources: ["ConfigMap", "Secret"]
                - backupName: custom
                    includedResources: ["sample1.myapp.mycompany.com", "sample2.myapp.mycompany.com", "sample3.myapp.mycompany.com"]
                    # labelSelector: "" # intentionally omitted - don't require label match
                    # includeClusterResources: false # by default
                - backupName: deployments
                    includedResources: ["Deployment"]
                - backupName: everything
                    includeClusterResources: true
                    excludedResources: ["ConfigMap", "Secret", "Deployment", "sample1.myapp.mycompany.com", "sample2.myapp.mycompany.com", "sample3.myapp.mycompany.com"]  # don't restore resources we've already restored
```

## Explanation of the Capture and Recovery Specifications in the VRG

The scope of a VRG disaster protection is a single Kubernetes namespace.  The
VRG protects persistent volumes associated with the namespace and optionally
protects Kubernetes resources in the namespace.  This documentation covers the
product previews for protecting Kuberentes resources so does not explain the
persistent volume disaster protection.

The VRG enables Kubernetes resources to be captured(backed up) and recovered as
part of disaster protection.  This is accomplished through the
kubeObjectProtection section of the VRG spec.  If kubeObjectProtection is not
included in a VRG, then Kubernetes resources are not protected as part of the
VRG disaster protection.

The kubeObjectProtection section contains two sub-sections, captureOrder and
recoverOrder.  This captureOrder section provides instructions on how to backup
a namespaces Kubernetes resources.  The recoverOrder section provides
instructions on how to recover a namespaces Kubernetes resources after a
disaster.  This implies that the backup and recover order can be different and
don't need to include all the same resources.  Each of the captureOrder and
recoverOrder sections contain a list of resource instructions.  Each item in the
list is acted upon even if it duplicates work done by other items in the list.
So care should be taken to avoid duplication within either the backup or recover
lists so that the best RPO and RTO are achieved.  The list items must meet the
following requirements.  If the requirements are not met, then the operation of
the lists is undefined.

1. The name of each item in the captureOrder list must be unique
1. The backupName of each item in the recoverOrder list much match a name in the
 recoverOrder list
1. A labelSelector in a list item only applies to that item in the list
1. If a list item contains multiple labelSelectors then any resource that
 matches either label selector is operated upon
1. includeClusterResources in a list item only applies to that item in the list
1. Each list item can contain either an includedResources section or an
 excludedResources section, but not both
