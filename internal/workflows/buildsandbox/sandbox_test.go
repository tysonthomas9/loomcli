package buildsandbox

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestRunUsesAllowlistAndRedactsDiagnostics(t *testing.T) {
	result := Run(context.Background(), Request{Command: []string{"/bin/sh", "-c", "printf '%s' \"$SECRET_TOKEN\""}, Env: map[string]string{"PATH": os.Getenv("PATH"), "SECRET_TOKEN": "not-for-child"}})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Output != "" {
		t.Fatalf("secret reached child/output: %q", result.Output)
	}
}

func TestRunRejectsConcurrentBuild(t *testing.T) {
	// A deterministic lock test does not need to race the first process: hold
	// the package mutex exactly as the runner does and verify the public result.
	running.Lock()
	result := Run(context.Background(), Request{Command: []string{"/bin/true"}})
	running.Unlock()
	if result.Err != ErrBuildInProgress {
		t.Fatalf("err=%v, want %v", result.Err, ErrBuildInProgress)
	}
}

func TestRedactRemovesSensitiveEnvironmentValues(t *testing.T) {
	t.Setenv("BUILD_SECRET_TOKEN", "s3cret")
	if got := Redact("token=s3cret"); strings.Contains(got, "s3cret") {
		t.Fatalf("unredacted output: %q", got)
	}
}
