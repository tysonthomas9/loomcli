package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// actorAwareStub embeds localBackendStub and additionally implements
// ClaimIssueAsActor, exposing it via the duck-typed interface that the
// claim command type-asserts against.
type actorAwareStub struct {
	*localBackendStub
	actorCalls []actorClaimCall
}

type actorClaimCall struct {
	id    string
	ttl   time.Duration
	actor string
}

func (a *actorAwareStub) ClaimIssueAsActor(_ context.Context, id string, ttl time.Duration, actor string) error {
	a.actorCalls = append(a.actorCalls, actorClaimCall{id: id, ttl: ttl, actor: actor})
	return nil
}

func TestClaimCmd_WithActor_RoutesThroughClaimIssueAsActor(t *testing.T) {
	stub := &actorAwareStub{localBackendStub: &localBackendStub{}}
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalIssueBackendProvider(func(context.Context) backend.IssueBackend {
			return stub
		})
		t.Cleanup(func() {
			SetLocalIssueBackendProvider(nil)
			claimActor = ""
		})

		outputFormat = "text"
		claimActor = "probe"

		if _, err := captureDataStdout(t, func() error {
			return claimCmd.RunE(claimCmd, []string{"loom-1"})
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}

		if len(stub.actorCalls) != 1 {
			t.Fatalf("ClaimIssueAsActor calls = %d, want 1 (%#v)", len(stub.actorCalls), stub.actorCalls)
		}
		got := stub.actorCalls[0]
		if got.id != "loom-1" || got.actor != "probe" || got.ttl != 0 {
			t.Fatalf("ClaimIssueAsActor call = %#v, want id=loom-1 actor=probe ttl=0", got)
		}
		for _, c := range stub.localBackendStub.calls {
			if c.method == "ClaimIssue" {
				t.Fatalf("unexpected ClaimIssue call: %#v", c)
			}
		}
	})
}

func TestClaimCmd_WithoutActor_RoutesThroughClaimIssue(t *testing.T) {
	stub := &actorAwareStub{localBackendStub: &localBackendStub{}}
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalIssueBackendProvider(func(context.Context) backend.IssueBackend {
			return stub
		})
		t.Cleanup(func() {
			SetLocalIssueBackendProvider(nil)
			claimActor = ""
		})

		outputFormat = "text"
		claimActor = ""

		if _, err := captureDataStdout(t, func() error {
			return claimCmd.RunE(claimCmd, []string{"loom-2"})
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}

		if len(stub.actorCalls) != 0 {
			t.Fatalf("ClaimIssueAsActor calls = %#v, want none", stub.actorCalls)
		}
		var sawClaim bool
		for _, c := range stub.localBackendStub.calls {
			if c.method == "ClaimIssue" && c.id == "loom-2" {
				sawClaim = true
			}
		}
		if !sawClaim {
			t.Fatalf("calls = %#v, want one ClaimIssue loom-2", stub.localBackendStub.calls)
		}
	})
}

func TestClaimCmd_WithActor_WhitespaceOnly_RoutesThroughClaimIssue(t *testing.T) {
	stub := &actorAwareStub{localBackendStub: &localBackendStub{}}
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalIssueBackendProvider(func(context.Context) backend.IssueBackend {
			return stub
		})
		t.Cleanup(func() {
			SetLocalIssueBackendProvider(nil)
			claimActor = ""
		})

		outputFormat = "text"
		claimActor = "  \t  "

		if _, err := captureDataStdout(t, func() error {
			return claimCmd.RunE(claimCmd, []string{"loom-3"})
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}

		if len(stub.actorCalls) != 0 {
			t.Fatalf("ClaimIssueAsActor calls = %#v, want none for whitespace-only --actor", stub.actorCalls)
		}
		var sawClaim bool
		for _, c := range stub.localBackendStub.calls {
			if c.method == "ClaimIssue" && c.id == "loom-3" {
				sawClaim = true
			}
		}
		if !sawClaim {
			t.Fatalf("calls = %#v, want fallback ClaimIssue loom-3 for whitespace-only --actor", stub.localBackendStub.calls)
		}
	})
}

func TestClaimCmd_WithActor_BackendWithoutSupport_ReturnsClearError(t *testing.T) {
	stub := &localBackendStub{}
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalIssueBackendProvider(func(context.Context) backend.IssueBackend {
			return stub
		})
		t.Cleanup(func() {
			SetLocalIssueBackendProvider(nil)
			claimActor = ""
		})

		outputFormat = "text"
		claimActor = "probe"

		_, err := captureDataStdout(t, func() error {
			return claimCmd.RunE(claimCmd, []string{"loom-4"})
		})
		if err == nil {
			t.Fatal("expected error when backend does not support ClaimIssueAsActor")
		}
		msg := err.Error()
		if !strings.Contains(msg, "--actor") {
			t.Errorf("error = %q, want substring %q", msg, "--actor")
		}
		if !strings.Contains(msg, "local-stub") {
			t.Errorf("error = %q, want substring %q", msg, "local-stub")
		}
		for _, c := range stub.calls {
			if c.method == "ClaimIssue" {
				t.Fatalf("unexpected ClaimIssue call after failed --actor check: %#v", c)
			}
		}
	})
}
