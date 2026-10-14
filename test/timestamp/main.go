// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// shellSpecialChars lists characters that make an argv token unsafe to paste unquoted.
const shellSpecialChars = ` '"\;|&$><*?#~!()[]{}` + "\t`"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: timestamp <command> [arguments...]")
		os.Exit(1)
	}

	targetCmd := os.Args[1]
	targetArgs := os.Args[2:]

	cmd := exec.Command(targetCmd, targetArgs...)

	// Pass stdin to the child process so commands like "podman build -f -"
	// can read from a heredoc or pipe.
	cmd.Stdin = os.Stdin

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error allocating stdout pipe: %v\n", err)
		os.Exit(1)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error allocating stderr pipe: %v\n", err)
		os.Exit(1)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error executing '%v': %v\n", cmd, err)
		os.Exit(127) // Standard UNIX exit code for command not found
	}

	start := time.Now()

	// Synthesize the start log at 0.000 to make the output clearer. Without it,
	// the first timestamp appears when the command prints its first line.
	// Quoting is reconstructed from argv so the line is copy-pasteable in a shell.
	// [    0.000] bash -c "sleep 1; echo it works"
	// [    1.022] it works
	fmt.Fprintf(os.Stdout, "[%9.3f] %s\n", 0.0, quotedCommand(os.Args[1:]))

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		processStream(stdoutPipe, os.Stdout, start)
	}()

	go func() {
		defer wg.Done()
		processStream(stderrPipe, os.Stderr, start)
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Error harvesting process termination status: %v\n", err)
		os.Exit(1)
	}
}

func quotedCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, shellSpecialChars) {
			arg = strconv.Quote(arg)
		}
		quoted[i] = arg
	}
	return strings.Join(quoted, " ")
}

func processStream(pipe io.Reader, outputStream *os.File, start time.Time) {
	reader := bufio.NewReader(pipe)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			elapsed := time.Since(start).Seconds()
			fmt.Fprintf(outputStream, "[%9.3f] %s", elapsed, line)
		}
		if err != nil {
			if err != io.EOF {
				elapsed := time.Since(start).Seconds()
				fmt.Fprintf(os.Stderr, "[%9.3f] timestamp: error reading stream: %v\n", elapsed, err)
			}
			break // Pipe closed
		}
	}
}
