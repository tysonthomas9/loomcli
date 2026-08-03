package realtime

import "time"

const MutationSessionChange = "session_change"

// MutationEvent is the durable event shape consumed by SSE catch-up. It is
// owned by the realtime adapter rather than the retired daemon RPC protocol.
type MutationEvent struct {
	Cursor     string
	Type       string
	EntityType string
	EntityID   string
	Action     string
	IssueID    string
	Title      string
	Assignee   string
	Actor      string
	Timestamp  time.Time
	OldStatus  string
	NewStatus  string
	ParentID   string
	StepCount  int
	SourceRepo string
}
