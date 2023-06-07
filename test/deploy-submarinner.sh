#!/bin/bash -e

echo "## Deploying broker on cluster dr1"
subctl deploy-broker --context dr1 --globalnet

echo "## Joining cluster dr1"
subctl join broker-info.subm --context dr1 --clusterid dr1 --cable-driver vxlan

echo "## Joining cluster dr2"
subctl join broker-info.subm --context dr2 --clusterid dr2 --cable-driver vxlan
