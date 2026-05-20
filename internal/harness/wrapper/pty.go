package wrapper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// waitResult bundles the outcome of cmd.Wait so the supervisor can
// receive it on a single channel.
type waitResult struct {
	err     error
	endedAt time.Time
}

// copyPTYOutput reads bytes from the PTY master and writes them to the
// caller's stdout, recording the timestamp of the most recent byte for
// idle detection.
func copyPTYOutput(src io.Reader, dst io.Writer, lastOutput *atomic.Int64, recentOutput *recentOutputBuffer) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			lastOutput.Store(time.Now().UnixNano())
			recentOutput.Write(buf[:n])
			_, _ = dst.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// classifyExit maps a finished os/exec process state into the wrapper's
// normalized status. When ctx was cancelled the wrapper considers the
// run interrupted regardless of how the child happened to exit.
func classifyExit(state *os.ProcessState, _ error, ctxErr error, recentOutput string) (Status, int, string, string) {
	if state == nil {
		return StatusUnknown, -1, "", "process state unavailable"
	}
	if ctxErr != nil {
		return StatusInterrupted, state.ExitCode(), signalFromState(state), "context cancelled"
	}
	if state.Success() {
		return StatusIdle, 0, "", ""
	}
	if state.Exited() {
		if isCostOrQuotaLimited(recentOutput) {
			return StatusBlockedByCost, state.ExitCode(), "", "cost, quota, or rate limit detected"
		}
		return StatusFailed, state.ExitCode(), "", fmt.Sprintf("exit code %d", state.ExitCode())
	}
	signal := signalFromState(state)
	return StatusInterrupted, state.ExitCode(), signal, fmt.Sprintf("terminated by %s", signal)
}

func isCostOrQuotaLimited(output string) bool {
	normalized := strings.ToLower(stripANSIEscapes(output))
	patterns := []string{
		"blocked by cost",
		"cost limit",
		"quota exceeded",
		"rate limit",
		"rate-limit",
		"usage limit",
		"you've hit your limit",
		"you have hit your limit",
		"limit resets",
		"resets at",
		"extra usage",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func stripANSIEscapes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEscape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inEscape {
			if c >= '@' && c <= '~' {
				inEscape = false
			}
			continue
		}
		if c == 0x1b {
			inEscape = true
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func signalFromState(state *os.ProcessState) string {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return status.Signal().String()
	}
	return ""
}

// isBinaryNotFound reports whether err from pty.Start is a result of the
// underlying binary not existing.
func isBinaryNotFound(err error) bool {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return errors.Is(execErr.Err, exec.ErrNotFound) || errors.Is(execErr.Err, os.ErrNotExist)
	}
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}
