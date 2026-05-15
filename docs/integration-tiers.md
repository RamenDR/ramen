<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Storage Vendor Integration Tiers

Ramen defines three tiers of integration for third-party storage vendors.
Higher tiers require more implementation effort but deliver a
significantly better user experience.

## Bronze — No Vendor Changes Required

Bronze integration requires **no changes** to the storage vendor's
product. Ramen provides DR orchestration around any storage backend that
can replicate data between two sites, even when Ramen has no direct
interface to that backend.

**Trade-offs:**

- Initial installation, site-to-site connectivity, and replication
  configuration are **manual** and happen **outside** of Ramen and
  OpenShift.
- Failover, failback, and relocate operations require **manual steps**
  by the administrator beyond what Ramen automates.
- Ramen has **no visibility** into replication status — it cannot
  monitor replication health or alert when the desired RPO is not being
  met.

**Example:** IBM Fusion Access, which is itself vendor-agnostic and
therefore cannot implement any higher tier.

See [Bronze Tier Details](integration-tier-bronze.md) for prerequisites,
failover flow, and failback flow.

## Silver — CSI Driver Implements Replication CRDs

Silver integration requires the vendor to implement the
[csi-addons](https://github.com/csi-addons/kubernetes-csi-addons)
replication and network fencing CRDs in their own CSI driver. No changes
to Ramen itself are needed — all implementation happens within the
vendor's CSI driver.

**Replication CRDs:**

- `VolumeReplication` / `VolumeReplicationClass`
- `VolumeGroupReplication` / `VolumeGroupReplicationClass` /
  `VolumeGroupReplicationContent`

**Network Fencing CRDs:**

- `NetworkFence` / `NetworkFenceClass` — used during failover to
  fence the failed cluster so it cannot continue writing to storage

The Class resources define parameters matched by provisioner name and
scheduling interval. The vendor's CSI driver must respond to the
Replication and NetworkFence resources that Ramen creates.

Initial site-to-site setup and replication configuration remain manual
and happen outside of Ramen. However, once configured:

- Ramen **automatically controls promotion and demotion** of replicated
  volumes during failover, failback, and relocate — significantly
  reducing the operational burden during these stressful events.
- Ramen **monitors replication status** through the status fields of the
  replication CRs, including RPO alerting when sync lag exceeds the
  policy interval, replication health (degraded/resync state), workload
  protection status, and split-brain detection.

**Current adopters:** IBM FlashSystem are planning to implement
this tier for their storage product.

## Gold — Full Upstream Contribution

Gold integration includes everything in Silver, plus the vendor
**contributes to the Ramen upstream project** to add automation for
their storage backend. This enables:

- **Automated initial setup** — storage instance discovery, site
  linkage, and replication configuration are handled by Ramen.
- **Fully automated DR lifecycle** — every step from initial
  configuration through failover, failback, and relocate is automated
  with no manual intervention.

**Current adopters:** Red Hat ODF and IBM Fusion are the only storage
offerings that implement at this level.

## Summary

| Capability | Bronze | Silver | Gold |
|---|---|---|---|
| Vendor effort required | ★☆☆☆☆ None | ★★★☆☆ CSI driver | ★★★★★ CSI driver + Ramen contribution |
| Initial setup automation | ★☆☆☆☆ Manual | ★☆☆☆☆ Manual | ★★★★★ Automated |
| Failover/failback automation | ★★☆☆☆ Manual patches | ★★★★☆ Automated | ★★★★★ Fully automated |
| Replication monitoring¹ | ☆☆☆☆☆ None | ★★★★★ Full | ★★★★★ Full |
| Split-brain prevention | ☆☆☆☆☆ Manual | ★★★★☆ NetworkFence | ★★★★★ NetworkFence |
| User experience | ★★☆☆☆ | ★★★★☆ | ★★★★★ |
| Recovery time (RTO) | ★★☆☆☆ Slow (manual steps) | ★★★★☆ Fast | ★★★★★ Fastest |

¹ **Replication monitoring** includes: RPO alerting (warning at 2x,
critical at 3x the policy interval), replication health tracking
(degraded and resync state), workload protection status (protected vs.
unprotected), and split-brain detection. All monitoring data is exposed
as Prometheus metrics and Kubernetes conditions/events.
