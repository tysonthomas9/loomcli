package localgit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

const maxGitRemoteOutput = 64 * 1024

// Inspector compares a checkout's origin with an expected token-free remote.
// The observed remote is kept inside this adapter and wiped after comparison;
// it never crosses the Source Control port.
type Inspector struct{}

var _ sourcecontrol.CheckoutInspector = Inspector{}

func (Inspector) CanonicalTarget(
	ctx context.Context,
	workspacePath string,
	targetPath string,
) (string, error) {
	canonicalTarget, err := canonicalCheckoutTarget(ctx, workspacePath, targetPath)
	if err != nil {
		return "", fmt.Errorf("%w: checkout containment validation failed: %w", sourcecontrol.ErrInvalid, err)
	}
	return canonicalTarget, nil
}

func (Inspector) MatchRemote(
	ctx context.Context,
	targetPath string,
	remoteName string,
	expectedRemote string,
) (sourcecontrol.CheckoutMatch, error) {
	info, err := os.Lstat(targetPath)
	if os.IsNotExist(err) {
		return sourcecontrol.CheckoutMissing, nil
	}
	if err != nil {
		return "", fmt.Errorf("stat checkout target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return sourcecontrol.CheckoutConflict, nil
	}
	gitInfo, err := os.Lstat(filepath.Join(targetPath, ".git"))
	if err != nil {
		if os.IsNotExist(err) {
			return sourcecontrol.CheckoutConflict, nil
		}
		return "", fmt.Errorf("stat checkout metadata: %w", err)
	}
	if gitInfo.Mode()&os.ModeSymlink != 0 || (!gitInfo.IsDir() && !gitInfo.Mode().IsRegular()) {
		return sourcecontrol.CheckoutConflict, nil
	}

	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.limit = maxGitRemoteOutput
	stderr.limit = maxGitRemoteOutput
	cmd := exec.CommandContext(ctx, "git", "-C", targetPath, "remote", "get-url", remoteName) //nolint:gosec // fixed executable and validated remote name.
	cmd.Env = localGitEnv()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	clearCommandEnv(cmd)
	if err != nil {
		stdout.zero()
		stderr.zero()
		return sourcecontrol.CheckoutConflict, nil
	}
	observed := bytes.TrimSpace(stdout.bytes())
	matched := bytes.Equal(observed, []byte(expectedRemote))
	stdout.zero()
	stderr.zero()
	if matched {
		return sourcecontrol.CheckoutMatched, nil
	}
	return sourcecontrol.CheckoutConflict, nil
}

func (Inspector) ResolveCommit(
	ctx context.Context,
	targetPath string,
	ref string,
) (string, error) {
	if strings.TrimSpace(targetPath) == "" || strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("checkout path and ref are required")
	}
	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.limit = maxGitRemoteOutput
	stderr.limit = maxGitRemoteOutput
	cmd := exec.CommandContext(ctx, "git", "-C", targetPath, "rev-parse", "--verify", ref+"^{commit}") //nolint:gosec // fixed executable; ref was validated by Source Control.
	cmd.Env = localGitEnv()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	clearCommandEnv(cmd)
	if err != nil {
		stdout.zero()
		message := strings.TrimSpace(string(stderr.bytes()))
		stderr.zero()
		if message == "" {
			return "", fmt.Errorf("resolve fetched ref: %w", err)
		}
		return "", fmt.Errorf("resolve fetched ref: %w: %s", err, message)
	}
	value := strings.TrimSpace(string(stdout.bytes()))
	stdout.zero()
	stderr.zero()
	return value, nil
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (writer *limitedBuffer) Write(value []byte) (int, error) {
	if writer == nil || writer.limit <= 0 {
		return 0, fmt.Errorf("checkout inspection output limit is unavailable")
	}
	remaining := writer.limit - writer.buffer.Len()
	if remaining <= 0 || len(value) > remaining {
		return 0, fmt.Errorf("checkout inspection output exceeds %d bytes", writer.limit)
	}
	return writer.buffer.Write(value)
}

func (writer *limitedBuffer) bytes() []byte {
	if writer == nil {
		return nil
	}
	return writer.buffer.Bytes()
}

func (writer *limitedBuffer) zero() {
	if writer == nil {
		return
	}
	value := writer.buffer.Bytes()
	for index := range value {
		value[index] = 0
	}
	writer.buffer.Reset()
}

func localGitEnv() []string {
	allowed := map[string]struct{}{
		"PATH": {}, "TMPDIR": {}, "TMP": {}, "TEMP": {},
		"SystemRoot": {}, "WINDIR": {}, "COMSPEC": {}, "PATHEXT": {},
		"LANG": {}, "LC_ALL": {}, "LC_CTYPE": {}, "LC_MESSAGES": {},
	}
	env := make([]string, 0, len(allowed)+2)
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, keep := allowed[name]; keep {
			env = append(env, entry)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}

func clearCommandEnv(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	for index := range cmd.Env {
		cmd.Env[index] = ""
	}
	cmd.Env = nil
}
