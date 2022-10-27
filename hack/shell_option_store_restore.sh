# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck shell=sh disable=2086
shell_option_store()
{
	case $- in
		*$2*)
		eval $1=set\\ -$2
		;;
	*)
		eval $1=set\\ +$2
		;;
	esac
} 2>/dev/null
shell_option_disable()
{
	case $- in
	*$2*)
		set +$2
		eval $1=set\\ -$2
		;;
	*)
		unset -v $1
		;;
	esac
} 2>/dev/null
shell_option_enable()
{
	case $- in
	*$2*)
		unset -v $1
		;;
	*)
		set -$2
		eval $1=set\\ +$2
		;;
	esac
} 2>/dev/null
shell_option_restore()
{
	eval \$$1
	unset -v $1
} 2>/dev/null
