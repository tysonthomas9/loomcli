package agentsv1

import (
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// The Agent v1 wire format: every field is snake_case, like the rest of the
// webui API (api/openapi.yaml). These types are the only JSON the routes
// read or write; loomagent and loomstore structs are never encoded directly.

// Expect holds optional version checks on a write.
type Expect struct {
	SpecVersion    *int64 `json:"spec_version,omitempty"`
	SubjectVersion string `json:"subject_version,omitempty"`
}

func (e *Expect) expect() *loomagent.Expect {
	if e == nil {
		return nil
	}
	return &loomagent.Expect{SpecVersion: e.SpecVersion, SubjectVersion: e.SubjectVersion}
}

// Overrides are a Create's per-agent changes to its preset.
type Overrides struct {
	Harness        string   `json:"harness"`
	Model          string   `json:"model"`
	Effort         string   `json:"effort"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd"`
	MaxRunDuration *int     `json:"max_run_duration"` // seconds
	ReadOnly       bool     `json:"read_only"`
	AllowedTools   []string `json:"allowed_tools"`
	DeniedTools    []string `json:"denied_tools"`
}

// Persona replaces the preset's persona with a file or inline text.
type Persona struct {
	File string `json:"file"`
	Text string `json:"text"`
}

// Subject is what an agent works on, for example a PR at a head SHA.
type Subject struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

// CreateBody is the POST /agents body.
type CreateBody struct {
	Preset       string    `json:"preset"`
	Overrides    Overrides `json:"overrides"`
	Persona      *Persona  `json:"persona"`
	Name         string    `json:"name"`
	Parent       string    `json:"parent"`
	Subject      Subject   `json:"subject"`
	Repo         string    `json:"repo"`
	BaseRef      string    `json:"base_ref"`
	ExternalKey  string    `json:"external_key"`
	FirstMessage string    `json:"first_message"`
}

func (b CreateBody) request() loomagent.CreateRequest {
	o := b.Overrides
	req := loomagent.CreateRequest{Preset: b.Preset, Overrides: loomagent.Overrides{Harness: o.Harness,
		Model: o.Model, Effort: o.Effort, MaxBudgetUSD: o.MaxBudgetUSD, MaxRunDuration: o.MaxRunDuration,
		ReadOnly: o.ReadOnly, AllowedTools: o.AllowedTools, DeniedTools: o.DeniedTools},
		Name: b.Name, Parent: b.Parent, Repo: b.Repo, BaseRef: b.BaseRef, ExternalKey: b.ExternalKey,
		FirstMessage: b.FirstMessage,
		Subject:      loomagent.Subject(b.Subject)}
	if b.Persona != nil {
		req.Persona = &loomagent.Persona{File: b.Persona.File, Text: b.Persona.Text}
	}
	return req
}

// UpdateBody is the PATCH /agents/{id} body; empty fields are unchanged.
type UpdateBody struct {
	Name    string  `json:"name"`
	Model   string  `json:"model"`
	Harness string  `json:"harness"`
	Expect  *Expect `json:"expect"`
}

// ArchiveBody is the optional archive body; reason defaults to done.
type ArchiveBody struct {
	Reason string `json:"reason"`
}

// SendBody is the POST /agents/{id}/messages body.
type SendBody struct {
	Text string `json:"text"`
}

// RespondBody answers an ask: decision for an approval, answer for a question.
type RespondBody struct {
	Decision string `json:"decision"`
	Answer   string `json:"answer"`
}

// Agent is what Create, Get, Update and List return. waiting_messages and
// open_asks are filled by Get only.
type Agent struct {
	AgentID         string           `json:"agent_id"`
	WorkspaceID     string           `json:"workspace_id"`
	Name            string           `json:"name"`
	ProfileKey      string           `json:"profile_key"`
	Preset          string           `json:"preset"`
	PresetVersion   string           `json:"preset_version"`
	Mode            string           `json:"mode"`
	InteractionMode string           `json:"interaction_mode"`
	RoleKind        string           `json:"role_kind"`
	Spec            string           `json:"spec_json"`
	SpecVersion     int64            `json:"spec_version"`
	OwnerKind       string           `json:"owner_kind"`
	OwnerID         string           `json:"owner_id"`
	CreatedByKind   string           `json:"created_by_kind"`
	CreatedByID     string           `json:"created_by_id"`
	ParentAgentID   *string          `json:"parent_agent_id"`
	RootAgentID     *string          `json:"root_agent_id"`
	SubjectType     *string          `json:"subject_type"`
	SubjectID       *string          `json:"subject_id"`
	SubjectVersion  *string          `json:"subject_version"`
	ExternalKey     *string          `json:"external_key"`
	Repo            string           `json:"repo"`
	BaseRef         *string          `json:"base_ref"`
	WorktreePath    *string          `json:"worktree_path"`
	Branch          *string          `json:"branch"`
	Harness         string           `json:"harness"`
	Host            string           `json:"host"`
	Model           *string          `json:"model"`
	State           string           `json:"state"`
	StateReason     *string          `json:"state_reason"`
	WaitingOn       *string          `json:"waiting_on"`
	Attempt         int64            `json:"attempt"`
	Outcome         *string          `json:"outcome"`
	ArchiveReason   *string          `json:"archive_reason"`
	AttentionReason *string          `json:"attention_reason"`
	RunningTurnID   *string          `json:"running_turn_id"`
	DeleteRequested bool             `json:"delete_requested"`
	LastActiveAt    *string          `json:"last_active_at"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
	ArchivedAt      *string          `json:"archived_at"`
	FinishedAt      *string          `json:"finished_at"`
	HistoryPurgedAt *string          `json:"history_purged_at"`
	DeletedAt       *string          `json:"deleted_at"`
	Compute         string           `json:"compute"`
	WaitingMessages []WaitingMessage `json:"waiting_messages"`
	OpenAsks        []Ask            `json:"open_asks"`
}

// WaitingMessage is one sender's message waiting for the agent.
type WaitingMessage struct {
	Sender string `json:"sender"`
	Text   string `json:"text"`
	Since  string `json:"since"`
}

// Ask is one open harness ask.
type Ask struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	About string `json:"about"`
}

func agentOut(i loomagent.AgentInfo) Agent {
	a := i.Agent
	out := Agent{AgentID: a.AgentID, WorkspaceID: a.WorkspaceID, Name: a.Name, ProfileKey: a.ProfileKey,
		Preset: a.Preset, PresetVersion: a.PresetVersion, Mode: a.Mode, InteractionMode: a.InteractionMode,
		RoleKind: a.RoleKind, Spec: a.SpecJSON, SpecVersion: a.SpecVersion, OwnerKind: a.OwnerKind,
		OwnerID: a.OwnerID, CreatedByKind: a.CreatedByKind, CreatedByID: a.CreatedByID,
		ParentAgentID: a.ParentAgentID, RootAgentID: a.RootAgentID, SubjectType: a.SubjectType,
		SubjectID: a.SubjectID, SubjectVersion: a.SubjectVersion, ExternalKey: a.ExternalKey, Repo: a.Repo,
		BaseRef: a.BaseRef, WorktreePath: a.WorktreePath, Branch: a.Branch, Harness: a.Harness, Host: a.Host,
		Model: a.Model, State: a.State, StateReason: a.StateReason, WaitingOn: a.WaitingOn, Attempt: a.Attempt,
		Outcome: a.Outcome, ArchiveReason: a.ArchiveReason, AttentionReason: a.AttentionReason,
		RunningTurnID: a.RunningTurnID, DeleteRequested: a.DeleteRequested, LastActiveAt: a.LastActiveAt,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, ArchivedAt: a.ArchivedAt, FinishedAt: a.FinishedAt,
		HistoryPurgedAt: a.HistoryPurgedAt, DeletedAt: a.DeletedAt, Compute: i.Compute,
		WaitingMessages: []WaitingMessage{}, OpenAsks: []Ask{}}
	for _, w := range i.WaitingMessages {
		out.WaitingMessages = append(out.WaitingMessages, WaitingMessage{w.Sender, w.Text, w.Since})
	}
	for _, k := range i.OpenAsks {
		out.OpenAsks = append(out.OpenAsks, Ask{k.ID, k.Type, k.About})
	}
	return out
}

// Event is one saved agent event; payload is opaque.
type Event struct {
	AgentID   string          `json:"agent_id"`
	Seq       int64           `json:"seq"`
	EventID   string          `json:"event_id"`
	Kind      string          `json:"kind"`
	TurnID    string          `json:"turn_id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

// EventPage is one ListEvents page.
type EventPage struct {
	Events      []Event `json:"events"`
	SnapshotSeq int64   `json:"snapshot_seq"`
	Next        int64   `json:"next"`
	More        bool    `json:"more"`
}

func eventPageOut(p loomstore.EventPage) EventPage {
	out := EventPage{Events: []Event{}, SnapshotSeq: p.SnapshotSeq, Next: p.Next, More: p.More}
	for _, e := range p.Events {
		out.Events = append(out.Events, eventOut(e))
	}
	return out
}

func eventOut(e loomstore.Event) Event {
	return Event{e.AgentID, e.Seq, e.EventID, e.Kind, e.TurnID, e.Payload, e.CreatedAt}
}

// SendResult is what Send returns.
type SendResult struct {
	MessageID string `json:"message_id"`
	State     string `json:"state"`
	Replaced  bool   `json:"replaced"`
	TurnID    string `json:"turn_id,omitempty"`
}

// WithdrawResult is withdrawn, nothing_waiting or already_handed.
type WithdrawResult struct {
	Result string `json:"result"`
}

// PermissionRule is one preset permission rule.
type PermissionRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

// Preset is one agent preset.
type Preset struct {
	Name           string           `json:"name"`
	Version        int              `json:"version"`
	Mode           string           `json:"mode"`
	RoleKind       string           `json:"role_kind"`
	OwnerKind      string           `json:"owner_kind"`
	ExternalKeyFmt string           `json:"external_key_fmt"`
	Persona        string           `json:"persona"`
	Harnesses      []string         `json:"harnesses"`
	Rules          []PermissionRule `json:"rules"`
	Tools          []string         `json:"tools"`
	Subagents      bool             `json:"subagents"`
	Overridable    []string         `json:"overridable"`
}

func presetOut(p loomagent.Preset) Preset {
	out := Preset{Name: p.Name, Version: p.Version, Mode: p.Mode, RoleKind: p.RoleKind, OwnerKind: p.OwnerKind,
		ExternalKeyFmt: p.ExternalKeyFmt, Persona: p.Persona, Harnesses: p.Harnesses, Rules: []PermissionRule{},
		Tools: p.Tools, Subagents: p.Subagents, Overridable: p.Overridable}
	for _, r := range p.Rules {
		out.Rules = append(out.Rules, PermissionRule{r.Action, r.Resource, r.Effect})
	}
	return out
}

// AgentList is what List returns; next is the cursor of the following page.
type AgentList struct {
	Agents []Agent `json:"agents"`
	Next   string  `json:"next"`
}

// PresetList is what GET /presets returns.
type PresetList struct {
	Presets []Preset `json:"presets"`
}

// Error is every error response. code is a loomagent code (design v2
// §12.1), empty for request errors such as a bad body.
type Error struct {
	Error       string         `json:"error"`
	Code        loomagent.Code `json:"code,omitempty"`
	Allowed     []string       `json:"allowed,omitempty"`
	Paths       []string       `json:"paths,omitempty"`
	Fingerprint string         `json:"fingerprint,omitempty"`
}
