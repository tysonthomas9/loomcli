package svcimpl

import "log/slog"

// logger is the package-level structured logger.
var logger = slog.Default()

// SetLogger overrides the package-level logger.
func SetLogger(l *slog.Logger) {
	if l != nil {
		logger = l
	}
}
