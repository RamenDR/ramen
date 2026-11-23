// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// Command kill-self gives TestRunCommandKilled a child that kills itself on
// both Unix and Windows without canceling the test context. The test runs the
// compiled binary directly so RunCommand observes the child's exit status,
// not that of a wrapper such as go run.
package main

import "os"

func main() {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		panic(err)
	}

	if err := p.Kill(); err != nil {
		panic(err)
	}
}
