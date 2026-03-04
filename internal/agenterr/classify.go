package agenterr

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"time"
)

// classifyResult is the internal result from a per-backend classifier.
type classifyResult struct {
	Class      ErrorClass
	Message    string
	RetryAfter time.Duration
}

// ClassifyFromLog reads the tail of an agent log file and classifies the error.
// It never returns nil — an Unknown classification is returned if nothing matches.
func ClassifyFromLog(logPath string, exitCode int, backend string) *AgentError {
	now := time.Now()

	logTail, _ := readLogTail(logPath, 100)

	var result *classifyResult
	switch backend {
	case "claude":
		result = classifyClaude(logTail)
	case "codex":
		result = classifyCodex(logTail)
	case "opencode":
		result = classifyOpenCode(logTail)
	}

	if result == nil {
		class := classifyByExitCode(exitCode)
		result = &classifyResult{
			Class:   class,
			Message: classifyByExitCodeMessage(class),
		}
	}

	return &AgentError{
		Class:      result.Class,
		ExitCode:   exitCode,
		Message:    result.Message,
		RawOutput:  logTail,
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

func classifyByExitCodeMessage(class ErrorClass) string {
	switch class {
	case Timeout:
		return "process killed by signal 9 (SIGKILL)"
	case Transient:
		return "process terminated by signal 15 (SIGTERM)"
	default:
		return "unclassified error"
	}
}
