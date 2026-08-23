package buildsandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	cmd := exec.Command(r.Command[0], r.Command[1:]...) //nolint:gosec // command is the resolved authoring toolchain, not workflow source.
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
