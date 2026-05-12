// Package runtimectx holds the process-wide root context that helper
// functions inherit from when they don't have access to a request-scoped
// ctx. Living here (rather than in internal/cli/cmdstore) lets infra-layer
// packages reach the root context without violating the cli↔infra
// dependency direction enforced by depguard.
package runtimectx

import "context"

// rootCtx is the process-wide parent context. Set once by SetRootContext
// before rootCmd.Execute runs; read by RootContext so any trace span (or
// other context-attached value) installed at the CLI entry point is
// inherited by every subcommand without each subcommand threading
// cmd.Context() down through every helper.
var rootCtx context.Context = context.Background()

// SetRootContext installs the parent context. Call once from CLI entry
// (cli.Execute) before dispatching to Cobra. Subsequent reads via
// RootContext return this ctx (and through it, any trace span attached
// at CLI startup).
func SetRootContext(ctx context.Context) {
	if ctx != nil {
		rootCtx = ctx
	}
}

// RootContext returns the process-wide root context. Prefer threading
// ctx through call sites where possible; this exists for the long tail
// of utility helpers where that's not practical.
func RootContext() context.Context {
	return rootCtx
}
