package backend

import (
	"context"
	"encoding/json"
)

// IssueRecoverySnapshot preserves the complete native certified issue manifest.
// Through is a committed lower bound, not an authorization to reset a client.
type IssueRecoverySnapshot struct {
	Manifest  string
	Workspace string
	Through   string
	Document  json.RawMessage
}

// IssueRecoveryBackend reads a fixed workspace manifest without ordinary-query fallback.
type IssueRecoveryBackend interface {
	ReadIssueRecovery(context.Context) (IssueRecoverySnapshot, error)
}
