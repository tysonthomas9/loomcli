package doctor

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

const publishAuthCheckName = "publish_auth"

// goos reports the running platform. It is a variable so the darwin-only probe
// can be exercised from a test on any host, which a build tag would not allow.
var goos = func() string { return runtime.GOOS }

// gitCredentialFill runs `git credential fill` for host and returns git's raw
// stdout. It is a variable because this probe needs stdin, which deps.Exec does
// not provide, and because tests must be able to stand in for a real git.
//
// The returned string IS the credential. It must never reach a CheckResult:
// callers pass it to credentialFillHasPassword and discard it. See
// internal/stackpublish/scrub.go for the same rule applied to error text.
var gitCredentialFill = runGitCredentialFill

// gitRemoteURL resolves the current repo's origin. A variable so a test can name
// a remote host without needing a real clone pointed at one.
var gitRemoteURL = localworkspace.GitRemoteURL

// ensureCredentialHelper is the only --fix action this check takes. A variable
// so the fix test does not rewrite the real repo's .git/config.
var ensureCredentialHelper = localworkspace.EnsureCredentialHelper

func runGitCredentialFill(ctx context.Context, dir, host string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "credential", "fill")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(fmt.Sprintf("protocol=https\nhost=%s\n\n", host))
	// Without this git blocks on a terminal prompt when no helper answers.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git credential fill: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// checkPublishAuth collapses the whole "this host cannot publish" class into one
// line. The outage it names presents as five unrelated symptoms — a failed push,
// a TLS OSStatus, a missing keychain item, "Device not configured", and `id -un`
// printing a number — none of which any other doctor check reports.
//
// The probes run in order of how much they explain, first failure winning:
// directory services being gone explains the credential failure, which in turn
// explains the push failure.
func checkPublishAuth(deps *cli.Deps) CheckResult {
	ctx := context.Background()

	if result, broken := probeDirectoryServices(deps); broken {
		return result
	}

	host, credentialsOK := probeRepoCredentials(ctx)
	if host != "" && !credentialsOK {
		tokenSet := tokenEnvSet()
		helperSet := repoHasLoomCredentialHelper(deps)

		// Only one shape of this failure has a safe automatic remedy: the token
		// is there and nothing routes git to it. A missing token or a dead
		// directory service is an operator decision, not ours.
		if doctorFix && tokenSet && !helperSet {
			if err := ensureCredentialHelper(ctx, "."); err == nil {
				_, credentialsOK = probeRepoCredentials(ctx)
				helperSet = true
			}
		}

		if !credentialsOK {
			return CheckResult{
				Name:    publishAuthCheckName,
				Status:  StatusFail,
				Summary: fmt.Sprintf("git cannot resolve credentials for %s (pushes will fail)", host),
				Detail:  credentialFailureDetail(tokenSet, helperSet),
			}
		}
	}

	if !ghTokenAvailable(deps) {
		return CheckResult{
			Name:    publishAuthCheckName,
			Status:  StatusWarn,
			Summary: "gh has no usable token (PR creation will fail)",
			Detail:  "set GITHUB_TOKEN in the agent environment, or gh auth login --with-token",
		}
	}

	return CheckResult{
		Name:    publishAuthCheckName,
		Status:  StatusPass,
		Summary: publishAuthPassSummary(host),
		Detail: "Not verified: an expired or wrong-scope token still fills and still prints here.\n" +
			"git credential fill only formats a credential and gh auth token does not validate one;\n" +
			"validating would need the network, and loom doctor must work offline.",
	}
}

// probeDirectoryServices reports whether this process has lost DirectoryServices,
// which on darwin makes `id -un` print the raw uid instead of a name. Everything
// keychain- or ssh-backed fails from there, so it is the first thing to say.
func probeDirectoryServices(deps *cli.Deps) (CheckResult, bool) {
	if goos() != "darwin" {
		return CheckResult{}, false
	}

	result := deps.Exec.Run(".", "id", "-un")
	if result.Err != nil {
		// Inconclusive. Do not warn: a warning here would preempt the credential
		// probe, which is the one that says whether publishing actually works.
		return CheckResult{}, false
	}

	uid := strings.TrimSpace(result.Stdout)
	if _, err := strconv.Atoi(uid); err != nil {
		return CheckResult{}, false
	}

	return CheckResult{
		Name:    publishAuthCheckName,
		Status:  StatusFail,
		Summary: "directory services unavailable (id -un returned a numeric uid)",
		Detail: fmt.Sprintf(
			"The login keychain is unreadable, ssh aborts with \"No user exists for uid %s\", and\n"+
				"git credential fill returns \"failed to get: -50\" for any host without an explicit helper.\n"+
				"Relaunch the pm2 tree inside a user GUI session to restore it.\n"+
				"Meanwhile https remotes still push with GITHUB_TOKEN set in the environment.", uid),
	}, true
}

// probeRepoCredentials asks git whether it can produce a credential for the
// current repo's remote host. An empty host means there was nothing to probe —
// no repo, no origin, or a non-https remote — and checkGitRepo already covers
// the first of those.
func probeRepoCredentials(ctx context.Context) (string, bool) {
	remote, err := gitRemoteURL(".", "origin")
	if err != nil || !strings.HasPrefix(remote, "https://") {
		return "", false
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() == "" {
		return "", false
	}
	host := parsed.Hostname()

	out, err := gitCredentialFill(ctx, ".", host)
	if err != nil {
		return host, false
	}
	return host, credentialFillHasPassword(out)
}

// credentialFillHasPassword reports whether `git credential fill` output carries
// a non-empty password field. Only its presence is ever returned: the value is
// the secret. An empty password= line is a real failure — loom's helper emits
// exactly that when no token is in the environment.
func credentialFillHasPassword(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "password=") && strings.TrimPrefix(line, "password=") != "" {
			return true
		}
	}
	return false
}

// tokenEnvSet reports whether a token is in this process's environment, which is
// the only place loom's credential helper looks.
func tokenEnvSet() bool {
	return strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) != "" ||
		strings.TrimSpace(os.Getenv("GH_TOKEN")) != ""
}

func repoHasLoomCredentialHelper(deps *cli.Deps) bool {
	// A repo with no credential.helper key exits non-zero; that is "absent".
	result := deps.Exec.Run(".", "git", "config", "--local", "--get-all", "credential.helper")
	if result.Err != nil {
		return false
	}
	return strings.Contains(result.Stdout, localworkspace.LoomCredentialHelper)
}

// ghTokenAvailable mirrors the token resolution in internal/cli/stack and
// internal/cli/epic: environment first, then a local gh.
func ghTokenAvailable(deps *cli.Deps) bool {
	if tokenEnvSet() {
		return true
	}
	result := deps.Exec.Run(".", "gh", "auth", "token")
	return result.Err == nil && strings.TrimSpace(result.Stdout) != ""
}

func credentialFailureDetail(tokenSet, helperSet bool) string {
	lines := make([]string, 0, 3)
	if tokenSet {
		lines = append(lines, "GITHUB_TOKEN or GH_TOKEN is set in this process.")
	} else {
		lines = append(lines, "Neither GITHUB_TOKEN nor GH_TOKEN is set in this process.")
	}
	if helperSet {
		lines = append(lines, "This repo has loom's credential helper configured.")
	} else {
		lines = append(lines, "This repo has no loom credential helper configured.")
	}
	return strings.Join(append(lines, "Run: loom doctor --fix"), "\n")
}

func publishAuthPassSummary(host string) string {
	if host == "" {
		return "publish auth OK (gh token present)"
	}
	return fmt.Sprintf("publish auth OK (%s credentials resolve, gh token present)", host)
}
