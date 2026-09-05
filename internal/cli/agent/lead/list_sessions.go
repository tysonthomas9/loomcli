package lead

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

// leadListOutputText / leadListOutputJSON are the two --output values.
const (
	leadListOutputText = "text"
	leadListOutputJSON = "json"
)

// leadListTimeFormat is minute resolution on purpose: this table is read to
// pick a session, and a nanosecond timestamp makes the columns unreadable
// without answering a question anyone asks of it.
const leadListTimeFormat = "2006-01-02 15:04"

// leadSessionView is one row of `loom lead --list-sessions`, and it is the
// JSON contract: the fields are named and tagged here rather than marshaling
// leadcontrol.LeadSessionRecord directly, so a change to the internal record
// cannot silently rename a key a script depends on.
type leadSessionView struct {
	SessionID        string `json:"session_id"`
	AgentID          string `json:"agent_id"`
	Status           string `json:"status"`
	StartedAt        string `json:"started_at,omitempty"`
	FinishedAt       string `json:"finished_at,omitempty"`
	Finished         bool   `json:"finished"`
	WorkDir          string `json:"work_dir,omitempty"`
	Provider         string `json:"provider,omitempty"`
	HarnessSessionID string `json:"harness_session_id,omitempty"`
	CodexThreadID    string `json:"codex_thread_id,omitempty"`
	// CodexThreadName is decoration read out of codex's own session index. It
	// is absent for a session codex never named, and for every non-codex row.
	CodexThreadName string `json:"codex_thread_name,omitempty"`
	// ResumeID is the id to hand `loom lead --resume`, i.e. whichever provider
	// handle this row actually recorded. Empty when it recorded none, which is
	// exactly the case resume refuses.
	ResumeID string `json:"resume_id,omitempty"`
}

// leadSessionListing is the top-level JSON document.
type leadSessionListing struct {
	Workspace string            `json:"workspace"`
	AgentID   string            `json:"agent_id"`
	Sessions  []leadSessionView `json:"sessions"`
}

// runLeadListSessions renders this agent's previous lead runs and returns.
//
// It is a pure query and deliberately a dead end in runLead: it registers no
// orchestration row, generates no prompt and materializes no skills, so asking
// "which sessions do I have?" never creates one more of them — which it would
// if the listing ran after registerLeadOrchestratorSession.
//
// Unlike the rest of `loom lead`, a store failure here is fatal. Lead itself
// launches fine with fleet-db down; a LISTING that silently prints nothing
// because the store was unreachable would read as "you have no sessions", and
// the operator would start a fresh one over the top.
func runLeadListSessions(ctx context.Context, out io.Writer, workDir string) error {
	if err := validateLeadListFlags(); err != nil {
		return err
	}

	openCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancel()
	handle, ws, ok := openLeadSessionStore(openCtx)
	if !ok {
		return fmt.Errorf(
			"cannot list lead sessions: loom's session store is unavailable. Start fleet-db and retry")
	}
	defer func() { _ = handle.Close() }()

	agentID := resolveLeadAgentID()
	listCtx, cancelList := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancelList()
	records, err := leadcontrol.ListLeadSessions(listCtx, handle.Store, ws, agentID, leadcontrol.LeadSessionListOptions{})
	if err != nil {
		return fmt.Errorf("cannot list lead sessions: %w", err)
	}

	// Decoration only, and best-effort by design: a codex index that cannot be
	// read costs the table one column, never the table.
	index, err := leadcontrol.ReadCodexSessionIndex(os.Getenv("CODEX_HOME"))
	if err != nil {
		index = nil
	}

	listing := leadSessionListing{
		Workspace: ws,
		AgentID:   agentID,
		Sessions:  leadSessionViews(records, index),
	}
	return renderLeadSessions(out, listing, leadListOutput, workDir)
}

// validateLeadListFlags rejects the combinations that are decidable from the
// flags alone. It runs BEFORE the store is opened: a usage error must not
// depend on whether fleet-db happens to be up.
func validateLeadListFlags() error {
	if leadContinue || strings.TrimSpace(leadResume) != "" {
		return fmt.Errorf("--list-sessions cannot be combined with --resume or --continue; " +
			"run 'loom lead --list-sessions' to choose a session, then 'loom lead --resume <id>' to reopen it")
	}
	switch strings.ToLower(strings.TrimSpace(leadListOutput)) {
	case leadListOutputText, leadListOutputJSON, "":
		return nil
	default:
		return fmt.Errorf("unsupported --output %q: use %s or %s", leadListOutput, leadListOutputText, leadListOutputJSON)
	}
}

// leadSessionViews projects the records for rendering, newest first as
// ListLeadSessions already ordered them.
func leadSessionViews(records []leadcontrol.LeadSessionRecord, index map[string]leadcontrol.CodexSessionIndexEntry) []leadSessionView {
	views := make([]leadSessionView, 0, len(records))
	for _, rec := range records {
		views = append(views, leadSessionView{
			SessionID:        rec.SessionID,
			AgentID:          rec.AgentID,
			Status:           string(rec.Status),
			StartedAt:        formatLeadListTime(rec.StartedAt),
			FinishedAt:       formatLeadListTime(rec.FinishedAt),
			Finished:         rec.Finished,
			WorkDir:          rec.WorkDir,
			Provider:         rec.Provider,
			HarnessSessionID: rec.HarnessSessionID,
			CodexThreadID:    rec.CodexThreadID,
			CodexThreadName:  leadcontrol.CodexThreadName(index, rec.CodexThreadID),
			ResumeID:         leadSessionResumeID(rec),
		})
	}
	return views
}

// leadSessionResumeID is the handle --resume would take for this row. The
// harness id wins when a row somehow recorded both, matching resume's own
// preference on every non-codex backend.
func leadSessionResumeID(rec leadcontrol.LeadSessionRecord) string {
	if rec.HarnessSessionID != "" {
		return rec.HarnessSessionID
	}
	return rec.CodexThreadID
}

func formatLeadListTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(leadListTimeFormat)
}

func renderLeadSessions(out io.Writer, listing leadSessionListing, format, workDir string) error {
	if strings.EqualFold(strings.TrimSpace(format), leadListOutputJSON) {
		return writeLeadSessionsJSON(out, listing)
	}
	return writeLeadSessionsText(out, listing, workDir)
}

// writeLeadSessionsJSON emits the listing with sessions always an array, never
// null, so `jq '.sessions[]'` works on an agent with no history.
func writeLeadSessionsJSON(out io.Writer, listing leadSessionListing) error {
	if listing.Sessions == nil {
		listing.Sessions = []leadSessionView{}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(listing)
}

func writeLeadSessionsText(out io.Writer, listing leadSessionListing, workDir string) error {
	if len(listing.Sessions) == 0 {
		_, err := fmt.Fprintf(out, "No lead sessions recorded for agent %q in workspace %s.\n",
			listing.AgentID, listing.Workspace)
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SESSION\tRESUME ID\tTHREAD\tSTATUS\tSTARTED\tFINISHED\tWORKDIR")
	for _, s := range listing.Sessions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			dashIfEmpty(s.SessionID), dashIfEmpty(s.ResumeID), dashIfEmpty(s.CodexThreadName),
			dashIfEmpty(s.Status), dashIfEmpty(s.StartedAt), dashIfEmpty(s.FinishedAt),
			dashIfEmpty(s.WorkDir))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// Resume refuses a session recorded elsewhere, so say which rows this
	// shell can actually reopen rather than letting the operator discover it
	// one failed --resume at a time.
	_, _ = fmt.Fprintf(out, "\nResume from this directory (%s): loom lead --resume <RESUME ID>\n", workDir)
	_, _ = fmt.Fprintln(out, "A row with no RESUME ID was never launched under a controlled runtime and cannot be resumed.")
	return nil
}

func dashIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
