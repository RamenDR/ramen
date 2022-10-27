# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck shell=sh disable=2086
until_true_or_n()
{
	{ case ${-} in *x*) set +x; x='unset -v x; set -x';; esac; } 2>/dev/null
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
