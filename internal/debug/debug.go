// Package debug holds the loom CLI's process-global output-verbosity switches:
// LOOM_DEBUG (or SetVerbose) gates Logf/Printf diagnostics, and quiet mode
// suppresses PrintNormal. Only internal/rpc currently reads it, via Logf for
// daemon-connection tracing; the verbose/quiet setters have no live callers.
package debug

import (
	"fmt"
	"os"
)

var (
	enabled     = os.Getenv("LOOM_DEBUG") != ""
	verboseMode = false
	quietMode   = false
)

func Enabled() bool {
	return enabled || verboseMode
}

// SetVerbose enables verbose/debug output
func SetVerbose(verbose bool) {
	verboseMode = verbose
}

// SetQuiet enables quiet mode (suppress non-essential output)
func SetQuiet(quiet bool) {
	quietMode = quiet
}

// IsQuiet returns true if quiet mode is enabled
func IsQuiet() bool {
	return quietMode
}

func Logf(format string, args ...interface{}) {
	if enabled || verboseMode {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func Printf(format string, args ...interface{}) {
	if enabled || verboseMode {
		fmt.Printf(format, args...)
	}
}

// PrintNormal prints output unless quiet mode is enabled
// Use this for normal informational output that should be suppressed in quiet mode
func PrintNormal(format string, args ...interface{}) {
	if !quietMode {
		fmt.Printf(format, args...)
	}
}

// PrintlnNormal prints a line unless quiet mode is enabled
func PrintlnNormal(args ...interface{}) {
	if !quietMode {
		fmt.Println(args...)
	}
}
