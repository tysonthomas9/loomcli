package cli

import (
	"fmt"
	"log/slog"
	"os"
)

// logFormat and logOutput are bound to persistent CLI flags in root.go.
var (
	logFormat string
	logOutput string
)

// InitLogger configures the process-wide default slog logger.
// format: "text" (default) or "json"
// output: "stderr" (default) or a file path
func InitLogger(format, output string) error {
	// Determine writer
	var w *os.File
	switch output {
	case "", "stderr":
		w = os.Stderr
	default:
		f, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600) // #nosec G304 - path from --log-output CLI flag
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %w", output, err)
		}
		w = f
	}

	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		// "text" or any unrecognized value defaults to text
		handler = slog.NewTextHandler(w, opts)
	}

	slog.SetDefault(slog.New(handler))

	// Bridge existing log.Printf calls through slog
	slog.SetLogLoggerLevel(slog.LevelInfo)

	return nil
}
