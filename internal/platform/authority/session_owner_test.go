package authority_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestIssueSessionForOwnerBindsExactLeaseGeneration(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject:   "session:sess-1",
		Class:     authority.ClassSession,
		Workspace: testWorkspace,
		Actions:   []authority.Action{testAction},
		ExpiresAt: now.Add(time.Hour),
	})
	got, err := issuer.IssueSessionForOwner(principal, testWorkspace, testAction, authority.SessionOwner{
		SessionID: "sess-1", AgentID: "agent-1", TerminalID: "term-1",
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
	})
	if err != nil {
		t.Fatalf("IssueSessionForOwner: %v", err)
	}
	if got.SessionID() != "sess-1" || got.AgentID() != "agent-1" ||
		got.TerminalID() != "term-1" || got.NodeID() != "node-1" ||
		got.LeaseID() != "lease-1" || got.FencingToken() != 7 {
		t.Fatalf("session owner = session %q agent %q terminal %q node %q lease %q fence %d",
			got.SessionID(), got.AgentID(), got.TerminalID(), got.NodeID(), got.LeaseID(), got.FencingToken())
	}
	admission, err := issuer.NewAdmission(authority.Allow(testAction, authority.ClassSession))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	if err := admission.RequireSession(testAction, testWorkspace, got); err != nil {
		t.Fatalf("RequireSession: %v", err)
	}
}

func TestIssueSessionForOwnerRejectsIncompleteOrUncanonicalFence(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject:   "session:sess-1",
		Class:     authority.ClassSession,
		Workspace: testWorkspace,
		Actions:   []authority.Action{testAction},
		ExpiresAt: now.Add(time.Hour),
	})
	valid := authority.SessionOwner{
		SessionID: "sess-1", AgentID: "agent-1", TerminalID: "term-1",
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
	}
	tests := []struct {
		name string
		edit func(*authority.SessionOwner)
	}{
		{"session", func(owner *authority.SessionOwner) { owner.SessionID = " " }},
		{"agent", func(owner *authority.SessionOwner) { owner.AgentID = "" }},
		{"terminal whitespace", func(owner *authority.SessionOwner) { owner.TerminalID = " term-1 " }},
		{"node", func(owner *authority.SessionOwner) { owner.NodeID = "" }},
		{"lease", func(owner *authority.SessionOwner) { owner.LeaseID = " lease-1" }},
		{"fence", func(owner *authority.SessionOwner) { owner.FencingToken = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner := valid
			tt.edit(&owner)
			_, err := issuer.IssueSessionForOwner(principal, testWorkspace, testAction, owner)
			if !errors.Is(err, authority.ErrInvalidScope) {
				t.Fatalf("error = %v, want ErrInvalidScope", err)
			}
		})
	}
}

func TestIssueSessionForOwnerWithCredentialTransfersCredentialExactlyOnce(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject:   "session:sess-1",
		Class:     authority.ClassSession,
		Workspace: testWorkspace,
		Actions:   []authority.Action{testAction},
		ExpiresAt: now.Add(time.Hour),
	})
	raw := []byte("raw-session-token")
	got, err := issuer.IssueSessionForOwnerWithCredential(
		principal,
		testWorkspace,
		testAction,
		authority.SessionOwner{
			SessionID: "sess-1", AgentID: "agent-1", NodeID: "node-1",
			LeaseID: "lease-1", FencingToken: 7,
		},
		raw,
	)
	if err != nil {
		t.Fatal(err)
	}
	clear(raw)

	owner := got.SessionOwner()
	credential := owner.ConsumeLeaseCredential()
	if string(credential) != "raw-session-token" {
		t.Fatalf("credential = %q", credential)
	}
	clear(credential)
	if replay := owner.ConsumeLeaseCredential(); len(replay) != 0 {
		t.Fatalf("credential replay returned %d bytes", len(replay))
	}
	owner.CloseLeaseCredential()
}
