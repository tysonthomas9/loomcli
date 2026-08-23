package buildsandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	cmd := exec.CommandContext(ctx, r.Command[0], r.Command[1:]...)
	cmd.Dir = r.Dir
	cmd.Env = cleanEnv(r.Env)
	out, e := cmd.CombinedOutput()
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
	for k, x := range v {
		if k == "PATH" || k == "HOME" || k == "TMPDIR" || k == "NODE_OPTIONS" || k == "NODE_ENV" || k == "CI" || k == "NO_COLOR" || k == "FORCE_COLOR" || k == "LANG" || k == "TZ" || strings.HasPrefix(k, "LC_") {
			r = append(r, k+"="+x)
		}
	}
	return append(r, "NODE_OPTIONS=--max-old-space-size=2048")
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
