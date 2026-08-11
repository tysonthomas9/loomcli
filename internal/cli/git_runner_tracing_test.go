package cli

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// fakeGitRunner is a minimal GitRunner stand-in used to drive the tracing
// wrapper without spawning a real git process.
type fakeGitRunner struct {
	runResult CommandResult
	runErr    error
	gotDir    string
	gotArgs   []string
}

func (f *fakeGitRunner) Run(dir string, args ...string) CommandResult {
	f.gotDir = dir
	f.gotArgs = append([]string(nil), args...)
	return f.runResult
}

func (f *fakeGitRunner) RunWithOutput(dir string, args ...string) error {
	f.gotDir = dir
	f.gotArgs = append([]string(nil), args...)
	return f.runErr
}

// installInMemoryExporter swaps the global TracerProvider for an in-memory
// recorder for the duration of the test and returns the recorder.
func installInMemoryExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
	})
	return exp
}

// TestTracedGitRunner_Run_EmitsSpan_WithBoundedAttributes asserts the span
// name is `git.<verb>` and that the recorded attributes are the bounded,
// PII-safe set declared in docs/observability/tracing-contract.md §4.2.
// Specifically, the raw arg list must NOT appear as a span attribute.
func TestTracedGitRunner_Run_EmitsSpan_WithBoundedAttributes(t *testing.T) {
	exp := installInMemoryExporter(t)

	inner := &fakeGitRunner{runResult: CommandResult{Stdout: "M file.go\n"}}
	traced := wrapGitRunnerWithTracing(t.Context(), inner)

	res := traced.Run("/tmp/wt", "status", "--porcelain", "feature/secret-branch-name")
	if res.Err != nil {
		t.Fatalf("Run returned error: %v", res.Err)
	}
	if inner.gotDir != "/tmp/wt" || len(inner.gotArgs) != 3 {
		t.Fatalf("inner runner not called through: dir=%q args=%v", inner.gotDir, inner.gotArgs)
	}

	got := exp.GetSpans()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 span, got %d", len(got))
	}
	s := got[0]
	if s.Name != "git.status" {
		t.Errorf("span name: got %q want %q", s.Name, "git.status")
	}

	// Allowed attributes only. No raw args / no branch name leak.
	allowed := map[string]bool{
		"git.command":     true,
		"git.arg_count":   true,
		"git.exit_code":   true,
		"git.duration_ms": true,
	}
	for _, a := range s.Attributes {
		key := string(a.Key)
		if !allowed[key] {
			t.Errorf("unexpected span attribute %q (value=%v); contract forbids non-allowlisted keys", key, a.Value.AsInterface())
		}
		// Defensively assert the branch name string never leaks into any
		// attribute value.
		if v := a.Value.AsString(); v == "feature/secret-branch-name" {
			t.Errorf("attribute %q leaked the raw arg %q", key, v)
		}
	}
}

// TestTracedGitRunner_RunWithOutput_RecordsErrorAndExitCode asserts that on
// a non-zero git exit, the span records the error and the categorized
// status without leaking output.
func TestTracedGitRunner_RunWithOutput_RecordsErrorAndExitCode(t *testing.T) {
	exp := installInMemoryExporter(t)

	inner := &fakeGitRunner{runErr: errors.New("git push failed: remote rejected")}
	traced := wrapGitRunnerWithTracing(t.Context(), inner)

	if err := traced.RunWithOutput("/tmp/wt", "push", "origin", "feature/x"); err == nil {
		t.Fatal("expected error from RunWithOutput, got nil")
	}

	got := exp.GetSpans()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 span, got %d", len(got))
	}
	s := got[0]
	if s.Name != "git.push" {
		t.Errorf("span name: got %q want %q", s.Name, "git.push")
	}

	// Assert the bounded exit_code attribute is recorded (-1 for non-exitCoder).
	var sawExit, sawCmd bool
	for _, a := range s.Attributes {
		switch string(a.Key) {
		case "git.exit_code":
			sawExit = true
			if a.Value.AsInt64() != -1 {
				t.Errorf("git.exit_code: got %d want -1 (non-exitCoder error)", a.Value.AsInt64())
			}
		case "git.command":
			sawCmd = true
			if a.Value.AsString() != "push" {
				t.Errorf("git.command: got %q want %q", a.Value.AsString(), "push")
			}
		}
	}
	if !sawExit {
		t.Error("expected git.exit_code attribute to be recorded on error")
	}
	if !sawCmd {
		t.Error("expected git.command attribute to be recorded")
	}
	if len(s.Events) == 0 {
		t.Error("expected RecordError to add an exception event")
	}
	if s.Status.Code.String() != "Error" {
		t.Errorf("span status: got %s want Error", s.Status.Code)
	}
}

// TestGitSpanName_FallsBackToUnknown asserts the span name allowlist is not
// breached even when callers pass surprising leading args (flags, refs,
// uppercase).
func TestGitSpanName_FallsBackToUnknown(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "git.unknown"},
		{[]string{}, "git.unknown"},
		{[]string{""}, "git.unknown"},
		{[]string{"-c"}, "git.unknown"},
		{[]string{"--no-pager"}, "git.unknown"},
		{[]string{"PUSH"}, "git.push"},             // uppercase canonicalized to lowercase
		{[]string{"push"}, "git.push"},             // canonical
		{[]string{"push;rm -rf /"}, "git.unknown"}, // semicolon outside [a-z_-] set
		{[]string{"rev-parse"}, "git.rev-parse"},   // dash allowed
		{[]string{"checkout", "feature/x"}, "git.checkout"},
	}
	for _, tc := range cases {
		got := gitSpanName(tc.args)
		if got != tc.want {
			t.Errorf("gitSpanName(%v): got %q want %q", tc.args, got, tc.want)
		}
	}
}

// TestExitCodeFor_HandlesAllShapes asserts the exitCodeFor helper returns
// the expected bounded numeric for the three error shapes the wrapper sees.
func TestExitCodeFor_HandlesAllShapes(t *testing.T) {
	if got := exitCodeFor(nil); got != 0 {
		t.Errorf("nil error: got %d want 0", got)
	}
	if got := exitCodeFor(errors.New("plain error")); got != -1 {
		t.Errorf("plain error: got %d want -1", got)
	}
	if got := exitCodeFor(stubExitCoder{code: 42}); got != 42 {
		t.Errorf("exitCoder: got %d want 42", got)
	}
}

type stubExitCoder struct{ code int }

func (s stubExitCoder) Error() string { return "stub" }
func (s stubExitCoder) ExitCode() int { return s.code }
