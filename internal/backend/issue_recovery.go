package backend

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// IssueRecoverySnapshot preserves the complete native certified issue manifest.
// Through is the v5 producer snapshot boundary under its guarded-writer contract.
// It is not authorization to reset a client or proof of cross-request identity.
type IssueRecoverySnapshot struct {
	SelectedIssueID string
	SourceIdentity  string
	Manifest        string
	Workspace       string
	Through         string
	Document        json.RawMessage
}

// IssueRecoveryBackend reads a fixed workspace manifest without ordinary-query fallback.
type IssueRecoveryBackend interface {
	ReadIssueRecovery(context.Context) (IssueRecoverySnapshot, error)
}

// IssueRecoverySelectedBackend adds one explicit selected-issue history window.
type IssueRecoverySelectedBackend interface {
	ReadIssueRecoveryForIssue(context.Context, string) (IssueRecoverySnapshot, error)
}

// ValidRecoveryIssueSelection validates an exact scope without normalizing it.
func ValidRecoveryIssueSelection(id string) bool {
	return len(id) <= 1024 && utf8.ValidString(id) && strings.TrimSpace(id) != ""
}
