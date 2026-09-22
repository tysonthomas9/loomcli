package data

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// parentStub serves one parent issue (or an error) and records the ids it was
// asked for, so a test can assert the lookup happened at all — and, just as
// importantly, that it did NOT happen when there was nothing to inherit.
// The embedded interface is deliberately nil: only Get is implemented, so any
// other backend call this resolver might grow would panic the test rather than
// pass silently.
type parentStub struct {
	backend.IssueBackend
	detail *backend.IssueDetailData
	err    error
	gets   []string
}

func (p *parentStub) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	p.gets = append(p.gets, id)
	if p.err != nil {
		return nil, p.err
	}
	return p.detail, nil
}

func parentWithRepo(repo string) *backend.IssueDetailData {
	d := &backend.IssueDetailData{}
	d.SourceRepo = repo
	return d
}

// runInherit exercises the resolver and returns the resulting repo plus whatever
// was written to the warning stream.
func runInherit(t *testing.T, params *backend.CreateParams, stub *parentStub) string {
	t.Helper()
	var warn bytes.Buffer
	inheritSourceRepoFromParent(context.Background(), &warn, stub, params)
	return warn.String()
}

func TestInheritSourceRepo_FillsFromParent(t *testing.T) {
	stub := &parentStub{detail: parentWithRepo("sandbox")}
	params := &backend.CreateParams{Parent: "TEST-13"}

	if warn := runInherit(t, params, stub); warn != "" {
		t.Errorf("unexpected warning on the happy path: %q", warn)
	}
	if params.SourceRepo != "sandbox" {
		t.Errorf("SourceRepo = %q, want inherited %q", params.SourceRepo, "sandbox")
	}
	if len(stub.gets) != 1 || stub.gets[0] != "TEST-13" {
		t.Errorf("parent lookups = %v, want one Get(TEST-13)", stub.gets)
	}
}

// An explicit flag is an intent; inheritance may only ever fill a gap. This is
// the guard against the change quietly rewriting what a caller asked for.
func TestInheritSourceRepo_ExplicitFlagWins(t *testing.T) {
	stub := &parentStub{detail: parentWithRepo("parent-repo")}
	params := &backend.CreateParams{Parent: "TEST-13", SourceRepo: "explicit-repo"}

	runInherit(t, params, stub)

	if params.SourceRepo != "explicit-repo" {
		t.Errorf("SourceRepo = %q, want the explicit flag to win", params.SourceRepo)
	}
	if len(stub.gets) != 0 {
		t.Errorf("parent was fetched (%v) despite an explicit --source-repo; that is a needless round trip", stub.gets)
	}
}

func TestInheritSourceRepo_NoParentDoesNothing(t *testing.T) {
	stub := &parentStub{detail: parentWithRepo("sandbox")}
	params := &backend.CreateParams{}

	runInherit(t, params, stub)

	if params.SourceRepo != "" {
		t.Errorf("SourceRepo = %q, want empty with no parent", params.SourceRepo)
	}
	if len(stub.gets) != 0 {
		t.Errorf("parent lookups = %v, want none without --parent", stub.gets)
	}
}

// A repo-less parent has nothing to lend. It must stay SILENT: warning here
// would fire for every child of every repo-less epic, and the caller is no worse
// off than before the change.
func TestInheritSourceRepo_ParentWithoutRepoIsSilent(t *testing.T) {
	for name, stub := range map[string]*parentStub{
		"empty repo": {detail: parentWithRepo("")},
		"nil detail": {detail: nil},
	} {
		t.Run(name, func(t *testing.T) {
			params := &backend.CreateParams{Parent: "epic-1"}
			warn := runInherit(t, params, stub)
			if params.SourceRepo != "" {
				t.Errorf("SourceRepo = %q, want empty", params.SourceRepo)
			}
			if warn != "" {
				t.Errorf("warning = %q, want silence when the parent simply has no repo", warn)
			}
		})
	}
}

// An unreadable parent must NOT fail the create — creates that work today keep
// working — but it must say so, because the issue being created will never be
// claimed by anyone.
func TestInheritSourceRepo_UnreadableParentWarnsButProceeds(t *testing.T) {
	stub := &parentStub{err: errors.New("boom")}
	params := &backend.CreateParams{Parent: "TEST-13"}

	warn := runInherit(t, params, stub)

	if params.SourceRepo != "" {
		t.Errorf("SourceRepo = %q, want empty when the parent could not be read", params.SourceRepo)
	}
	for _, want := range []string{"TEST-13", "boom", "no agent will claim"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warning missing %q; got %q", want, warn)
		}
	}
}

// The ordering guarantee. The default idempotency key hashes the create body, so
// it has to be derived AFTER inheritance: a key computed from the pre-inheritance
// body describes a request that is never sent, which is precisely what trips
// fleet-db's "key already used with a different request body" 409.
//
// Asserted by equivalence rather than by inspecting the hash: creating a child
// that INHERITS "sandbox" must produce the same key as creating the identical
// child that names "sandbox" explicitly, because the bodies are identical.
func TestCreateIdempotencyKey_ReflectsInheritedSourceRepo(t *testing.T) {
	base := backend.CreateParams{
		Parent:    "TEST-13",
		Title:     "wordcount.py utility",
		IssueType: "task",
		Priority:  2,
	}

	inherited := base
	if warn := runInherit(t, &inherited, &parentStub{detail: parentWithRepo("sandbox")}); warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	applyCreateIdempotency(&inherited, createIssueFlags{})

	explicit := base
	explicit.SourceRepo = "sandbox"
	applyCreateIdempotency(&explicit, createIssueFlags{})

	if inherited.IdempotencyKey == "" {
		t.Fatal("no idempotency key was stamped")
	}
	if inherited.IdempotencyKey != explicit.IdempotencyKey {
		t.Errorf("inherited key %q != explicit key %q — the key does not reflect the body actually sent",
			inherited.IdempotencyKey, explicit.IdempotencyKey)
	}

	// And the sanity check that the comparison above is not vacuous: a DIFFERENT
	// repo must produce a different key.
	other := base
	other.SourceRepo = "other-repo"
	applyCreateIdempotency(&other, createIssueFlags{})
	if other.IdempotencyKey == inherited.IdempotencyKey {
		t.Error("keys collide across different source repos; the assertion above proves nothing")
	}
}

func TestApplyCreateIdempotency_Flags(t *testing.T) {
	t.Run("explicit key wins", func(t *testing.T) {
		p := backend.CreateParams{Title: "t"}
		applyCreateIdempotency(&p, createIssueFlags{idempotencyKey: "mine"})
		if p.IdempotencyKey != "mine" {
			t.Errorf("IdempotencyKey = %q, want %q", p.IdempotencyKey, "mine")
		}
	})
	t.Run("no-idempotency stamps nothing", func(t *testing.T) {
		p := backend.CreateParams{Title: "t"}
		applyCreateIdempotency(&p, createIssueFlags{noIdempotency: true})
		if p.IdempotencyKey != "" {
			t.Errorf("IdempotencyKey = %q, want empty", p.IdempotencyKey)
		}
	})
}

// End-to-end through the command: a child created with --parent and no
// --source-repo reaches the backend carrying the parent's repo. This is the
// behavior that makes the dead-child class impossible, as opposed to merely
// documented.
func TestCreateCommand_InheritsSourceRepoFromParent(t *testing.T) {
	parent := parentWithRepo("sandbox")
	stub := &localBackendStub{
		detail:     parent,
		createItem: &backend.IssueData{ID: "TEST-19", Title: "child", Status: "open"},
	}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		cmd := newCreateCmd()
		cmd.SetArgs([]string{"--title", "child", "--parent", "TEST-13"})
		if _, err := captureDataStdout(t, cmd.Execute); err != nil {
			t.Fatalf("create: %v", err)
		}
		var created *backend.CreateParams
		for _, c := range stub.calls {
			if c.method == "Create" {
				p := c.args.(backend.CreateParams)
				created = &p
			}
		}
		if created == nil {
			t.Fatalf("no Create call recorded; calls = %#v", stub.calls)
		}
		if created.SourceRepo != "sandbox" {
			t.Errorf("SourceRepo = %q, want the parent's %q", created.SourceRepo, "sandbox")
		}
	})
}
