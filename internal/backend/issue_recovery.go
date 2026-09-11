package backend

import (
	"context"
	"encoding/json"
)

// IssueRecoverySnapshot preserves the complete native certified issue manifest.
// Through is the v3 producer snapshot boundary under its guarded-writer contract.
// It is not authorization to reset a client or proof of cross-request identity.
type IssueRecoverySnapshot struct {
	SourceIdentity string
	Manifest       string
	Workspace      string
	Through        string
	Document       json.RawMessage
}

// IssueRecoveryBackend reads a fixed workspace manifest without ordinary-query fallback.
type IssueRecoveryBackend interface {
	ReadIssueRecovery(context.Context) (IssueRecoverySnapshot, error)
}
