// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramendr/ramen/internal/controller/hooks"
)

func TestParseCommandArray(t *testing.T) {
	raw := `["sudo","find","/tmp","-maxdepth","1"]`
	out, err := hooks.ConvertCommandToStringArray(raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"sudo", "find", "/tmp", "-maxdepth", "1"}, out)
}

func TestParseCommandQuotedScript(t *testing.T) {
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

func TestParseCommandError(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{
			name:    "unclosed single quote",
			command: `echo 'broken`,
		},
		{
			name:    "unclosed double quote",
			command: `echo "broken`,
		},
		{
			name:    "unclosed backslash",
			command: `echo \`,
		},
		{
			name:    "invalid JSON array",
			command: `[not json]`,
		},
		{
			name:    "JSON array trailing comma",
			command: `["sudo", "find",]`,
		},
		{
			name:    "JSON array missing comma",
			command: `[true false]`,
		},
		{
			name:    "JSON array numeric trailing comma",
			command: `[1,2,3,]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hooks.ConvertCommandToStringArray(tt.command)
			require.Error(t, err)
		})
	}
}

func TestParseCommandComplexNestedQuotes(t *testing.T) {
	// Test the actual complex command from the user's issue
	// nolint:lll // Long command string is necessary for testing
	cmd := `/bin/bash -c 'sudo find /mnt/blumeta0 -type l -exec /bin/bash -c '\''for p; do t=$(readlink "$p"); case "$t" in opt/*|usr/*|var/*|etc/*|bin/*|sbin/*|lib/*|lib64/*|mnt/*) o=$(stat -c "%u:%g" "$p"); echo -e "[FIX] $p\n  current -> $t\n  new     -> /$t\n  owner   -> $o"; sudo ln -snf "/$t" "$p" && sudo chown -h "$o" "$p"; echo ;; esac; done'\'' _ {} +; sudo rm -rfv /mnt/bludata0/sshkeys'`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)

	//nolint:lll // Long expected script string must match parsed output exactly
	wantScript := `sudo find /mnt/blumeta0 -type l -exec /bin/bash -c 'for p; do t=$(readlink "$p"); case "$t" in opt/*|usr/*|var/*|etc/*|bin/*|sbin/*|lib/*|lib64/*|mnt/*) o=$(stat -c "%u:%g" "$p"); echo -e "[FIX] $p\n  current -> $t\n  new     -> /$t\n  owner   -> $o"; sudo ln -snf "/$t" "$p" && sudo chown -h "$o" "$p"; echo ;; esac; done' _ {} +; sudo rm -rfv /mnt/bludata0/sshkeys`
	want := []string{"/bin/bash", "-c", wantScript}
	assert.Equal(t, want, out)
}

func TestParseCommandDoubleQuote(t *testing.T) {
	cmd := `sh -c "echo $HOME and \"quoted\" text"`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	assert.Equal(t, []string{"sh", "-c", `echo $HOME and "quoted" text`}, out)
}

func TestParseCommandSingleQuote(t *testing.T) {
	cmd := `sh -c 'echo $HOME stays literal'`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	assert.Equal(t, []string{"sh", "-c", "echo $HOME stays literal"}, out)
}

func TestParseCommandEscapedCharacters(t *testing.T) {
	cmd := `echo hello\ world "test\"quote"`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	// shlex should handle escaped spaces and quotes
	require.Greater(t, len(out), 0)
	assert.Equal(t, "echo", out[0])
	assert.Equal(t, "hello world", out[1])
	assert.Equal(t, `test"quote`, out[2])
}

func TestParseCommandFindExecSemicolon(t *testing.T) {
	// Verify that find -exec with \; is properly parsed
	cmd := `find /tmp -name "*.txt" -exec rm {} \;`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)

	// The semicolon should be preserved as a separate token
	assert.Contains(t, out, ";", "Semicolon should be in the parsed output")
	assert.Equal(t, ";", out[len(out)-1], "Last token should be semicolon")
}

func TestParseCommandMultipleSpaces(t *testing.T) {
	cmd := `echo    hello     world`
	out, err := hooks.ConvertCommandToStringArray(cmd)
	require.NoError(t, err)
	// Multiple spaces should be collapsed
	assert.Equal(t, []string{"echo", "hello", "world"}, out)
}
