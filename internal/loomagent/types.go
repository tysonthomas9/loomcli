package loomagent

import (
	"fmt"
	"strings"
)

// Code is a public loomagent error code (design v2 §12.1).
type Code string

const (
	CodeAgentNotFound       Code = "agent_not_found"
	CodeAgentNameTaken      Code = "agent_name_taken"
	CodeAgentArchived       Code = "agent_archived"
	CodeAgentBusy           Code = "agent_busy"
	CodeChildrenLive        Code = "children_live"
	CodeSpecVersionMismatch Code = "spec_version_mismatch"
	CodeExternalKeyConflict Code = "external_key_conflict"
	CodeExternalKeyTaken    Code = "external_key_taken"
	CodePresetNotFound      Code = "preset_not_found"
	CodePresetInvalid       Code = "preset_invalid"
	CodeAskNotFound         Code = "ask_not_found"
	CodeUnsavedWork         Code = "unsaved_work"
	CodeStaleSubject        Code = "stale_subject"
	CodeHarnessUnavailable  Code = "harness_unavailable"
	CodeHarnessError        Code = "harness_error"
	CodeGitFailed           Code = "git_failed"
	CodeSubscriberLagged    Code = "subscriber_lagged"
	CodeHistoryExpired      Code = "history_expired"
	CodeCursorExpired       Code = "cursor_expired"
	CodeWorktreeTaken       Code = "worktree_taken"
	CodeTurnNotFound        Code = "turn_not_found"
	// OR5a: a Respond's claim on its ask. conflict: the same request with
	// another answer; already_answered: another request claimed the ask;
	// reply_unknown: its Reply may or may not have landed, and is never sent again.
	CodeConflict        Code = "conflict"
	CodeAlreadyAnswered Code = "already_answered"
	CodeReplyUnknown    Code = "reply_unknown"
)

// Error is a loomagent error. Allowed lists the accepted values for
// preset_invalid; Paths and Fingerprint describe unsaved_work.
type Error struct {
	Code        Code
	Message     string
	Allowed     []string
	Paths       []string
	Fingerprint string
}

func (e *Error) Error() string {
	if len(e.Allowed) == 0 {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s (allowed: %s)", e.Code, e.Message, strings.Join(e.Allowed, ", "))
}

func invalid(msg string, allowed ...string) *Error {
	return &Error{Code: CodePresetInvalid, Message: msg, Allowed: allowed}
}

// Envelope is carried by every write (design v2 §4.3).
type Envelope struct {
	RequestID string
	Expect    *Expect
}

// Expect holds optional version checks.
type Expect struct {
	SpecVersion    *int64
	SubjectVersion string
}

// Subject is what an agent works on, for example a PR at a head SHA.
type Subject struct {
	Type    string
	ID      string
	Version string
}

// Persona overrides the preset's persona with a file or inline text.
type Persona struct {
	File string
	Text string
}

// Overrides are the per-Create changes to a preset. Empty fields keep the
// preset's value.
type Overrides struct {
	Harness        string
	Model          string
	Effort         string
	MaxBudgetUSD   *float64
	MaxRunDuration *int // seconds
	ReadOnly       bool
	AllowedTools   []string
	DeniedTools    []string
}

// CreateRequest is the Create input (design v2 §4.4).
type CreateRequest struct {
	Envelope
	Preset       string // name or name@version
	Overrides    Overrides
	Persona      *Persona // lead, task and daemon-worker only
	Name         string
	Parent       string
	Subject      Subject
	Repo         string
	BaseRef      string
	ExternalKey  string
	FirstMessage string
	// Actor is the caller, set by the entry point; the owner when the preset
	// is user-owned. Empty means the local user.
	Actor ActorRef `json:"-"`
	// Bridge is set only by Loom's host-owned bridge wiring, never from a
	// request body or any model or agent input.
	Bridge BridgeCaps `json:"-"`
}

// ActorRef is who calls: Kind is user, agent or system.
type ActorRef struct{ Kind, ID string }

// BridgeCaps are the agent's Loom bridge capabilities that replace gh and git push.
type BridgeCaps struct {
	HasGitHubRead bool // the github_read bridge tools
	HasPublish    bool // the publish bridge tool
}
