package agenterr

import (
	"bytes"
	"io"
	"os"
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

	logTail, _ := readLogTail(logPath, 100, 64*1024)

	var result *classifyResult
	switch backend {
	case "claude":
		result = classifyClaude(logTail, exitCode)
	case "codex":
		result = classifyCodex(logTail, exitCode)
	case "opencode":
		result = classifyOpenCode(logTail, exitCode)
	}

	if result == nil {
		class := classifyByExitCode(exitCode)
		result = &classifyResult{
			Class:   class,
			Message: classifyByExitCodeMessage(exitCode, class),
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

// readLogTail reads the last maxLines lines from a file, reading at most
// maxBytes from the end. Returns empty string on any error.
func readLogTail(path string, maxLines int, maxBytes int64) (string, error) {
	f, err := os.Open(path)
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

	readSize := maxBytes
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

func classifyByExitCodeMessage(exitCode int, class ErrorClass) string {
	switch class {
	case Timeout:
		return "process killed by signal 9 (SIGKILL)"
	case Transient:
		return "process terminated by signal 15 (SIGTERM)"
	default:
		return "unclassified error"
	}
}
