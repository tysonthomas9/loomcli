package localgit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/gitauth"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

type recordingCredentialSource struct {
	mu       sync.Mutex
	password []byte
	calls    int
}

func (source *recordingCredentialSource) Resolve(
	context.Context,
	string,
) (*gitauth.Credential, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls++
	return &gitauth.Credential{
		Username: "x-access-token",
		Password: source.password,
	}, nil
}

func (source *recordingCredentialSource) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls
}

func TestExecutorOwnsCredentialedCloneAndClearsResolvedSecret(t *testing.T) {
	fakeGit := installFakeGit(t, false)
	t.Setenv("OPENAI_API_KEY", "must-not-reach-git")
	t.Setenv("GITHUB_TOKEN", "must-not-reach-git")
	t.Setenv("LOOM_RUN_TOKEN", "must-not-reach-git")

	password := []byte("connector-owned-clone-secret")
	source := &recordingCredentialSource{password: password}
	executor := New(source)
	command := cloneCommand(t, "https://github.com/acme/private.git")
	if err := executor.ExecuteGitRead(t.Context(), command); err != nil {
		t.Fatalf("ExecuteGitRead: %v", err)
	}
	if source.callCount() != 1 {
		t.Fatalf("credential source calls = %d, want 1", source.callCount())
	}
	for index, value := range password {
		if value != 0 {
			t.Fatalf("resolved password byte %d was not cleared", index)
		}
	}
	args := readFakeGitOutput(t, fakeGit+".args")
	if strings.Contains(args, "connector-owned-clone-secret") {
		t.Fatalf("credential reached git argv: %s", args)
	}
	if !strings.Contains(args, "credential.helper=") {
		t.Fatalf("credentialed git argv omitted ephemeral helper: %s", args)
	}
	envNames := readFakeGitOutput(t, fakeGit+".envnames")
	for _, forbidden := range []string{
		"OPENAI_API_KEY",
		"GITHUB_TOKEN",
		"LOOM_RUN_TOKEN",
	} {
		if strings.Contains("\n"+envNames+"\n", "\n"+forbidden+"\n") {
			t.Fatalf("credentialed git inherited forbidden environment %s", forbidden)
		}
	}
	if !strings.Contains("\n"+envNames+"\n", "\n"+gitCredentialPasswordEnv+"\n") {
		t.Fatalf("credentialed git omitted its one scoped credential environment")
	}
}

func TestExecutorKeepsPublicCloneAnonymous(t *testing.T) {
	installFakeGit(t, true)
	source := &recordingCredentialSource{password: []byte("must-not-be-resolved")}
	executor := New(source)
	if err := executor.ExecuteGitRead(
		t.Context(),
		cloneCommand(t, "https://github.com/acme/public.git"),
	); err != nil {
		t.Fatalf("ExecuteGitRead: %v", err)
	}
	if source.callCount() != 0 {
		t.Fatalf("credential source calls = %d, want 0", source.callCount())
	}
}

func TestExecutorRejectsRemoteAuthorityBeforeCredentialResolution(t *testing.T) {
	installFakeGit(t, false)
	source := &recordingCredentialSource{password: []byte("must-not-be-resolved")}
	executor := New(source)
	for _, remoteURL := range []string{
		"https://user:secret@github.com/acme/private.git",
		"https://github.com/acme/private.git?access_token=secret",
		"https://github.com/acme/private.git#secret",
	} {
		command := cloneCommand(t, remoteURL)
		err := executor.ExecuteGitRead(t.Context(), command)
		if err == nil || !strings.Contains(err.Error(), "invalid bounded Git read") {
			t.Fatalf("remote %q error = %v, want containment rejection", remoteURL, err)
		}
		if strings.Contains(err.Error(), "access_token=secret") ||
			strings.Contains(err.Error(), "user:secret") {
			t.Fatalf("remote rejection reflected authority: %v", err)
		}
	}
	if source.callCount() != 0 {
		t.Fatalf("credential source calls = %d, want 0", source.callCount())
	}
}

func TestCredentialedGitRedactsFailureAndKeepsSecretOutOfArgv(t *testing.T) {
	fakeGit := installFailingCredentialGit(t)
	password := []byte("connector-error-secret")
	_, err := runNetworkGit(
		t.Context(),
		t.TempDir(),
		&gitauth.Credential{Username: "x-access-token", Password: password},
		false,
		"fetch", "origin", "main",
	)
	if err == nil {
		t.Fatal("credentialed Git succeeded, want fake failure")
	}
	if strings.Contains(err.Error(), "connector-error-secret") {
		t.Fatalf("git error leaked credential: %v", err)
	}
	if !strings.Contains(err.Error(), "password=***") {
		t.Fatalf("git error = %v, want redacted subprocess output", err)
	}
	if args := readFakeGitOutput(t, fakeGit+".args"); strings.Contains(args, "connector-error-secret") {
		t.Fatalf("git argv leaked credential: %s", args)
	}
}

func cloneCommand(t *testing.T, remoteURL string) connectors.GitReadCommand {
	t.Helper()
	workspace := t.TempDir()
	return connectors.GitReadCommand{
		WorkspaceKey:  "WS-1",
		OperationID:   "clone-1",
		RepositoryRef: "repo-1",
		Operation:     connectors.GitReadClone,
		RemoteURL:     remoteURL,
		RemoteName:    "origin",
		WorkspacePath: workspace,
		TargetPath:    filepath.Join(workspace, "repo"),
	}
}

func installFakeGit(t *testing.T, anonymousSucceeds bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake Git is Unix-only")
	}
	fakeBin := t.TempDir()
	path := filepath.Join(fakeBin, "git")
	anonymousExit := "1"
	if anonymousSucceeds {
		anonymousExit = "0"
	}
	body := `#!/bin/sh
printf '%s\n' "$@" > "$0.args"
env | while IFS='=' read -r name value; do printf '%s\n' "$name"; done > "$0.envnames"
if [ -n "$LOOM_PR_GIT_PASSWORD" ]; then
  exit 0
fi
exit ` + anonymousExit + `
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func installFailingCredentialGit(t *testing.T) string {
	t.Helper()
	path := installFakeGit(t, false)
	body := `#!/bin/sh
printf '%s\n' "$@" > "$0.args"
printf 'fatal: rejected password=%s\n' "$LOOM_PR_GIT_PASSWORD" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("rewrite fake git: %v", err)
	}
	return path
}

func readFakeGitOutput(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
