package webui

import "log/slog"

// logger is the package-level structured logger, used by all webui code.
// It defaults to slog.Default() and is replaced during server initialization
// via initLogger so callers can inject a custom logger through ServerConfig.Logger.
var logger = slog.Default()

// initLogger sets the package-level logger. If l is nil, slog.Default() is kept.
func initLogger(l *slog.Logger) {
	if l != nil {
		logger = l
	}
}
