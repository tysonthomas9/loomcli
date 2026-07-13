package git

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

const maxHistoryDiffBytes int64 = 2 * 1024 * 1024

const emptyTreeObjectID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// FileContentAtRev contains bounded content returned from a git revision.
type FileContentAtRev struct {
	Content   []byte
	Size      int64
	Truncated bool
}

// ShowFileAtRev returns file content from a git revision, capped to maxBytes.
func ShowFileAtRev(dir, rev, path string, maxBytes int64) (*FileContentAtRev, error) {
	if err := validateHistoryRev(rev); err != nil {
		return nil, err
	}
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return nil, err
	}
	spec := rev + ":" + cleanPath
	sizeOut, err := cli.RunGitCommand(dir, "cat-file", "-s", spec)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse git object size: %w", err)
	}
	content, truncated, err := runGitCatFileBlobLimited(dir, spec, maxBytes, size)
	if err != nil {
		return nil, err
	}
	return &FileContentAtRev{Content: content, Size: size, Truncated: truncated}, nil
}

// DiffFile returns a unified diff for one checkout-relative file path. When to
// is empty, it compares from against the working tree.
func DiffFile(dir, path, from, to string) (string, error) {
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return "", err
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		from = "HEAD"
	}
	if err := validateHistoryRev(from); err != nil {
		return "", err
	}
	args := []string{"diff"}
	if to != "" {
		if err := validateHistoryRev(to); err != nil {
			return "", err
		}
		from, err = normalizeDiffBaseForRootCommit(dir, from)
		if err != nil {
			return "", err
		}
		args = append(args, from+".."+to)
	} else {
		args = append(args, from)
	}
	args = append(args, "--", cleanPath)
	return runGitCommandOutputLimited(dir, args, maxHistoryDiffBytes)
}

func normalizeDiffBaseForRootCommit(dir, from string) (string, error) {
	if !strings.HasSuffix(from, "^") {
		return from, nil
	}
	if _, err := cli.RunGitCommand(dir, "rev-parse", "--verify", from+"^{commit}"); err == nil {
		return from, nil
	}
	commit := strings.TrimSuffix(from, "^")
	out, err := cli.RunGitCommand(dir, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return "", err
	}
	if len(strings.Fields(out)) == 1 {
		return emptyTreeObjectID, nil
	}
	return from, nil
}

// LogFile returns bounded git log output for one checkout-relative file path.
func LogFile(dir, path string, limit int) (string, error) {
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = 100
	}
	return cli.RunGitCommand(
		dir,
		"log",
		"--follow",
		"-n",
		strconv.Itoa(limit),
		"--format=%H%x1f%an%x1f%at%x1f%s%x1e",
		"--",
		cleanPath,
	)
}

// BlamePorcelain returns git blame --porcelain output for one file path.
func BlamePorcelain(dir, path string) (string, error) {
	cleanPath, err := cleanGitFilePath(path)
	if err != nil {
		return "", err
	}
	return cli.RunGitCommand(dir, "blame", "--porcelain", "--", cleanPath)
}

func cleanGitFilePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be repository-relative")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("path must stay within the repository")
	}
	return clean, nil
}

func validateHistoryRev(rev string) error {
	if rev == "" {
		return fmt.Errorf("git revision is required")
	}
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("invalid git revision %q", rev)
	}
	if strings.Contains(rev, "..") || strings.Contains(rev, ":") || strings.ContainsAny(rev, " \t\r\n\x00") {
		return fmt.Errorf("invalid git revision %q", rev)
	}
	for _, r := range rev {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '/' || r == '.' || r == '~' || r == '^' {
			continue
		}
		return fmt.Errorf("invalid git revision %q", rev)
	}
	return nil
}

func runGitCatFileBlobLimited(dir, spec string, maxBytes, size int64) ([]byte, bool, error) {
	if maxBytes <= 0 {
		return nil, false, fmt.Errorf("maxBytes must be positive")
	}
	cmd := exec.Command("git", "cat-file", "blob", spec) //nolint:gosec // args are validated above.
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, fmt.Errorf("git cat-file stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, false, fmt.Errorf("git cat-file start: %w", err)
	}
	truncated := size > maxBytes
	limit := maxBytes
	if !truncated {
		limit = size
	}
	content, readErr := io.ReadAll(io.LimitReader(stdout, limit))
	if truncated && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, false, fmt.Errorf("git cat-file read: %w", readErr)
	}
	if waitErr != nil && !truncated {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return nil, false, fmt.Errorf("git cat-file failed: %s", msg)
	}
	return content, truncated, nil
}

func runGitCommandOutputLimited(dir string, args []string, maxBytes int64) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // Args are fixed git verbs plus validated revisions and paths.
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	content, readErr := io.ReadAll(io.LimitReader(stdout, maxBytes+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", readErr
	}
	if int64(len(content)) > maxBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", fmt.Errorf("git output exceeds %d byte limit", maxBytes)
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}
	return string(content), nil
}
