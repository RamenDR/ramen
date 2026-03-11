<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Metrics

Metrics are collected using Prometheus, and registered with its global
metrics registry in each controller. There are two ways where you can
look at the metrics in ramen. One way is use prometheus stack(recommended)
and the other way is to use curl or postman. More details on
each of these in the below sections.

More information on metrics is [here](https://book.kubebuilder.io/reference/metrics.html)

## 1. Using Prometheus Stack

We recommend to use [kube-prometheus](https://github.com/prometheus-operator/kube-prometheus).
Installing this will give you a containerized stack that
includes Prometheus, AlertManager and Grafana.

For more detailed information and querying,
consider using the Prometheus Operator, but in summary:

### Setup

Follow the next steps before configuring ramen.

#### Installing kube-prometheus

Quickstart instructions are [here](https://github.com/prometheus-operator/kube-prometheus#quickstart).

#### Grant permission for prometheus to scrape metrics

Go to `ramen/config/hub/default/k8s/kustomization.yaml`
and uncomment `../../../prometheus`
and `metrics_role_binding.yaml` under `Kustomization` section.
Next is to install and configure ramen.

#### Dashboard Access

[Accessing Graphical User Interfaces](https://github.com/prometheus-operator/kube-prometheus/blob/main/docs/access-ui.md#access-uis)

## 2. Basic testing (no Prometheus required)

If running from minikube or a container, expose the port using `port-forward`
on the hub. The endpoint exposed is `localhost:8443/metrics`. The metrics
endpoint is protected by controller-runtime built-in auth;
use a service account token when scraping.

### Steps

**Note:** All commands should be run against the hub cluster.

#### 1. Create a ServiceAccount for metrics access

Create a ServiceAccount and bind it to the `ramen-hub-metrics-reader`
ClusterRole to access the metrics endpoint.

Create a file named `metrics-reader-sa.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: metrics-reader-sa
  namespace: ramen-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: metrics-reader-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ramen-hub-metrics-reader
subjects:
- kind: ServiceAccount
  name: metrics-reader-sa
  namespace: ramen-system
```

Apply the manifest:

```bash
kubectl apply -f metrics-reader-sa.yaml
```

Verify the ServiceAccount was created:

```bash
kubectl get serviceaccount metrics-reader-sa -n ramen-system
```

#### 2. Port-forward

Start port-forwarding to the Ramen operator:

```bash
kubectl port-forward -n ramen-system deployment/ramen-hub-operator 8443:8443
```

#### 3. Get a token

In another terminal, generate a token for the ServiceAccount:

```bash
export TOKEN=$(kubectl create token metrics-reader-sa -n ramen-system)
```

#### 4. Call the metrics endpoint

Call the metrics endpoint using the token:

```bash
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:8443/metrics
```

If curl can connect, search for your metrics in the output.

### Metrics List

All metrics are prefixed with `ramen_`. This makes them easier to find.

To get the list of all the Ramen metrics available and their descriptions,
run the Ramen code, then run this command (requires `$TOKEN` from the steps above):

```bash
curl -k -s -H "Authorization: Bearer $TOKEN" https://localhost:8443/metrics \
  | grep "# HELP ramen_"
```
