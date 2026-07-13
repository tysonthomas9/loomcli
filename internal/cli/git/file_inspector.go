package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	fileStatusTimeout = 3 * time.Second
	fileStatusBytes   = 8 * 1024 * 1024
	fileStatusEntries = 50_000
	fileDiffTimeout   = 5 * time.Second
	fileDiffBytes     = 2 * 1024 * 1024
	fileLogTimeout    = 5 * time.Second
	fileLogBytes      = 2 * 1024 * 1024
	fileLogEntries    = 100
	fileBlameTimeout  = 5 * time.Second
	fileBlameBytes    = 4 * 1024 * 1024
	fileShowTimeout   = 3 * time.Second
	fileShowBytes     = 1 * 1024 * 1024
	gitStderrBytes    = 64 * 1024
)

// InspectorError classifies failures from bounded file-browser Git commands.
type InspectorError struct {
	Operation string
	Kind      string
	Stderr    string
	Err       error
}

func (e *InspectorError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git %s %s: %v", e.Operation, e.Stderr, e.Err)
	}
	return fmt.Sprintf("git %s: %v", e.Operation, e.Err)
}

func (e *InspectorError) Unwrap() error { return e.Err }

// InspectionKind allows higher layers to preserve error categories without
// importing this package through the web UI dependency boundary.
func (e *InspectorError) InspectionKind() string { return e.Kind }

func validationInspectorError(operation string, err error) error {
	return &InspectorError{Operation: operation, Kind: "validation", Err: err}
}

// InspectorTextResult is bounded command output. Partial and LimitHit are set
// when output was clipped without treating the useful prefix as a failure.
type InspectorTextResult struct {
	Output   []byte
	Partial  bool
	LimitHit bool
}

// InspectorStatusResult is NUL-safe porcelain status keyed by destination.
type InspectorStatusResult struct {
	Entries  map[string]string
	Partial  bool
	LimitHit bool
}

// InspectorShowResult is bounded blob content at a revision.
type InspectorShowResult struct {
	Content   []byte
	Size      int64
	Truncated bool
}

// GitInspector runs the file browser's read-only Git operations with isolated
// configuration, bounded resources, and request cancellation.
type GitInspector struct{ binary string }

func NewGitInspector() *GitInspector { return &GitInspector{binary: "git"} }

func (g *GitInspector) Status(ctx context.Context, dir string) (InspectorStatusResult, error) {
	result, err := g.run(ctx, "status", dir, fileStatusTimeout, fileStatusBytes,
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=all")
	parsed := parsePorcelainV1Z(result.Output, fileStatusEntries)
	parsed.Partial = parsed.Partial || result.Partial
	parsed.LimitHit = parsed.LimitHit || result.LimitHit
	return parsed, err
}

func (g *GitInspector) Diff(ctx context.Context, dir, path, from, to string) (InspectorTextResult, error) {
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return InspectorTextResult{}, validationInspectorError("diff", err)
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		from = "HEAD"
	}
	if err := validateHistoryRev(from); err != nil {
		return InspectorTextResult{}, validationInspectorError("diff", err)
	}
	ctx, cancel := context.WithTimeout(ctx, fileDiffTimeout)
	defer cancel()
	args := []string{"diff", "--no-ext-diff", "--no-textconv"}
	if to != "" {
		if err := validateHistoryRev(to); err != nil {
			return InspectorTextResult{}, validationInspectorError("diff", err)
		}
		from, err = g.normalizeDiffBase(ctx, dir, from)
		if err != nil {
			return InspectorTextResult{}, err
		}
		args = append(args, from+".."+to)
	} else {
		args = append(args, from)
	}
	args = append(args, "--", cleanPath)
	return g.runWithContext(ctx, "diff", dir, fileDiffBytes, args...)
}

func (g *GitInspector) Log(ctx context.Context, dir, path string, limit int) (InspectorTextResult, error) {
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return InspectorTextResult{}, validationInspectorError("log", err)
	}
	if limit <= 0 || limit > fileLogEntries {
		limit = fileLogEntries
	}
	return g.run(ctx, "log", dir, fileLogTimeout, fileLogBytes,
		"log", "--follow", "-n", strconv.Itoa(limit), "--format=%H%x00%an%x00%at%x00%s%x00", "--", cleanPath)
}

func (g *GitInspector) Blame(ctx context.Context, dir, path string) (InspectorTextResult, error) {
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return InspectorTextResult{}, validationInspectorError("blame", err)
	}
	return g.run(ctx, "blame", dir, fileBlameTimeout, fileBlameBytes,
		"blame", "--porcelain", "--no-progress", "--no-textconv", "--", cleanPath)
}

func (g *GitInspector) Show(ctx context.Context, dir, rev, path string, maxBytes int64) (InspectorShowResult, error) {
	if err := validateHistoryRev(strings.TrimSpace(rev)); err != nil {
		return InspectorShowResult{}, validationInspectorError("show", err)
	}
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return InspectorShowResult{}, validationInspectorError("show", err)
	}
	if maxBytes <= 0 || maxBytes > fileShowBytes {
		maxBytes = fileShowBytes
	}
	ctx, cancel := context.WithTimeout(ctx, fileShowTimeout)
	defer cancel()
	spec := strings.TrimSpace(rev) + ":" + cleanPath
	sizeResult, err := g.runWithContext(ctx, "show-size", dir, 128, "cat-file", "-s", spec)
	if err != nil {
		return InspectorShowResult{}, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeResult.Output)), 10, 64)
	if err != nil {
		return InspectorShowResult{}, fmt.Errorf("parse git object size: %w", err)
	}
	content, err := g.runWithContext(ctx, "show", dir, maxBytes, "cat-file", "blob", spec)
	return InspectorShowResult{Content: content.Output, Size: size, Truncated: content.LimitHit || size > maxBytes}, err
}

func (g *GitInspector) CurrentBranch(ctx context.Context, dir string) (string, error) {
	result, err := g.run(ctx, "branch", dir, fileShowTimeout, 4096, "branch", "--show-current")
	return strings.TrimSpace(string(result.Output)), err
}

func (g *GitInspector) normalizeDiffBase(ctx context.Context, dir, from string) (string, error) {
	if !strings.HasSuffix(from, "^") {
		return from, nil
	}
	if _, err := g.runWithContext(ctx, "verify", dir, 4096, "rev-parse", "--verify", from+"^{commit}"); err == nil {
		return from, nil
	}
	commit := strings.TrimSuffix(from, "^")
	result, err := g.runWithContext(ctx, "parents", dir, 4096, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return "", err
	}
	if len(strings.Fields(string(result.Output))) == 1 {
		return emptyTreeObjectID, nil
	}
	return from, nil
}

func (g *GitInspector) run(ctx context.Context, operation, dir string, timeout time.Duration, maxBytes int64, args ...string) (InspectorTextResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return g.runWithContext(ctx, operation, dir, maxBytes, args...)
}

func (g *GitInspector) runWithContext(ctx context.Context, operation, dir string, maxBytes int64, args ...string) (InspectorTextResult, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	config := []string{"-c", "core.fsmonitor=false", "-c", "submodule.recurse=false", "-c", "core.hooksPath=" + os.DevNull}
	binary := g.binary
	if binary == "" {
		binary = "git"
	}
	cmd := exec.CommandContext(runCtx, binary, append(config, args...)...) //nolint:gosec // arguments are fixed or validated above.
	cmd.Dir = dir
	cmd.Env = isolatedGitEnv(os.Environ())
	stdout := &cappedBuffer{limit: maxBytes, onLimit: cancel}
	stderr := &cappedBuffer{limit: gitStderrBytes, onLimit: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result := InspectorTextResult{Output: stdout.Bytes(), Partial: stdout.exceeded, LimitHit: stdout.exceeded}
	if stdout.exceeded {
		return result, nil
	}
	if err == nil {
		return result, nil
	}
	kind := "failed"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		kind = "timeout"
	} else if errors.Is(ctx.Err(), context.Canceled) {
		kind = "canceled"
	}
	return result, &InspectorError{Operation: operation, Kind: kind, Stderr: strings.TrimSpace(string(stderr.Bytes())), Err: err}
}

func isolatedGitEnv(env []string) []string {
	out := make([]string, 0, len(env)+6)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		out = append(out, item)
	}
	return append(out,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
	)
}

type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int64
	written  int64
	exceeded bool
	onLimit  func()
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.written
	if remaining > 0 {
		keep := int64(len(p))
		if keep > remaining {
			keep = remaining
		}
		_, _ = b.buf.Write(p[:keep])
		b.written += keep
	}
	if int64(n) > remaining {
		if !b.exceeded {
			b.exceeded = true
			if b.onLimit != nil {
				b.onLimit()
			}
		}
	}
	return n, nil
}

func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

func parsePorcelainV1Z(output []byte, maxEntries int) InspectorStatusResult {
	result := InspectorStatusResult{Entries: make(map[string]string)}
	for offset := 0; offset < len(output); {
		end := bytes.IndexByte(output[offset:], 0)
		if end < 0 {
			result.Partial = true
			break
		}
		record := output[offset : offset+end]
		offset += end + 1
		if len(record) < 4 || record[2] != ' ' {
			result.Partial = true
			continue
		}
		xy := string(record[:2])
		path := string(record[3:])
		if path == "" {
			result.Partial = true
			continue
		}
		if len(result.Entries) >= maxEntries {
			result.Partial, result.LimitHit = true, true
			break
		}
		result.Entries[path] = xy
		if xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C' {
			sourceEnd := bytes.IndexByte(output[offset:], 0)
			if sourceEnd < 0 {
				result.Partial = true
				break
			}
			offset += sourceEnd + 1
		}
	}
	return result
}
