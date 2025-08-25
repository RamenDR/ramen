<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Kubernetes Resource Protection

## Overview

Kubernetes applications rely on Kubernetes API resources to function within a
cluster.  For example, deployments can specify the program components of an
application, configmaps modify how a user wants to run an application, and
custom resources can control the overall operation of the application through
application operators. Protecting Kubernetes resources against data loss and
disasters in an application independent manner, greatly simplifies Kubernetes
application development for applications that require those data protection
services.

## Selective Protection and Recovery of Kubernetes Resources for Disaster Recovery

To minimize recovery time, an application can pre-deploy some of its Kubernetes
resources on the recovery cluster.  Such an application will want a method to
avoid duplicate recovery of its Kubernetes resources to ensure the benefits of
pre-deployment.  Some Kubernetes resources are created dynamically by Kubernetes
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
capturing and restoring resources in a prescribed order using [Recipe](recipe.md).

## An Example Kubernetes Resource Protection Specification

```yaml
    spec:
      kubeObjectProtection:
        recipeRef:
          namespace: my-ns
          name: my-recipe
```
