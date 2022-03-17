#!/bin/sh
# shellcheck disable=2086
set -x
set -e
set -- $1 1 8 1
if command -v $1/velero; then
	IFS=. read -r x y z <<-a
	$(velero version --client-only|cut -zf2|cut -dv -f2)
	a
	if test $x -gt $2 ||\
		{ test $x -eq $2 && test $y -gt $3; } ||\
		{ test $x -eq $2 && test $y -eq $3 && test $z -ge $4; }
	then
		exit
	fi
fi
set -- $1 v$2.$3.$4
set -- $1 $2 velero-$2-linux-amd64
curl -sSL https://github.com/vmware-tanzu/velero/releases/download/$2/$3.tar.gz|tar -xzC$1 --strip-components 1 $3/velero
