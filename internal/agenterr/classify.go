package agenterr

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// classifyResult is the internal result from a per-backend classifier.
type classifyResult struct {
	Class      ErrorClass
	Message    string
	RetryAfter time.Duration
}

// errorPattern defines a single regex→ErrorClass mapping used by all backends.
type errorPattern struct {
	re    *regexp.Regexp
	class ErrorClass
	msg   string
}

// retryAfterRe extracts a Retry-After header value from log/output text.
// Shared across all backends — the format is provider-independent.
var retryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

// classifyWithPatterns runs the shared classification logic against a pattern
// table. Every per-backend classifier delegates to this function.
func classifyWithPatterns(text string, patterns []errorPattern) *classifyResult {
	if text == "" {
		return nil
	}
	for _, p := range patterns {
		if p.re.MatchString(text) {
			r := &classifyResult{
				Class:   p.class,
				Message: p.msg,
			}
			if p.class == RateLimited {
				if m := retryAfterRe.FindStringSubmatch(text); len(m) > 1 {
					if secs, err := strconv.Atoi(m[1]); err == nil {
						r.RetryAfter = time.Duration(secs) * time.Second
					}
				}
			}
			return r
		}
	}
	return nil
}

// ClassifyFromLog reads the tail of an agent log file and classifies the error.
// It never returns nil — an Unknown classification is returned if nothing matches.
func ClassifyFromLog(logPath string, exitCode int, backend string) *AgentError {
	logTail, _ := readLogTail(logPath, 100)
	return classifyFromText(logTail, exitCode, backend)
}

// ClassifyFromOutput classifies an error from raw output text (e.g. captured
// stream-json lines) instead of reading from a log file. Same classification
// logic as ClassifyFromLog. Never returns nil.
func ClassifyFromOutput(output string, exitCode int, backend string) *AgentError {
	return classifyFromText(output, exitCode, backend)
}

// classifyFromText is the shared classification implementation used by both
// ClassifyFromLog and ClassifyFromOutput.
func classifyFromText(text string, exitCode int, backend string) *AgentError {
	now := time.Now()

	var result *classifyResult
	switch backend {
	case "claude":
		result = classifyClaude(text)
	case "codex":
		result = classifyCodex(text)
	case "cursor":
		result = classifyCursor(text)
	case "gemini":
		result = classifyGemini(text)
	case "opencode":
		result = classifyOpenCode(text)
	}

	if result == nil {
		class := classifyByExitCode(exitCode)
		result = &classifyResult{
			Class:   class,
			Message: classifyByExitCodeMessage(exitCode),
		}
	}

	return &AgentError{
		Class:      result.Class,
		ExitCode:   exitCode,
		Message:    result.Message,
		RawOutput:  text,
		Backend:    backend,
		RetryAfter: result.RetryAfter,
		Timestamp:  now,
	}
}

// maxLogTailBytes is the maximum number of bytes to read from the end of a log file.
const maxLogTailBytes int64 = 64 * 1024

// readLogTail reads the last maxLines lines from a file, reading at most
// maxLogTailBytes from the end. Returns empty string on any error.
func readLogTail(path string, maxLines int) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}

	size := stat.Size()
	if size == 0 {
		return "", nil
	}

	readSize := maxLogTailBytes
	if size < readSize {
		readSize = size
	}

	offset := size - readSize

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}

	buf := make([]byte, int(readSize))
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}
	buf = buf[:n]

	// Take the last maxLines lines.
	lines := bytes.Split(buf, []byte("\n"))
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return string(bytes.Join(lines, []byte("\n"))), nil
}

// classifyByExitCode provides a generic fallback classification based on
// the process exit code when no log pattern matches.
func classifyByExitCode(exitCode int) ErrorClass {
	switch exitCode {
	case 137: // 128+9 = SIGKILL (OOM killer or watchdog)
		return Timeout
	case 143: // 128+15 = SIGTERM (graceful shutdown)
		return Transient
	default:
		return Unknown
	}
}

func classifyByExitCodeMessage(exitCode int) string {
	switch exitCode {
	case 137:
		return "process killed by signal 9 (SIGKILL), exit code 137"
	case 143:
		return "process terminated by signal 15 (SIGTERM), exit code 143"
	default:
		return fmt.Sprintf("unclassified error (exit code %d)", exitCode)
	}
}
