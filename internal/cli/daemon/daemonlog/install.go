package daemonlog

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// This package imports internal/cli and stdlib only. It must NOT import
// internal/cli/daemon or its supervisor package — both import this one, and
// either edge would be an import cycle.

// Install points the process-wide slog default at a daemon-owned log file
// under logDir, teeing to os.Stderr so the process manager keeps seeing every
// line. The returned Sink is the caller's to Close on shutdown.
//
// The error is returned for reporting only: the Sink is always usable (it
// degrades to stderr-only), and a daemon must never refuse to supervise
// because it could not open its own log.
func Install(logDir, workspaceID string) (*Sink, error) {
	dir := logDir
	if workspaceID != "" {
		dir = filepath.Join(dir, workspaceID)
	}
	path := filepath.Join(dir, "daemon.log")

	sink := New(path, os.Stderr)
	slog.SetDefault(slog.New(cli.NewSlogHandler(sink, cli.LogFormat())))
	slog.SetLogLoggerLevel(slog.LevelInfo)

	if h := sink.Health(); !h.Healthy {
		return sink, &InstallError{Path: path, Msg: h.LastErrMsg}
	}
	return sink, nil
}

// InstallError reports that the daemon log file could not be opened. The Sink
// returned alongside it still works as a stderr-only writer.
type InstallError struct {
	Path string
	Msg  string
}

func (e *InstallError) Error() string {
	return "daemon log " + e.Path + " unavailable: " + e.Msg
}

// DaemonLogPath resolves the daemon's own log path the same way Install does,
// for callers that need to name the file without installing anything (the
// doctor check, for one). It applies the same logDir defaulting and
// project-relative joining as the agent log path resolution.
func DaemonLogPath(projectDir string, cfg *cfgpkg.DaemonConfig, workspaceID string) string {
	logDir := ""
	if cfg != nil {
		logDir = cfg.Daemon.LogDir
	}
	if logDir == "" {
		logDir = ".loom/logs"
	}
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(projectDir, logDir)
	}
	if workspaceID != "" {
		logDir = filepath.Join(logDir, workspaceID)
	}
	return filepath.Join(logDir, "daemon.log")
}
