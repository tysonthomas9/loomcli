// Package gitexec is the sole Git process boundary for Loom Git v2.
package gitexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/cred"
)

var (
	ErrForbidden = errors.New("git command forbidden")
	ErrStale     = errors.New("git ref is stale")
	ErrTruncated = errors.New("git output truncated")
	ErrTimeout   = errors.New("git command timed out")
)

const (
	defaultTimeout   = 2 * time.Minute
	defaultOutputCap = 16 << 20
)

// Identity is captured at construction, rather than inherited at commit time.
type Identity struct {
	Name  string
	Email string
}

type Options struct {
	GlobalConfig     string   // Empty reads Git's normal global config files.
	SystemConfig     string   // Empty reads Git's normal system config file.
	FallbackIdentity Identity // Used only when the user has no complete Git identity.
	Timeout          time.Duration
	OutputCap        int
}

type Runner struct {
	dir      string
	identity Identity
	config   []string
	timeout  time.Duration
	cap      int
}

type CommandError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("git %s: %s: %v", strings.Join(e.Args, " "), e.Stderr, e.Err)
}

func (e *CommandError) Unwrap() error { return e.Err }

var urlCredentials = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)
var tokenPattern = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|glpat-[A-Za-z0-9_-]+)\b`)

func redact(s string) string {
	s = urlCredentials.ReplaceAllString(s, "${1}***@")
	return tokenPattern.ReplaceAllString(s, "***")
}

// New reads the user's identity and the small allowed config set once.
func New(dir string, opts Options) (*Runner, error) {
	global, err := readConfig("--global", opts.GlobalConfig)
	if err != nil {
		return nil, err
	}
	system, err := readConfig("--system", opts.SystemConfig)
	if err != nil {
		return nil, err
	}
	r := &Runner{dir: dir, timeout: opts.Timeout, cap: opts.OutputCap}
	if r.timeout <= 0 {
		r.timeout = defaultTimeout
	}
	if r.cap <= 0 {
		r.cap = defaultOutputCap
	}
	for _, entries := range [][]configEntry{system, global} {
		for _, entry := range entries {
			if allowedConfig(entry.key) {
				r.config = append(r.config, entry.key+"="+entry.value)
			}
		}
	}
	for _, entry := range global {
		switch entry.key {
		case "user.name":
			r.identity.Name = entry.value
		case "user.email":
			r.identity.Email = entry.value
		}
	}
	if r.identity.Name == "" || r.identity.Email == "" {
		r.identity = opts.FallbackIdentity
	}
	if r.identity.Name == "" || r.identity.Email == "" {
		return nil, errors.New("git user.name and user.email are required")
	}
	return r, nil
}

func (r *Runner) Identity() Identity { return r.identity }

func (r *Runner) Path() string { return r.dir }

// CheckRefFormat validates a complete ref or a branch through the Git process boundary.
func CheckRefFormat(ref string, branch bool) error {
	args := []string{"check-ref-format"}
	if branch {
		args = append(args, "--branch")
	}
	args = append(args, ref)
	return runRefProbe("", args...)
}

// RefExists reports whether an exact ref exists in repo, including packed refs.
func RefExists(repo, ref string) (bool, error) {
	err := runRefProbe(repo, "show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func runRefProbe(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // Fixed Git verbs and argv; no shell.
	cmd.Dir = dir
	cmd.Env = cleanEnv(os.Environ())
	_, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ErrTimeout
	}
	return err
}

type configEntry struct{ key, value string }

func readConfig(scope, path string) ([]configEntry, error) {
	args := []string{"config", "--null", "--list"}
	if path != "" {
		args = append(args, "--file", path)
	} else {
		args = append(args, scope)
	}
	cmd := exec.Command("git", args...) //nolint:gosec // Fixed git binary and config-read arguments.
	cmd.Env = configReadEnv(scope, path)
	out, err := cmd.Output()
	if err != nil {
		// Git exits 128 when a config file does not exist; normal hosts need no system file.
		if path == "" && len(out) == 0 {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 128 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("read git config: %w", err)
	}
	var result []configEntry
	for _, item := range bytes.Split(out, []byte{0}) {
		if len(item) == 0 {
			continue
		}
		key, value, ok := strings.Cut(string(item), "\n")
		if !ok {
			continue
		}
		result = append(result, configEntry{key, value})
	}
	return result, nil
}

// configReadEnv lets Git locate the user's real config before we copy only
// allowlisted values into the isolated environment used for all other commands.
func configReadEnv(scope, path string) []string {
	env := cleanEnv(os.Environ())
	if path != "" {
		return env
	}
	blocked := "GIT_CONFIG_GLOBAL="
	if scope == "--system" {
		blocked = "GIT_CONFIG_NOSYSTEM="
	}
	filtered := env[:0]
	for _, item := range env {
		if !strings.HasPrefix(item, blocked) {
			filtered = append(filtered, item)
		}
	}
	if scope == "--global" {
		if path, ok := os.LookupEnv("GIT_CONFIG_GLOBAL"); ok {
			filtered = append(filtered, "GIT_CONFIG_GLOBAL="+path)
		}
	}
	return filtered
}

func allowedConfig(key string) bool {
	for _, prefix := range []string{"filter.", "lfs.", "gpg."} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	switch key {
	case "commit.gpgsign", "user.signingkey", "safe.directory", "core.autocrlf":
		return true
	default:
		return false
	}
}

func cleanEnv(env []string) []string {
	clean := make([]string, 0, len(env)+4)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") || key == "SSH_ASKPASS" {
			continue
		}
		clean = append(clean, item)
	}
	return append(clean, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="+os.DevNull)
}

func forbidden(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return true
	}
	// Git aliases only run when a verb is unknown to Git. Restrict verbs to
	// builtins so a local alias cannot become a shell command.
	if !builtinVerb(args[0]) {
		return true
	}
	if args[0] == "clean" {
		return true
	}
	return args[0] == "push" && forbiddenPush(args[1:])
}

func forbiddenPush(args []string) bool {
	for _, arg := range args {
		if arg == "--force" || arg == "--mirror" || strings.HasPrefix(arg, "+") ||
			(strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "f")) {
			return true
		}
		if strings.HasPrefix(arg, "--force-with-lease") {
			value, ok := strings.CutPrefix(arg, "--force-with-lease=")
			if !ok {
				return true
			}
			ref, sha, ok := strings.Cut(value, ":")
			if !ok || ref == "" || (sha != "" && !fullSHA(sha)) {
				return true
			}
		}
		if strings.HasPrefix(arg, "-") && strings.TrimLeft(arg, "-") == "force" {
			return true
		}
	}
	return false
}

func builtinVerb(verb string) bool {
	switch verb {
	case "add", "am", "apply", "archive", "bisect", "blame", "branch", "bundle",
		"cat-file", "check-attr", "check-ignore", "check-ref-format", "checkout",
		"cherry-pick", "clean", "clone", "commit", "commit-tree", "config", "credential",
		"describe", "diff", "diff-tree", "fetch", "for-each-ref", "fsck", "grep",
		"hash-object", "init", "log", "ls-files", "ls-remote", "ls-tree", "merge",
		"merge-base", "merge-tree", "mktree", "mv", "notes", "pull", "push",
		"read-tree", "rebase", "reflog", "remote", "reset", "restore", "rev-list",
		"rev-parse", "revert", "rm", "show", "show-ref", "status", "submodule",
		"symbolic-ref", "tag", "update-index", "update-ref", "verify-commit",
		"version", "worktree", "write-tree":
		return true
	default:
		return false
	}
}

var shaPattern = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

func fullSHA(s string) bool { return shaPattern.MatchString(s) }

type cappedWriter struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.max {
		w.exceeded = true
		remaining := w.max - w.buf.Len()
		if remaining > 0 {
			_, _ = w.buf.Write(p[:remaining])
		}
		return len(p), nil
	}
	return w.buf.Write(p)
}

// Run returns stdout. On failure stderr and command arguments are redacted.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return r.runWithEnv(ctx, nil, nil, args...)
}

// RunWithCredential permits one host fetch or push against the scoped URL.
// The token reaches Git through askpass, never argv or repository config.
func (r *Runner) RunWithCredential(ctx context.Context, source *cred.Source, repoURL string, args ...string) ([]byte, error) {
	if source == nil || len(args) < 2 || (args[0] != "fetch" && args[0] != "push") || forbidden(args) {
		return nil, ErrForbidden
	}
	found := false
	for _, arg := range args[1:] {
		if arg == repoURL {
			found = true
		} else if strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "http://") {
			return nil, ErrForbidden
		}
	}
	if !found {
		return nil, ErrForbidden
	}
	token, err := source.Take(repoURL)
	if err != nil {
		return nil, err
	}
	askpass, err := os.CreateTemp("", "loom-git-askpass-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(askpass.Name())
	defer askpass.Close()
	const script = "#!/bin/sh\ncase \"$1\" in *Username*) printf '%s' x-access-token;; *Password*) printf '%s' \"$LOOM_HOST_GIT_PASSWORD\";; esac\n"
	if _, err := askpass.WriteString(script); err != nil {
		return nil, err
	}
	if err := askpass.Chmod(0700); err != nil {
		return nil, err
	}
	// Linux refuses to execute a script while it is still open for writing.
	if err := askpass.Close(); err != nil {
		return nil, err
	}
	env := map[string]string{"GIT_ASKPASS": askpass.Name(), "LOOM_HOST_GIT_PASSWORD": token}
	out, err := r.runWithEnv(ctx, nil, env, args...)
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		commandErr.Stderr = strings.ReplaceAll(commandErr.Stderr, token, "***")
	}
	return out, err
}

func (r *Runner) run(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	return r.runWithEnv(ctx, input, nil, args...)
}

// RunWithEnv permits only the Git variables needed for an isolated index and
// preserving commit authors during source-revision freezing.
func (r *Runner) RunWithEnv(ctx context.Context, env map[string]string, args ...string) ([]byte, error) {
	for key := range env {
		switch key {
		case "GIT_INDEX_FILE", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE":
		default:
			return nil, ErrForbidden
		}
	}
	return r.runWithEnv(ctx, nil, env, args...)
}

func (r *Runner) runWithEnv(ctx context.Context, input io.Reader, env map[string]string, args ...string) ([]byte, error) {
	if forbidden(args) {
		return nil, ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	config := append([]string{}, r.config...)
	config = append(config, "core.hooksPath="+os.DevNull, "credential.helper=", "credential.interactive=never",
		"user.name="+r.identity.Name, "user.email="+r.identity.Email,
		"author.name="+r.identity.Name, "author.email="+r.identity.Email,
		"committer.name="+r.identity.Name, "committer.email="+r.identity.Email)
	argv := make([]string, 0, len(config)*2+len(args))
	//nolint:gosec // Only allowlisted Git config values enter this fixed Git invocation.
	for _, item := range config {
		argv = append(argv, "-c", item)
	}
	//nolint:gosec // Git verb and push modes are screened; arguments are passed without a shell.
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "git", argv...) //nolint:gosec // Git argv is screened above; no shell.
	cmd.Dir = r.dir
	cmd.Env = cleanEnv(os.Environ())
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdin = input
	out, stderr := &cappedWriter{max: r.cap}, &cappedWriter{max: r.cap}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, ErrTimeout
	}
	if out.exceeded || stderr.exceeded {
		return nil, ErrTruncated
	}
	if err != nil {
		safeArgs := make([]string, len(args))
		for i, arg := range args {
			safeArgs[i] = redact(arg)
		}
		return nil, &CommandError{Args: safeArgs, Stderr: redact(stderr.buf.String()), Err: err}
	}
	return out.buf.Bytes(), nil
}

// UpdateRef changes a ref only when its current value equals expected.
func (r *Runner) UpdateRef(ctx context.Context, ref, next, expected string) error {
	if strings.ContainsAny(ref, "\x00\n\r ") || !fullSHA(next) || !fullSHA(expected) {
		return ErrForbidden
	}
	input := strings.NewReader("update " + ref + " " + next + " " + expected + "\n")
	_, err := r.run(ctx, input, "update-ref", "--stdin")
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTimeout) || errors.Is(err, ErrTruncated) {
		return err
	}
	current, readErr := r.Run(ctx, "rev-parse", "--verify", ref)
	if readErr == nil && strings.TrimSpace(string(current)) != expected {
		return fmt.Errorf("%w: %s", ErrStale, ref)
	}
	return err
}
