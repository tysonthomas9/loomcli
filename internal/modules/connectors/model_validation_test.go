package connectors

import (
	"errors"
	"testing"
	"time"
)

func validConnector() Connector {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	return Connector{
		WorkspaceKey:        "ws-1",
		ConnectorID:         "conn-github-main",
		SourceKind:          ConnectorSourceGitHub,
		DisplayName:         "GitHub (main org)",
		InboundEndpointPath: "/hooks/conn-github-main",
		Status:              ConnectorStatusActive,
		CreatedBy:           "tyson",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func TestConnectorValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Connector)
		wantErr error
	}{
		{name: "valid", mutate: func(*Connector) {}},
		{name: "missing workspace key", mutate: func(c *Connector) { c.WorkspaceKey = "" }, wantErr: ErrInvalid},
		{name: "missing connector id", mutate: func(c *Connector) { c.ConnectorID = "" }, wantErr: ErrInvalid},
		{name: "unknown source kind", mutate: func(c *Connector) { c.SourceKind = "gitlab" }, wantErr: ErrInvalid},
		{name: "empty source kind", mutate: func(c *Connector) { c.SourceKind = "" }, wantErr: ErrInvalid},
		{name: "unknown status", mutate: func(c *Connector) { c.Status = "paused" }, wantErr: ErrInvalid},
		{name: "empty status", mutate: func(c *Connector) { c.Status = "" }, wantErr: ErrInvalid},
		{name: "relative endpoint path", mutate: func(c *Connector) { c.InboundEndpointPath = "hooks/x" }, wantErr: ErrInvalid},
		{name: "endpoint path optional", mutate: func(c *Connector) { c.InboundEndpointPath = "" }},
		{name: "slack kind", mutate: func(c *Connector) { c.SourceKind = ConnectorSourceSlack }},
		{name: "datadog kind", mutate: func(c *Connector) { c.SourceKind = ConnectorSourceDatadog }},
		{name: "internal kind", mutate: func(c *Connector) { c.SourceKind = ConnectorSourceInternal }},
		{name: "disabled status", mutate: func(c *Connector) { c.Status = ConnectorStatusDisabled }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConnector()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is(%v)", err, tt.wantErr)
			}
		})
	}
}

func validGrant() ConnectorGrant {
	return ConnectorGrant{
		WorkspaceKey:    "ws-1",
		GrantID:         "grant-1",
		ConnectorID:     "conn-github-main",
		BindingID:       "bind-1",
		Action:          "github.merge",
		ResourcePattern: "repo:octocat/hello",
		CreatedAt:       time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
	}
}

func TestConnectorGrantValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ConnectorGrant)
		wantErr error
	}{
		{name: "valid", mutate: func(*ConnectorGrant) {}},
		{name: "missing workspace key", mutate: func(g *ConnectorGrant) { g.WorkspaceKey = "" }, wantErr: ErrInvalid},
		{name: "missing grant id", mutate: func(g *ConnectorGrant) { g.GrantID = "" }, wantErr: ErrInvalid},
		{name: "missing connector id", mutate: func(g *ConnectorGrant) { g.ConnectorID = "" }, wantErr: ErrInvalid},
		{name: "missing binding id", mutate: func(g *ConnectorGrant) { g.BindingID = "" }, wantErr: ErrInvalid},
		{name: "missing resource pattern", mutate: func(g *ConnectorGrant) { g.ResourcePattern = "" }, wantErr: ErrInvalid},
		{name: "bad action", mutate: func(g *ConnectorGrant) { g.Action = "merge" }, wantErr: ErrInvalid},
		{name: "three segment action", mutate: func(g *ConnectorGrant) { g.Action = "slack.chat.post_message" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := validGrant()
			tt.mutate(&g)
			err := g.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is(%v)", err, tt.wantErr)
			}
		})
	}
}

func TestConnectorGrantRevoked(t *testing.T) {
	g := validGrant()
	if g.Revoked() {
		t.Fatal("fresh grant reports revoked")
	}
	at := time.Date(2026, 6, 11, 13, 0, 0, 0, time.UTC)
	g.RevokedAt = &at
	if !g.Revoked() {
		t.Fatal("grant with RevokedAt set reports active")
	}
	zero := time.Time{}
	g.RevokedAt = &zero
	if g.Revoked() {
		t.Fatal("grant with zero RevokedAt reports revoked")
	}
}

func TestValidateConnectorAction(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		wantErr bool
	}{
		{name: "github merge", action: "github.merge"},
		{name: "three segments", action: "slack.chat.post_message"},
		{name: "digits and dashes", action: "datadog.mute-monitor2"},
		{name: "single segment", action: "merge", wantErr: true},
		{name: "empty", action: "", wantErr: true},
		{name: "leading dot", action: ".merge", wantErr: true},
		{name: "trailing dot", action: "github.", wantErr: true},
		{name: "double dot", action: "github..merge", wantErr: true},
		{name: "uppercase", action: "github.Merge", wantErr: true},
		{name: "space", action: "github. merge", wantErr: true},
		{name: "slash", action: "github/merge", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConnectorAction(tt.action)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("ValidateConnectorAction(%q) = %v, want errors.Is(ErrInvalid)", tt.action, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateConnectorAction(%q) = %v, want nil", tt.action, err)
			}
		})
	}
}

func validCallRecord() ConnectorCallRecord {
	return ConnectorCallRecord{
		WorkspaceKey:     "ws-1",
		CallID:           ConnectorCallID("run-1", "github.merge", 3),
		Seq:              3,
		RunID:            "run-1",
		BindingID:        "bind-1",
		ConnectorID:      "conn-github-main",
		SourceKind:       ConnectorSourceGitHub,
		Action:           "github.merge",
		Resource:         "repo:octocat/hello",
		Decision:         ConnectorCallGranted,
		UpstreamStatus:   200,
		SanitizedSummary: "merged PR 42",
		OccurredAt:       time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
	}
}

func TestConnectorCallRecordValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ConnectorCallRecord)
		wantErr error
	}{
		{name: "valid granted", mutate: func(*ConnectorCallRecord) {}},
		{name: "denied before egress", mutate: func(r *ConnectorCallRecord) {
			r.Decision = ConnectorCallDenied
			r.UpstreamStatus = 0
		}},
		{name: "stale subject", mutate: func(r *ConnectorCallRecord) { r.Decision = ConnectorCallStaleSubject }},
		{name: "precondition required", mutate: func(r *ConnectorCallRecord) { r.Decision = ConnectorCallPreconditionRequired }},
		{name: "upstream error", mutate: func(r *ConnectorCallRecord) { r.Decision = ConnectorCallUpstreamError }},
		{name: "missing workspace key", mutate: func(r *ConnectorCallRecord) { r.WorkspaceKey = "" }, wantErr: ErrInvalid},
		{name: "missing run id", mutate: func(r *ConnectorCallRecord) { r.RunID = "" }, wantErr: ErrInvalid},
		{name: "missing binding id", mutate: func(r *ConnectorCallRecord) { r.BindingID = "" }, wantErr: ErrInvalid},
		{name: "missing connector id", mutate: func(r *ConnectorCallRecord) { r.ConnectorID = "" }, wantErr: ErrInvalid},
		{name: "unknown source kind", mutate: func(r *ConnectorCallRecord) { r.SourceKind = "pagerduty" }, wantErr: ErrInvalid},
		{name: "bad action", mutate: func(r *ConnectorCallRecord) { r.Action = "merge" }, wantErr: ErrInvalid},
		{name: "unknown decision", mutate: func(r *ConnectorCallRecord) { r.Decision = "maybe" }, wantErr: ErrInvalid},
		{name: "call id mismatch on seq", mutate: func(r *ConnectorCallRecord) { r.Seq = 4 }, wantErr: ErrInvalid},
		{name: "call id mismatch on run", mutate: func(r *ConnectorCallRecord) { r.RunID = "run-2" }, wantErr: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validCallRecord()
			tt.mutate(&r)
			err := r.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is(%v)", err, tt.wantErr)
			}
		})
	}
}

func TestConnectorCallID(t *testing.T) {
	got := ConnectorCallID("run-1", "github.merge", 0)
	if got != "run-1#github.merge#0" {
		t.Fatalf("ConnectorCallID = %q, want %q", got, "run-1#github.merge#0")
	}
}

func TestConnectorSentinelsStayDistinct(t *testing.T) {
	if errors.Is(ErrGrantDenied, ErrGrantRevoked) {
		t.Fatal("ErrGrantDenied and ErrGrantRevoked must stay distinct")
	}
}
