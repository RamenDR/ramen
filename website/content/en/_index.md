---
title: Ramen
---

<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->


{{< blocks/cover title="Kubernetes-native Disaster Recovery" image_anchor="top" height="full" >}}
<a class="btn btn-lg btn-primary me-3 mb-4" href="{{< relref "/docs/" >}}">
  Documentation <i class="fas fa-arrow-alt-circle-right ms-2"></i>
</a>
<a class="btn btn-lg btn-secondary me-3 mb-4" href="https://github.com/RamenDR/ramen">
  GitHub <i class="fab fa-github ms-2"></i>
</a>
<p class="lead mt-5">Orchestrate failover and relocation of Kubernetes workloads and their data across clusters.</p>
{{< blocks/link-down color="info" >}}
{{< /blocks/cover >}}

{{% blocks/lead color="primary" %}}
Ramen is an [open-cluster-management (OCM)](https://open-cluster-management.io/)
placement extension that provides **Kubernetes-native Disaster Recovery** for
stateful workloads across a pair of managed clusters — handling planned
**relocation** and unplanned **failover** of both workloads and their
persistent data.
{{% /blocks/lead %}}

{{% blocks/section color="dark" type="row" %}}
{{% blocks/feature icon="fa-copy" title="Data replication" %}}
Storage-vendor-assisted replication via the CSI-addons
`Volume[Group]Replication` APIs, or VolSync-based rsync replication for
snapshot-capable storage.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-random" title="Failover & relocate" %}}
Planned relocation for maintenance and failback, and unplanned failover to a
peer cluster after cluster loss.
{{% /blocks/feature %}}

{{% blocks/feature icon="fab fa-github" title="Open source" url="https://github.com/RamenDR/ramen" %}}
Apache-2.0 licensed. Contributions, issues and discussions welcome on GitHub.
{{% /blocks/feature %}}
{{% /blocks/section %}}
