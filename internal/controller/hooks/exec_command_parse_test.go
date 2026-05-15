// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramendr/ramen/internal/controller/hooks"
)

func TestConvertCommandToStringArray_JSONArray(t *testing.T) {
	t.Parallel()

	raw := `["sudo","find","/tmp","-maxdepth","1"]`
	out, err := hooks.ConvertCommandToStringArray(raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"sudo", "find", "/tmp", "-maxdepth", "1"}, out)
}

func TestConvertCommandToStringArray_findExecWithQuotedScript(t *testing.T) {
	t.Parallel()

	// argv must preserve single-quoted -c script and end with ';' for find -exec (not '\\;' split across tokens).
	cmd := `sudo find /mnt/blumeta0 -type l -exec sh -c 'ln -snf "$(readlink -f "{}")" "{}"' \;`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	require.Equal(t, ";", out[len(out)-1])

	idx := -1

	for i := range out {
		if out[i] == "-c" {
			idx = i

			break
		}
	}

	require.NotEqual(t, -1, idx)
	require.Less(t, idx+1, len(out))
	assert.Contains(t, out[idx+1], "readlink")
}

func TestConvertCommandToStringArray_shlexUnclosedQuote(t *testing.T) {
	t.Parallel()

	_, err := hooks.ConvertCommandToStringArray(`echo 'broken`)
	require.Error(t, err)
}

func TestConvertCommandToStringArray_ComplexNestedQuotes(t *testing.T) {
	t.Parallel()

	// Test the actual complex command from the user's issue
	// nolint:lll // Long command string is necessary for testing
	cmd := `/bin/bash -c 'sudo find /mnt/blumeta0 -type l -exec /bin/bash -c '\''for p; do t=$(readlink "$p"); case "$t" in opt/*|usr/*|var/*|etc/*|bin/*|sbin/*|lib/*|lib64/*|mnt/*) o=$(stat -c "%u:%g" "$p"); echo -e "[FIX] $p\n  current -> $t\n  new     -> /$t\n  owner   -> $o"; sudo ln -snf "/$t" "$p" && sudo chown -h "$o" "$p"; echo ;; esac; done'\'' _ {} +; sudo rm -rfv /mnt/bludata0/sshkeys'`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)

	// Verify the command is properly parsed
	require.Greater(t, len(out), 0, "Command should be parsed into multiple tokens")
	assert.Equal(t, "/bin/bash", out[0], "First token should be /bin/bash")
	assert.Equal(t, "-c", out[1], "Second token should be -c")

	// The third token should be the entire script as a single string
	require.Greater(t, len(out), 2, "Should have at least 3 tokens")
	assert.Contains(t, out[2], "sudo find", "Script should contain 'sudo find'")
	assert.Contains(t, out[2], "readlink", "Script should contain 'readlink'")
	assert.Contains(t, out[2], "sudo rm -rfv", "Script should contain 'sudo rm -rfv'")
}

func TestConvertCommandToStringArray_DoubleQuotesWithVariables(t *testing.T) {
	t.Parallel()

	cmd := `sh -c "echo $HOME and \"quoted\" text"`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	assert.Equal(t, []string{"sh", "-c", `echo $HOME and "quoted" text`}, out)
}

func TestConvertCommandToStringArray_SingleQuotesPreserveEverything(t *testing.T) {
	t.Parallel()

	cmd := `sh -c 'echo $HOME stays literal'`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	assert.Equal(t, []string{"sh", "-c", "echo $HOME stays literal"}, out)
}

func TestConvertCommandToStringArray_EscapedCharacters(t *testing.T) {
	t.Parallel()

	cmd := `echo hello\ world "test\"quote"`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	// shlex should handle escaped spaces and quotes
	require.Greater(t, len(out), 0)
	assert.Equal(t, "echo", out[0])
	assert.Equal(t, "hello world", out[1])
	assert.Equal(t, `test"quote`, out[2])
}

func TestConvertCommandToStringArray_FindExecSemicolon(t *testing.T) {
	t.Parallel()

	// Verify that find -exec with \; is properly parsed
	cmd := `find /tmp -name "*.txt" -exec rm {} \;`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)

	// The semicolon should be preserved as a separate token
	assert.Contains(t, out, ";", "Semicolon should be in the parsed output")
	assert.Equal(t, ";", out[len(out)-1], "Last token should be semicolon")
}

func TestConvertCommandToStringArray_MultipleSpaces(t *testing.T) {
	t.Parallel()

	cmd := `echo    hello     world`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	// Multiple spaces should be collapsed
	assert.Equal(t, []string{"echo", "hello", "world"}, out)
}
