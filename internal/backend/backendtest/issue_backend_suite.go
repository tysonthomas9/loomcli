// Package backendtest holds the shared conformance suite that every
// backend.IssueBackend implementation must pass — the issue-tracker backend
// (local fleet-db, cloud fleet-db, direct fleet, Loom API), not the AI CLI
// backends in internal/cli/backends. Imported only by tests; currently just
// internal/cli/issue_backend_conformance_e2e_test.go.
package backendtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// IssueBackendSuiteConfig configures the shared IssueBackend conformance suite.
type IssueBackendSuiteConfig struct {
	// NewBackend returns a backend wired to an isolated workspace. It may return
	// the same backend instance across calls if the implementation is safe for
	// sequential use.
	NewBackend func(t testing.TB) backend.IssueBackend

	// SupportsExplicitCreateID enables the strict CreateParams.ID contract test.
	// Leave false for backends with a known server-side generated-ID limitation.
	SupportsExplicitCreateID bool
}

// RunIssueBackendConformance runs behavior that every IssueBackend should share
// regardless of whether callers reached it through local fleet-db, cloud
// fleet-db, direct fleet, or the Loom API backend.
func RunIssueBackendConformance(t *testing.T, cfg IssueBackendSuiteConfig) {
	t.Helper()
	if cfg.NewBackend == nil {
		t.Fatal("IssueBackendSuiteConfig.NewBackend is required")
	}

	t.Run("CreateListGetUpdateCloseReopen", func(t *testing.T) {
		runCreateListGetUpdateCloseReopen(t, cfg.NewBackend(t))
	})
	t.Run("ReadyAndBlockedAgreeOnUnblockedIssue", func(t *testing.T) {
		runReadyAndBlockedAgreeOnUnblockedIssue(t, cfg.NewBackend(t))
	})
	t.Run("ExplicitCreateID", func(t *testing.T) {
		runExplicitCreateID(t, cfg)
	})
}

func runCreateListGetUpdateCloseReopen(t *testing.T, ib backend.IssueBackend) {
	t.Helper()
	ctx := suiteContext(t)
	created := createIssue(t, ctx, ib, "lifecycle")
	assertIssue(t, created, "lifecycle", "open")

	assertListIncludes(t, ctx, ib, created.ID)
	assertGetMatches(t, ctx, ib, created)
	assertUpdateTitle(t, ctx, ib, created)
	assertCloseReopen(t, ctx, ib, created.ID)
}

func runReadyAndBlockedAgreeOnUnblockedIssue(t *testing.T, ib backend.IssueBackend) {
	t.Helper()
	ctx := suiteContext(t)
	created := createIssue(t, ctx, ib, "ready")

	ready, err := ib.Ready(ctx, backend.ReadyOpts{Limit: 100})
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if !containsIssueID(ready, created.ID) {
		t.Fatalf("Ready did not include unblocked issue %q; got ids %v", created.ID, issueIDs(ready))
	}

	blocked, err := ib.Blocked(ctx, backend.BlockedOpts{Limit: 100})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if containsIssueID(blocked, created.ID) {
		t.Fatalf("Blocked unexpectedly included unblocked issue %q", created.ID)
	}
}

func runExplicitCreateID(t *testing.T, cfg IssueBackendSuiteConfig) {
	t.Helper()
	if !cfg.SupportsExplicitCreateID {
		t.Skip("backend does not currently honor CreateParams.ID")
	}
	ib := cfg.NewBackend(t)
	ctx := suiteContext(t)
	wantID := "CONTRACT-" + strings.ToUpper(safeName(t.Name()))
	issue, err := ib.Create(ctx, backend.CreateParams{
		ID:        wantID,
		Title:     uniqueTitle(t, "explicit-id"),
		Status:    "open",
		IssueType: "task",
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("Create explicit ID: %v", err)
	}
	if issue.ID != wantID {
		t.Fatalf("CreateParams.ID was not honored: got %q, want %q", issue.ID, wantID)
	}
}

func assertListIncludes(t *testing.T, ctx context.Context, ib backend.IssueBackend, id string) {
	t.Helper()
	listed, err := ib.List(ctx, backend.ListOpts{Status: "open", Limit: 100})
	if err != nil {
		t.Fatalf("List(open): %v", err)
	}
	if !containsIssueID(listed, id) {
		t.Fatalf("List(open) did not include created issue %q; got ids %v", id, issueIDs(listed))
	}
}

func assertGetMatches(t *testing.T, ctx context.Context, ib backend.IssueBackend, issue *backend.IssueData) {
	t.Helper()
	detail, err := ib.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", issue.ID, err)
	}
	if detail.ID != issue.ID || detail.Title != issue.Title {
		t.Fatalf("Get(%s) = {ID:%q Title:%q}, want {ID:%q Title:%q}",
			issue.ID, detail.ID, detail.Title, issue.ID, issue.Title)
	}
}

func assertUpdateTitle(t *testing.T, ctx context.Context, ib backend.IssueBackend, issue *backend.IssueData) {
	t.Helper()
	updatedTitle := issue.Title + " updated"
	if err := ib.Update(ctx, issue.ID, backend.UpdateParams{Title: &updatedTitle}); err != nil {
		t.Fatalf("Update title: %v", err)
	}
	detail, err := ib.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if detail.Title != updatedTitle {
		t.Fatalf("updated title = %q, want %q", detail.Title, updatedTitle)
	}
}

func assertCloseReopen(t *testing.T, ctx context.Context, ib backend.IssueBackend, id string) {
	t.Helper()
	closed, err := ib.Close(ctx, id, backend.CloseParams{Reason: "backend conformance"})
	if err != nil {
		t.Fatalf("Close(%s): %v", id, err)
	}
	if closed == nil || closed.Closed == nil || closed.Closed.ID != id {
		t.Fatalf("Close result = %#v, want closed issue %q", closed, id)
	}
	if err := ib.Reopen(ctx, id, backend.ReopenParams{Reason: "backend conformance"}); err != nil {
		t.Fatalf("Reopen(%s): %v", id, err)
	}
	detail, err := ib.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after Reopen: %v", err)
	}
	if detail.Status != "open" {
		t.Fatalf("status after Reopen = %q, want open", detail.Status)
	}
}

func suiteContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func createIssue(t *testing.T, ctx context.Context, ib backend.IssueBackend, suffix string) *backend.IssueData {
	t.Helper()
	issue, err := ib.Create(ctx, backend.CreateParams{
		Title:       uniqueTitle(t, suffix),
		Description: "created by IssueBackend conformance suite",
		Status:      "open",
		IssueType:   "task",
		Priority:    2,
		Labels:      []string{"backend-conformance"},
	})
	if err != nil {
		t.Fatalf("Create(%s): %v", suffix, err)
	}
	return issue
}

func assertIssue(t *testing.T, issue *backend.IssueData, titleSuffix, status string) {
	t.Helper()
	if issue == nil {
		t.Fatal("Create returned nil issue")
	}
	if issue.ID == "" {
		t.Fatal("Create returned empty issue ID")
	}
	if !strings.Contains(issue.Title, titleSuffix) {
		t.Fatalf("created title = %q, want suffix %q", issue.Title, titleSuffix)
	}
	if issue.Status != status {
		t.Fatalf("created status = %q, want %q", issue.Status, status)
	}
}

func uniqueTitle(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("backend conformance %s %d", suffix, time.Now().UnixNano())
}

func containsIssueID(items []backend.IssueData, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func issueIDs(items []backend.IssueData) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, name)
	if len(name) > 24 {
		name = name[len(name)-24:]
	}
	return strings.Trim(name, "-")
}
