#!/bin/bash

echo "Running tests..."

cd ./e2e/
go test -kubeconfig-c1 ~/.config/drenv/rdr-rdr/kubeconfigs/rdr-dr1  -kubeconfig-c2 ~/.config/drenv/rdr-rdr/kubeconfigs/rdr-dr2 -kubeconfig-hub ~/.config/drenv/rdr-rdr/kubeconfigs/rdr-hub -v

exit 0