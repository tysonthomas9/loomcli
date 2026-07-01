package data

import (
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var hexKeyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// runCreate executes `loom data create` against the stub and returns the
// recorded CreateParams.
func runCreate(t *testing.T, stub *localBackendStub, args ...string) backend.CreateParams {
	t.Helper()
	var params backend.CreateParams
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		cmd := newCreateCmd()
		cmd.SetArgs(args)
		if _, err := captureDataStdout(t, cmd.Execute); err != nil {
			t.Fatalf("create: %v", err)
		}
		if len(stub.calls) == 0 || stub.calls[len(stub.calls)-1].method != "Create" {
			t.Fatalf("calls = %#v, want Create", stub.calls)
		}
		params = stub.calls[len(stub.calls)-1].args.(backend.CreateParams)
	})
	return params
}

func TestCreateCommand_DefaultIdempotencyKey(t *testing.T) {
	args := []string{"--title", "dup me", "--type", "bug", "--description", "same"}
	p1 := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x-1"}}, args...)
	p2 := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x-2"}}, args...)

	if !hexKeyRe.MatchString(p1.IdempotencyKey) {
		t.Fatalf("default key = %q, want 64-char hex", p1.IdempotencyKey)
	}
	if p1.IdempotencyKey != p2.IdempotencyKey {
		t.Errorf("identical invocations must derive the same key: %q vs %q", p1.IdempotencyKey, p2.IdempotencyKey)
	}

	// A different persisted field must change the key…
	p3 := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x-3"}},
		"--title", "dup me", "--type", "bug", "--description", "DIFFERENT")
	if p3.IdempotencyKey == p1.IdempotencyKey {
		t.Error("different description must produce a different key")
	}

	// external_ref is persisted in the fleet create body, so it MUST change
	// the key — it distinguishes the created issue.
	pExtRef := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x-4"}},
		append(args, "--external-ref", "gh-42")...)
	if pExtRef.IdempotencyKey == p1.IdempotencyKey {
		t.Error("persisted external_ref must differentiate the idempotency key")
	}

	// …but fields fleet-db drops from the create body (e.g. estimated-minutes)
	// must NOT — they persist identically, so they must dedup identically.
	pDropped := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x-5"}},
		append(args, "--estimated-minutes", "45")...)
	if pDropped.IdempotencyKey != p1.IdempotencyKey {
		t.Error("fleet-db-dropped fields must not differentiate the idempotency key")
	}
}

func TestCreateCommand_IdempotencyFlags(t *testing.T) {
	base := []string{"--title", "t"}

	if p := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x"}},
		append(base, "--no-idempotency")...); p.IdempotencyKey != "" {
		t.Errorf("--no-idempotency must clear the key, got %q", p.IdempotencyKey)
	}

	if p := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x"}},
		append(base, "--idempotency-key", "my-key")...); p.IdempotencyKey != "my-key" {
		t.Errorf("--idempotency-key override = %q, want my-key", p.IdempotencyKey)
	}

	p := runCreate(t, &localBackendStub{createItem: &backend.IssueData{ID: "x"}},
		append(base, "--force")...)
	if !p.Force {
		t.Error("--force must set CreateParams.Force")
	}
	if p.IdempotencyKey == "" {
		t.Error("--force alone must keep the idempotency key (it only bypasses the soft guard)")
	}
}

func TestCreateCommand_JSONStreamSplit(t *testing.T) {
	stub := &localBackendStub{createItem: &backend.IssueData{ID: "loom-7", Title: "t"}}
	withLocalBackend(t, stub, func() {
		outputFormat = "json"
		cmd := newCreateCmd()
		cmd.SetArgs([]string{"--title", "t"})

		// Capture stderr around the stdout capture.
		origErr := os.Stderr
		rErr, wErr, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		os.Stderr = wErr
		out, runErr := captureDataStdout(t, cmd.Execute)
		_ = wErr.Close()
		os.Stderr = origErr
		errOut, _ := io.ReadAll(rErr)
		_ = rErr.Close()
		if runErr != nil {
			t.Fatalf("create: %v", runErr)
		}

		// stdout stays pure JSON; the stable line goes to stderr.
		if strings.Contains(out, "CREATED ") {
			t.Errorf("json stdout must stay pure JSON, got %q", out)
		}
		if !strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Errorf("json stdout = %q, want JSON object", out)
		}
		if got := strings.TrimSpace(string(errOut)); got != "CREATED loom-7" {
			t.Errorf("stderr = %q, want CREATED loom-7", got)
		}
	})
}

// runClose executes `loom data close <id>` through the data root command
// (closeCmd has a parent, so Execute on the subcommand would run the root
// with os.Args instead).
func runClose(t *testing.T, stub *localBackendStub, id string) (string, error) {
	t.Helper()
	var out string
	var err error
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		dataRootCmd.SetArgs([]string{"close", id})
		out, err = captureDataStdout(t, dataRootCmd.Execute)
	})
	return out, err
}

func TestCloseCommand_AlreadyClosedIsSuccess(t *testing.T) {
	stub := &localBackendStub{closeErr: backend.ErrConflict("Close", "issue is already closed")}
	out, err := runClose(t, stub, "loom-9")
	if err != nil {
		t.Fatalf("double-close must exit 0, got %v", err)
	}
	if !strings.Contains(out, "closed loom-9") {
		t.Errorf("output = %q, want closed loom-9", out)
	}
}

func TestCloseCommand_BlockerConflictStillFails(t *testing.T) {
	stub := &localBackendStub{closeErr: backend.ErrConflict("Close", "issue has open blockers")}
	if _, err := runClose(t, stub, "loom-9"); err == nil {
		t.Fatal("blocker conflicts must keep failing")
	}
}

func TestIsAlreadyClosedConflict(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{backend.ErrConflict("Close", "issue is already closed"), true},
		{backend.ErrConflict("Close", "issue is closed"), true},
		{backend.ErrConflict("Close", "issue has open blockers"), false},
		{backend.ErrConflict("Close", "blocked by dependency"), false},
		{backend.ErrNotFound("Close", "issue is already closed"), false}, // wrong kind
		{nil, false},
	}
	for _, tc := range cases {
		if got := isAlreadyClosedConflict(tc.err); got != tc.want {
			t.Errorf("isAlreadyClosedConflict(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
