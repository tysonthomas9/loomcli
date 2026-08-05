package localgit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/gitauth"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/repositoryremote"
)

// The helper command is supplied through ephemeral git -c arguments. The
// credential is available only in the child Git environment, never in argv,
// a remote URL, or repository/global config.
//
//nolint:gosec // G101: environment variable name and helper template, not credential material.
const (
	gitCredentialPasswordEnv = "LOOM_PR_GIT_PASSWORD"
	gitCredentialHelper      = `!f() { test "$1" = get || exit 0; protocol= host=; while IFS='=' read -r key value; do case "$key" in protocol) protocol=$value ;; host) host=$value ;; esac; done; case "$protocol" in [hH][tT][tT][pP][sS]) ;; *) exit 0 ;; esac; case "$host" in [gG][iI][tT][hH][uU][bB].[cC][oO][mM]|[gG][iI][tT][hH][uU][bB].[cC][oO][mM]:443) ;; *) exit 0 ;; esac; printf '%s\n' username=x-access-token "password=$LOOM_PR_GIT_PASSWORD"; }; f`
)

func cloneAmbient(ctx context.Context, command connectors.GitReadCommand) error {
	args, err := cloneArgs(command)
	if err != nil {
		return err
	}
	if _, err := runNetworkGit(ctx, filepath.Dir(command.TargetPath), nil, true, args...); err != nil {
		return fmt.Errorf("ambient git clone failed for %s: %w", sanitizedRemoteURL(command.RemoteURL), err)
	}
	return nil
}

func cloneWithCredential(
	ctx context.Context,
	command connectors.GitReadCommand,
	credential *gitauth.Credential,
) error {
	if err := validateCredential(credential); err != nil {
		return err
	}
	args, err := cloneArgs(command)
	if err != nil {
		return err
	}
	if _, err := runNetworkGit(ctx, filepath.Dir(command.TargetPath), credential, false, args...); err != nil {
		return fmt.Errorf("credentialed git clone failed for %s: %w", sanitizedRemoteURL(command.RemoteURL), err)
	}
	return nil
}

func fetchRefAmbient(ctx context.Context, command connectors.GitReadCommand) error {
	args, err := fetchArgs(command)
	if err != nil {
		return err
	}
	if out, err := runNetworkGit(ctx, command.TargetPath, nil, true, args...); err != nil {
		return fmt.Errorf("ambient git fetch failed: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// fetchRefFromLocalSource performs the same exact read-only ref fetch as the
// named-remote path, but uses the already-admitted absolute source path. It is
// reached only after exactCheckoutRemoteMode proves the target is a linked
// worktree of that source repository, so no persistent remote mutation is
// needed in the user's repository configuration.
func fetchRefFromLocalSource(ctx context.Context, command connectors.GitReadCommand) error {
	if err := validateGitReadCommand(command); err != nil {
		return err
	}
	if !filepath.IsAbs(command.RemoteURL) || filepath.Clean(command.RemoteURL) != command.RemoteURL {
		return fmt.Errorf("local Git source path is invalid")
	}
	args := []string{
		"fetch", "--no-tags", "--force", "--",
		command.RemoteURL, command.SourceRef + ":" + command.DestinationRef,
	}
	if out, err := runNetworkGit(ctx, command.TargetPath, nil, true, args...); err != nil {
		return fmt.Errorf("ambient local Git fetch failed: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func fetchRefWithCredential(
	ctx context.Context,
	command connectors.GitReadCommand,
	credential *gitauth.Credential,
) error {
	if err := validateCredential(credential); err != nil {
		return err
	}
	args, err := fetchArgs(command)
	if err != nil {
		return err
	}
	if out, err := runNetworkGit(ctx, command.TargetPath, credential, false, args...); err != nil {
		return fmt.Errorf("credentialed git fetch failed: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func cloneArgs(command connectors.GitReadCommand) ([]string, error) {
	if err := validateGitReadCommand(command); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(command.TargetPath), 0o755); err != nil {
		return nil, fmt.Errorf("create clone parent directory: %w", err)
	}
	remoteName := command.RemoteName
	if strings.TrimSpace(remoteName) == "" {
		remoteName = "origin"
	}
	return []string{
		"clone", "--origin", remoteName, "--",
		command.RemoteURL, command.TargetPath,
	}, nil
}

func fetchArgs(command connectors.GitReadCommand) ([]string, error) {
	if err := validateGitReadCommand(command); err != nil {
		return nil, err
	}
	return []string{
		"fetch", "--no-tags", "--force", "--",
		command.RemoteName, command.SourceRef + ":" + command.DestinationRef,
	}, nil
}

func validateGitReadCommand(command connectors.GitReadCommand) error {
	if err := rejectRemoteURLSecrets(command.RemoteURL); err != nil {
		return err
	}
	switch command.Operation {
	case connectors.GitReadClone:
		if command.RemoteName != "" {
			if err := validateRemoteName(command.RemoteName); err != nil {
				return err
			}
		}
		if command.SourceRef != "" || command.DestinationRef != "" {
			return fmt.Errorf("git clone cannot carry fetch refs")
		}
		return nil
	case connectors.GitReadFetchRef:
		if err := validateRemoteName(command.RemoteName); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported git read operation %q", command.Operation)
	}
	for _, ref := range []string{command.SourceRef, command.DestinationRef} {
		if !strings.HasPrefix(ref, "refs/") ||
			strings.HasPrefix(ref, "-") ||
			strings.ContainsAny(ref, " \t\r\n:") {
			return fmt.Errorf("git ref is invalid")
		}
	}
	return nil
}

func validateRemoteName(remoteName string) error {
	if strings.TrimSpace(remoteName) == "" ||
		strings.HasPrefix(remoteName, "-") ||
		strings.ContainsAny(remoteName, " \t\r\n:/\\") {
		return fmt.Errorf("git remote name is invalid")
	}
	return nil
}

func runNetworkGit(
	ctx context.Context,
	dir string,
	credential *gitauth.Credential,
	ambient bool,
	args ...string,
) (string, error) {
	if credential != nil {
		if err := validateCredential(credential); err != nil {
			return "", err
		}
		if err := requireCredentialProcessIsolation(); err != nil {
			return "", err
		}
		args = append([]string{
			"-c", "credential.helper=",
			"-c", "credential.helper=" + gitCredentialHelper,
			"-c", "core.askPass=",
			"-c", "http.extraHeader=",
		}, args...)
	}
	command := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed executable; the credential is never in argv.
	command.Dir = dir
	command.Env = networkGitEnv(credential, ambient)
	command.WaitDelay = 2 * time.Second
	configureNetworkGitCancellation(command)

	rawOutput, err := command.CombinedOutput()
	output := redactCredential(rawOutput, credential)
	zeroBytes(rawOutput)
	clearCommandEnv(command)
	if err != nil {
		return string(output), fmt.Errorf(
			"git %s: %w: %s",
			sanitizedGitArgs(args),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return string(output), nil
}

func validateCredential(credential *gitauth.Credential) error {
	if credential == nil || len(credential.Password) == 0 {
		return fmt.Errorf("resolved git credential is required")
	}
	if credential.Username != "x-access-token" {
		return fmt.Errorf("resolved git credential username is unsupported")
	}
	if bytes.ContainsAny(credential.Password, "\x00\r\n") {
		return fmt.Errorf("resolved git credential contains invalid control characters")
	}
	return nil
}

func networkGitEnv(credential *gitauth.Credential, ambient bool) []string {
	allowed := map[string]struct{}{
		"PATH": {}, "TMPDIR": {}, "TMP": {}, "TEMP": {},
		"SystemRoot": {}, "WINDIR": {}, "COMSPEC": {}, "PATHEXT": {},
		"LANG": {}, "LC_ALL": {}, "LC_CTYPE": {}, "LC_MESSAGES": {},
		"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
		"http_proxy": {}, "https_proxy": {}, "no_proxy": {},
		"ALL_PROXY": {}, "all_proxy": {},
		"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {}, "CURL_CA_BUNDLE": {},
		"GIT_SSL_CAINFO": {}, "GIT_SSL_CAPATH": {},
	}
	if ambient {
		for _, name := range []string{
			"HOME", "USER", "LOGNAME", "SSH_AUTH_SOCK", "GIT_SSH_COMMAND",
			"XDG_CONFIG_HOME",
		} {
			allowed[name] = struct{}{}
		}
	}
	env := make([]string, 0, len(allowed)+3)
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, keep := allowed[name]; keep {
			env = append(env, entry)
		}
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	if credential != nil {
		// exec.Cmd requires immutable environment strings. Clear the Cmd.Env
		// references immediately after Wait; Credential.Close overwrites the
		// mutable source bytes in the caller.
		env = append(env, gitCredentialPasswordEnv+"="+string(credential.Password))
	}
	return env
}

func sanitizedGitArgs(args []string) string {
	safe := make([]string, len(args))
	for index, arg := range args {
		safe[index] = sanitizedRemoteURL(arg)
	}
	return strings.Join(safe, " ")
}

func sanitizedRemoteURL(remoteURL string) string {
	trimmed := strings.TrimSpace(remoteURL)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return trimmed
	}
	if index := strings.IndexAny(trimmed, "?#"); index >= 0 {
		trimmed = trimmed[:index]
	}
	parts := strings.SplitN(trimmed, "://", 2)
	if len(parts) != 2 {
		return trimmed
	}
	if at := strings.LastIndex(parts[1], "@"); at >= 0 {
		return parts[0] + "://***@" + parts[1][at+1:]
	}
	return trimmed
}

func rejectRemoteURLSecrets(remoteURL string) error {
	if _, err := repositoryremote.Normalize(remoteURL); err != nil {
		return fmt.Errorf("git remote URL is not canonical and token-free: %w", err)
	}
	return nil
}

func redactCredential(output []byte, credential *gitauth.Credential) []byte {
	if credential == nil || len(credential.Password) == 0 {
		return bytes.Clone(output)
	}
	return bytes.ReplaceAll(bytes.Clone(output), credential.Password, []byte("***"))
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
