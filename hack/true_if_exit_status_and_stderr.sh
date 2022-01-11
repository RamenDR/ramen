# shellcheck shell=sh
true_if_exit_status_and_stderr()
{
	exit_status_expected=$1
	stderr_expected=$2
	shift 2
	stderr_pipe_name1=/tmp/$$.err1 stderr_pipe_name2=/tmp/$$.err2
	rm -f $stderr_pipe_name1 $stderr_pipe_name2
	mkfifo -m o=rw $stderr_pipe_name1 $stderr_pipe_name2
	tee $stderr_pipe_name2 <$stderr_pipe_name1 >&2 &
	tee_pid=$!
	case $- in *e*) e=-e; set +e;; *) e= ; esac
	"$@" 2>$stderr_pipe_name1
	set "$e" -- $?
	unset -v e
	stderr=$(cat $stderr_pipe_name2)
	wait $tee_pid
	unset -v tee_pid
	rm -f $stderr_pipe_name1 $stderr_pipe_name2
	unset -v stderr_pipe_name1 stderr_pipe_name2
	if test "$1" -eq "$exit_status_expected" && test "$stderr" = "$stderr_expected"; then
		set -- 0
	fi
	unset -v stderr stderr_expected exit_status_expected
	return "$1"
}
