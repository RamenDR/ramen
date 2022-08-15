# shellcheck shell=sh
trap 'set -- $?; trap - EXIT; eval $exit_stack; echo exit status: $1' EXIT
trap 'trap - EXIT; eval $exit_stack' EXIT
trap 'trap - ABRT' ABRT
trap 'trap - QUIT' QUIT
trap 'trap - TERM' TERM
trap 'trap - INT' INT
trap 'trap - HUP' HUP
exit_stack_push()
{
	exit_stack=$*\;$exit_stack
} 2>/dev/null
exit_stack_push unset -v exit_stack
exit_stack_push unset -f exit_stack_push
exit_stack_pop()
{
	{ set +x; } 2>/dev/null
	IFS=\; read -r x exit_stack <<-a
	$exit_stack
	a
	eval set -x; $x
	{ unset -v x; } 2>/dev/null
}
exit_stack_push unset -f exit_stack_pop
