package buildsandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrBuildInProgress = errors.New("build_in_progress")
var ErrTimeout = errors.New("flue_build_timeout")

type Request struct {
	Command []string
	Dir     string
	Env     map[string]string
	Timeout time.Duration
	// Profile, when non-empty, is a macOS Seatbelt (sandbox-exec) profile the
	// command is wrapped in. It is populated only on darwin, only when Mode
	// reports "seatbelt"; on every other platform it stays empty and Run execs
	// the command directly. Callers build it with Profile(ProfileSpec{...}).
	Profile string
}
type Result struct {
	Output string
	Err    error
}

var running sync.Mutex

func Run(ctx context.Context, r Request) Result {
	if !running.TryLock() {
		return Result{Err: ErrBuildInProgress}
	}
	defer running.Unlock()
	if len(r.Command) == 0 {
		return Result{Err: errors.New("flue_build_failed: empty command")}
	}
	if r.Timeout <= 0 {
		r.Timeout = 5 * time.Minute
	}
	ctx, c := context.WithTimeout(ctx, r.Timeout)
	defer c()
	argv := r.Command
	if r.Profile != "" {
		// sandbox-exec -p <profile> <cmd> <args...>: the build (node + Flue CLI)
		// runs confined by the Seatbelt profile — network denied at the BSD-socket
		// layer and writes confined to the build/output roots and TMPDIR.
		argv = append([]string{"/usr/bin/sandbox-exec", "-p", r.Profile}, r.Command...)
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // command is the resolved authoring toolchain wrapped in sandbox-exec, not workflow source.
	cmd.Dir = r.Dir
	cmd.Env = cleanEnv(r.Env)
	prepareCommand(cmd)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if e := cmd.Start(); e != nil {
		return Result{Err: fmt.Errorf("flue_build_failed: %w", e)}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var e error
	select {
	case e = <-done:
	case <-ctx.Done():
		killProcessGroup(cmd.Process.Pid)
		e = <-done
	}
	out := buf.Bytes()
	text := Redact(string(out))
	if len(text) > 32768 {
		text = text[len(text)-32768:]
	}
	if ctx.Err() == context.DeadlineExceeded {
		return Result{Output: text, Err: fmt.Errorf("%w: command exceeded %s", ErrTimeout, r.Timeout)}
	}
	if e != nil {
		return Result{Output: text, Err: fmt.Errorf("flue_build_failed: %w", e)}
	}
	return Result{Output: text}
}
func cleanEnv(v map[string]string) []string {
	r := []string{}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		x := v[k]
		if allowed(k) {
			r = append(r, k+"="+x)
		}
	}
	// NODE_OPTIONS is controlled by Loom even when a caller supplied a value.
	// This prevents a workflow source tree from smuggling arbitrary Node flags.
	// Replace a caller-provided value rather than inheriting it.
	for i := range r {
		if strings.HasPrefix(r[i], "NODE_OPTIONS=") {
			r[i] = "NODE_OPTIONS=--max-old-space-size=2048"
		}
	}
	return r
}

// ProfileSpec parameterizes the Seatbelt profile Run wraps a build in.
type ProfileSpec struct {
	BuildRoot  string // the Flue build project: readable + writable
	OutputRoot string // the dist output dir (may be a sibling of BuildRoot): readable + writable (may be empty)
	TmpDir     string // scratch: readable + writable (may be empty)
	Home       string // used to derive denied credential-store subpaths (may be empty)
}

// SecretReadDenyDirs are the well-known credential stores, relative to HOME, that
// a build must not read. They sit off Node's module-resolution path, so denying
// them costs no build functionality.
var SecretReadDenyDirs = []string{".ssh", ".aws", ".gnupg", ".kube", ".docker", ".config/gcloud", "Library/Keychains", ".npmrc", ".netrc"}

// Profile renders a macOS Seatbelt (sandbox-exec) profile for a workflow build.
//
// The two robust, enforced guarantees are:
//   - network denied at the BSD-socket layer (deny network*) — the exfiltration
//     backstop: even a build that reads a secret cannot send it anywhere;
//   - writes confined to the build root, the dist output dir, and TMPDIR (plus
//     the standard /dev nodes) — the build cannot tamper with the user's files
//     or persist outside its own outputs.
//
// Reads stay allow-default on purpose: Node's module resolution stats
// node_modules up every ancestor directory (through HOME for a project checked
// out under HOME), so a read jail there breaks real builds. As defense in depth
// the well-known credential stores under HOME are denied. This is a layer
// beneath human approval, not a hermetic jail; Mach/XPC access is a documented
// residual of (allow default).
func Profile(spec ProfileSpec) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")
	b.WriteString("(deny network*)\n")
	b.WriteString("(deny file-write*)\n")
	writeAllow := func(path string) {
		if p := canonicalProfilePath(path); p != "" {
			fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", quoteProfile(p))
		}
	}
	writeAllow(spec.BuildRoot)
	writeAllow(spec.OutputRoot)
	writeAllow(spec.TmpDir)
	b.WriteString("(allow file-write-data (literal \"/dev/null\") (literal \"/dev/zero\") (literal \"/dev/random\") (literal \"/dev/urandom\") (literal \"/dev/dtracehelper\") (literal \"/dev/tty\"))\n")
	if h := canonicalProfilePath(spec.Home); h != "" {
		for _, rel := range SecretReadDenyDirs {
			fmt.Fprintf(&b, "(deny file-read* (subpath %s))\n", quoteProfile(filepath.Join(h, rel)))
		}
	}
	return b.String()
}

func canonicalProfilePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	return canonicalAbs(filepath.Clean(p))
}

// canonicalAbs resolves symlinks so a Seatbelt subpath rule matches the
// filesystem-canonical path the kernel checks — e.g. /var/folders/... must
// become /private/var/folders/... or the rule never matches. When the leaf does
// not exist yet (a build output dir created during the build), it resolves the
// deepest existing ancestor and re-appends the remainder.
func canonicalAbs(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(canonicalAbs(parent), filepath.Base(p))
}

func quoteProfile(s string) string {
	// Seatbelt string literals are double-quoted; backslash and double-quote are
	// the only characters that must be escaped.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

func allowed(k string) bool {
	return k == "PATH" || k == "HOME" || k == "TMPDIR" || k == "NODE_OPTIONS" || k == "NODE_ENV" || k == "CI" || k == "NO_COLOR" || k == "FORCE_COLOR" || k == "LANG" || k == "TZ" || strings.HasPrefix(k, "LC_") || (k == "DEBUG" && os.Getenv("LOOM_WORKFLOW_BUILD_DEBUG") == "1")
}
func Redact(v string) string {
	for _, x := range os.Environ() {
		k, s, ok := strings.Cut(x, "=")
		if ok && len(s) >= 4 && sensitive(k) {
			v = strings.ReplaceAll(v, s, "[redacted]")
		}
	}
	return v
}
func sensitive(k string) bool {
	k = strings.ToUpper(k)
	for _, x := range []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "API_KEY", "PRIVATE_KEY", "PROXY"} {
		if strings.Contains(k, x) {
			return true
		}
	}
	return false
}
