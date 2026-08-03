package local

import (
	"testing"
)

// The local runtime re-execs this same binary as `loom serve` or
// `loom local service`. Under `go test`, os.Executable() is local.test, so an
// unguarded re-exec re-runs the whole suite and re-enters the spawn path.

func TestGuardLoomReexecRejectsTestBinary(t *testing.T) {
	bombs := []string{"local.test", "/tmp/go-build123/b001/local.test", "/x/y/supervisor.test"}
	for _, exe := range bombs {
		if err := guardLoomReexec(exe); err == nil {
			t.Errorf("guardLoomReexec(%q) = nil, want fork-bomb error", exe)
		}
	}
	real := []string{"loom", "/usr/local/bin/loom", "/x/y/loom"}
	for _, exe := range real {
		if err := guardLoomReexec(exe); err != nil {
			t.Errorf("guardLoomReexec(%q) = %v, want nil", exe, err)
		}
	}
}
