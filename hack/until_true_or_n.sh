#!/bin/sh
# shellcheck disable=2086
until_true_or_n()
{
	case ${-} in *x*) { set +x; } 2>/dev/null; x='unset -v x; set -x';; esac
	n=${1}
	shift
	date
	until test ${n} -eq 0 || "${@}"
	do
		sleep 1
		n=$((n-1))
	done
	date
	unset -v n
	eval ${x}
	"${@}"
}
