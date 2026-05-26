// Package backendcheck is the single seam between loom code and the
// in-tree harness CLI discovery primitive. It exists as its own package
// (rather than a var in internal/cli) to keep internal/cli's import
// fanout and file count under the project's per-package gates.
package backendcheck

import "github.com/olesho/harness-wrapper/pkg/discovery"

// CheckBackend reports whether a named backend's CLI is installed on
// PATH and, when a version probe is registered, at what version.
//
// Production code reads CheckBackend; tests reassign it to inject a
// fake without spawning subprocesses or mutating PATH:
//
//	prev := backendcheck.CheckBackend
//	backendcheck.CheckBackend = func(string) (discovery.Info, error) { ... }
//	t.Cleanup(func() { backendcheck.CheckBackend = prev })
var CheckBackend = discovery.Lookup
