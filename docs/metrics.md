<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Metrics

Metrics are collected using Prometheus, and registered with its global metrics
registry in each controller. The endpoint exposed is `localhost:9289/metrics`.
If running from minikube or a container, expose the port using `port-forward`
on the hub:

```bash
kubectl port-forward -n ramen-system \
  service/ramen-hub-operator-metrics-service 9289:9289
```

## Basic testing (no Prometheus required)

Verify that the metrics endpoint is exposed with curl:
`curl http://localhost:9289/metrics`

If curl can connect, search for your metrics in the output.

## Metrics List

All metrics are prefixed with `ramen_`. This makes them easier to find.

To get the list of all the Ramen metrics available and their descriptions,
run the Ramen code, then run this command:
`curl http://localhost:9289/metrics -s | grep "# HELP ramen_"`

## Prometheus Stack

For more detailed information and querying, consider using the Prometheus
Operator
[kube-prometheus](https://github.com/prometheus-operator/kube-prometheus).
Installing this will give you a containerized stack that
includes Prometheus, AlertManager and Grafana. Quickstart instructions are
[here](https://github.com/prometheus-operator/kube-prometheus#quickstart),
but in summary:

Setup and create:

```bash
git clone https://github.com/prometheus-operator/kube-prometheus.git

cd kube-prometheus

# install CRDs
kubectl create -f manifests/setup

# install resources
kubectl create -f manifests/

# verify everything is running
kubectl get all -n monitoring
```

Delete stack:

```bash
kubectl delete --ignore-not-found=true -f manifests/ -f manifests/setup
```

### Dashboard Access

To access GUI dashboards for each component, use `port-forward`:

* Prometheus:
 `$ kubectl --namespace monitoring port-forward svc/prometheus-k8s 9090`
* Grafana: `$ kubectl --namespace monitoring port-forward svc/grafana 3000`
* Alert Manager:
 `$ kubectl --namespace monitoring port-forward svc/alertmanager-main 9093`

Then navigate to the appropriate port on localhost, e.g.
[http://localhost:9090](http://localhost:9090) for Prometheus.

Ramen metrics can be found in namespace "ramen-system"; e.g. PromQL query
`{namespace="ramen-system"}`
