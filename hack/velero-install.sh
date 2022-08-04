#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2046,2086
set -ex
set -- ${1:?} $(dirname $0)
set -- $1 $2 $(cd $2/..;go list -m github.com/vmware-tanzu/velero|cut -d\  -f2 -|cut -c2- -|tr . \ )
if command -v $1/velero; then
	IFS=. read -r x y z <<-a
	$(velero version --client-only|cut -zf2|cut -dv -f2)
	a
	if test $x -gt $3 ||\
	{ test $x -eq $3 &&\
		{ test $y -gt $4 ||\
		{ test $y -eq $4 &&\
			 test $z -ge $5;};};}
	then
		: exit
	fi
fi
set -- $1 $2 $3.$4 v$3.$4.$5
set -- $1 $2 $3 $4 velero-$4-linux-amd64
curl --silent --show-error --location https://github.com/vmware-tanzu/velero/releases/download/$4/$5.tar.gz|tar -xzC$1 --strip-components 1 $5/velero
#wget --quiet --directory-prefix $2/test\
curl --silent --show-error --location --remote-name-all --remote-time --output-dir $2/test\
	https://raw.githubusercontent.com/vmware-tanzu/velero/release-$3/config/crd/v1/bases/velero.io_backups.yaml\
	https://raw.githubusercontent.com/vmware-tanzu/velero/release-$3/config/crd/v1/bases/velero.io_backupstoragelocations.yaml\
	https://raw.githubusercontent.com/vmware-tanzu/velero/release-$3/config/crd/v1/bases/velero.io_restores.yaml\

