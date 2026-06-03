// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var linePrefix = regexp.MustCompile(`^\[\s*\d+\.\d{3}\] `)

type result struct {
	exit   int
	stdout string
	stderr string
}

func TestStdout(t *testing.T) {
	r := run(t, "./timestamp", "echo", "hello")
	checkExit(t, r.exit, 0)
	checkTimestampStream(t, r.stdout, "echo hello", "hello")
	checkTimestampStream(t, r.stderr)
}

func TestEmptyArgument(t *testing.T) {
	r := run(t, "./timestamp", "echo", "")
	checkExit(t, r.exit, 0)
	checkTimestampStream(t, r.stdout, `echo ""`, "")
	checkTimestampStream(t, r.stderr)
}

func TestQuotedCommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "apostrophe without space",
			args: []string{"it's"},
			want: `true "it's"`,
		},
		{
			name: "space only",
			args: []string{" "},
			want: `true " "`,
		},
		{
			name: "tab only",
			args: []string{"\t"},
			want: `true "\t"`,
		},
		{
			name: "tab in argument",
			args: []string{"a\tb"},
			want: `true "a\tb"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("./timestamp", append([]string{"true"}, tc.args...)...)
			r := runCmd(t, cmd)
			checkExit(t, r.exit, 0)
			checkTimestampStream(t, r.stdout, tc.want)
			checkTimestampStream(t, r.stderr)
		})
	}
}

func TestStderr(t *testing.T) {
	r := run(t, "./timestamp", "sh", "-c", "echo err >&2")
	checkExit(t, r.exit, 0)
	checkTimestampStream(t, r.stdout, `sh -c "echo err >&2"`)
	checkTimestampStream(t, r.stderr, "err")
}

func TestStdoutAndStderr(t *testing.T) {
	r := run(t, "./timestamp", "sh", "-c", "echo out; echo err >&2")
	checkExit(t, r.exit, 0)
	checkTimestampStream(t, r.stdout, `sh -c "echo out; echo err >&2"`, "out")
	checkTimestampStream(t, r.stderr, "err")
}

func TestExitFailure(t *testing.T) {
	r := run(t, "./timestamp", "false")
	checkExit(t, r.exit, 1)
	checkTimestampStream(t, r.stdout, "false")
	checkTimestampStream(t, r.stderr)
}

func TestNoCommand(t *testing.T) {
	r := run(t, "./timestamp")
	checkExit(t, r.exit, 1)
	checkTimestampStream(t, r.stdout)
	checkStream(t, r.stderr, "Usage: timestamp <command>")
}

func TestMissingCommand(t *testing.T) {
	r := run(t, "./timestamp", "timestamp-test-nonexistent")
	checkExit(t, r.exit, 127)
	checkTimestampStream(t, r.stdout)
	checkStream(t, r.stderr, "executable file not found")
}

func TestStdin(t *testing.T) {
	cmd := exec.Command("./timestamp", "cat")
	cmd.Stdin = strings.NewReader("hello from stdin\n")
	r := runCmd(t, cmd)
	checkExit(t, r.exit, 0)
	checkTimestampStream(t, r.stdout, "cat", "hello from stdin")
	checkTimestampStream(t, r.stderr)
}

func run(t *testing.T, program string, args ...string) result {
	t.Helper()
	cmd := exec.Command(program, args...)
	return runCmd(t, cmd)
}

func runCmd(t *testing.T, cmd *exec.Cmd) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return r
	}

	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run %v: %v", cmd, err)
	}

	r.exit = exit.ExitCode()
	return r
}

func checkExit(t *testing.T, exit, want int) {
	t.Helper()

	if exit != want {
		t.Fatalf("exit = %d, want %d", exit, want)
	}
}

func checkStream(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(got, want) {
		t.Fatalf("stderr = %q, want substring %q", got, want)
	}
}

func checkTimestampStream(t *testing.T, got string, want ...string) {
	t.Helper()

	if len(want) == 0 {
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		return
	}

	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %q, want %v", got, want)
	}

	for i, line := range lines {
		if !linePrefix.MatchString(line) {
			t.Fatalf("line %d missing timestamp: %q", i, line)
		}
		idx := strings.Index(line, "] ")
		if got := line[idx+2:]; got != want[i] {
			t.Fatalf("line %d: got %q, want %q", i, got, want[i])
		}
	}
}
