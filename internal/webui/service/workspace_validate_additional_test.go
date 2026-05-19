package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

func TestWorkspaceKeyFromNameEdgeCases(t *testing.T) {
	cases := map[string]string{
		"hello_world.1":         "HELLO-WORLD-1",
		"123":                   "W-123",
		"!!!":                   "W",
		"":                      "W",
		strings.Repeat("a", 40): strings.Repeat("A", 32),
	}
	for in, want := range cases {
		if got := WorkspaceKeyFromName(in); got != want {
			t.Fatalf("WorkspaceKeyFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkspaceCreateWarningsAndStartingError(t *testing.T) {
	ctx := WithCreateWarnings(context.Background())
	if warnings := GetCreateWarnings(ctx); warnings != nil {
		t.Fatalf("initial warnings = %#v, want nil", warnings)
	}
	AddCreateWarning(ctx, "first")
	AddCreateWarning(context.Background(), "ignored")
	if warnings := GetCreateWarnings(ctx); len(warnings) != 1 || warnings[0] != "first" {
		t.Fatalf("warnings = %#v, want [first]", warnings)
	}

	err := ErrStarting("daemon warming up")
	if err.Kind != KindStarting || !strings.Contains(err.Error(), "daemon warming up") {
		t.Fatalf("ErrStarting = %#v", err)
	}
}

func TestWorkspaceValidationBranches(t *testing.T) {
	if err := validateWorkspaceName("valid_Name-1"); err != nil {
		t.Fatalf("validateWorkspaceName valid: %v", err)
	}
	for _, name := range []string{"", strings.Repeat("a", maxWorkspaceNameLen+1), "bad space"} {
		if err := validateWorkspaceName(name); err == nil {
			t.Fatalf("validateWorkspaceName(%q) succeeded, want error", name)
		}
	}

	createCases := []WorkspaceCreateRequest{
		{Name: "empty", Type: "empty"},
		{Name: "clone", Type: "clone", CloneURLs: []string{"https://github.com/example/repo.git"}},
	}
	for _, req := range createCases {
		if err := validateWorkspaceCreateRequest(&req); err != nil {
			t.Fatalf("validateWorkspaceCreateRequest(%+v): %v", req, err)
		}
	}
	invalidCreates := []WorkspaceCreateRequest{
		{},
		{Name: strings.Repeat("a", maxWorkspaceNameLen+1), Type: "empty"},
		{Name: "bad space", Type: "empty"},
		{Name: "clone", Type: "clone"},
		{Name: "clone", Type: "clone", CloneURLs: []string{"file:///tmp/repo"}},
		{Name: "template", Type: "template"},
		{Name: "missing-type"},
		{Name: "bad-type", Type: "zip"},
	}
	for _, req := range invalidCreates {
		if err := validateWorkspaceCreateRequest(&req); err == nil {
			t.Fatalf("validateWorkspaceCreateRequest(%+v) succeeded, want error", req)
		}
	}
}

func TestWorkspaceAddReposNormalizationAndValidation(t *testing.T) {
	req := normalizeWorkspaceAddReposRequest(WorkspaceAddReposRequest{
		WorkspaceID: "WS",
		Repos:       []string{" /repo/a ", "", "https://github.com/example/repo.git"},
		CloneURLs:   []string{" ", "git@github.com:example/other.git"},
	})
	if len(req.Repos) != 1 || req.Repos[0] != "/repo/a" {
		t.Fatalf("Repos = %#v, want trimmed local repo", req.Repos)
	}
	if len(req.CloneURLs) != 2 {
		t.Fatalf("CloneURLs = %#v, want explicit and repo-derived clone URLs", req.CloneURLs)
	}
	if err := validateWorkspaceAddReposRequest(&req); err != nil {
		t.Fatalf("validateWorkspaceAddReposRequest valid: %v", err)
	}
	for _, invalid := range []WorkspaceAddReposRequest{
		{},
		{WorkspaceID: "WS"},
		{WorkspaceID: "WS", CloneURLs: []string{"https://localhost/repo.git"}},
	} {
		if err := validateWorkspaceAddReposRequest(&invalid); err == nil {
			t.Fatalf("validateWorkspaceAddReposRequest(%+v) succeeded, want error", invalid)
		}
	}
}

func TestCloneURLValidationHostParsingAndBlocks(t *testing.T) {
	valid := []string{
		"https://github.com/example/repo.git",
		"git@github.com:example/repo.git",
		"git@[2001:4860:4860::8888]:example/repo.git",
	}
	for _, u := range valid {
		if err := ValidateCloneURL(u); err != nil {
			t.Fatalf("ValidateCloneURL(%q): %v", u, err)
		}
		if host, err := extractCloneHost(u); err != nil || host == "" {
			t.Fatalf("extractCloneHost(%q) = %q, %v; want host", u, host, err)
		}
	}
	invalid := []string{
		"file:///tmp/repo",
		"https://",
		"https://github.com/-bad/repo",
		"https://github.com/repo\n.git",
		"https://127.0.0.1/repo.git",
		"https://10.0.0.1/repo.git",
		"https://169.254.169.254/repo.git",
		"https://100.64.0.1/repo.git",
		"https://metadata.google.internal/repo.git",
		"git@:example/repo.git",
		"git@[::1:example/repo.git",
	}
	for _, u := range invalid {
		if err := ValidateCloneURL(u); err == nil {
			t.Fatalf("ValidateCloneURL(%q) succeeded, want error", u)
		}
	}
	if !isBlockedCloneHost("localhost:22") || isBlockedCloneHost("github.com") {
		t.Fatalf("isBlockedCloneHost did not classify hosts as expected")
	}
}

func TestClassifyWorkspaceCreateError(t *testing.T) {
	cases := []struct {
		code workspaceerrors.Code
		kind ErrorKind
	}{
		{workspaceerrors.AlreadyExists, KindConflict},
		{workspaceerrors.PathNotFound, KindValidation},
		{workspaceerrors.NotGitRepo, KindValidation},
		{workspaceerrors.GitFailed, KindValidation},
		{workspaceerrors.SecurityViolation, KindForbidden},
		{workspaceerrors.ConfigFailed, KindInternal},
	}
	for _, tc := range cases {
		err := classifyWorkspaceCreateError(workspaceerrors.New(tc.code, "message", errors.New("cause")))
		if err.Kind != tc.kind {
			t.Fatalf("code %s classified as %s, want %s", tc.code, err.Kind, tc.kind)
		}
	}
	if err := classifyWorkspaceCreateError(errors.New("plain")); err.Kind != KindInternal {
		t.Fatalf("plain error classified as %s, want internal", err.Kind)
	}
}

func TestIssueHelperConversions(t *testing.T) {
	mins := 30
	params := &CreateIssueParams{
		ID: "ISSUE-1", Parent: "EPIC-1", Title: "Title", Description: "Desc",
		Status: string(types.StatusOpen), IssueType: string(types.TypeTask), Priority: 2,
		Design: "Design", AcceptanceCriteria: "AC", Notes: "Notes", Assignee: "nova",
		ExternalRef: "gh-1", EstimatedMinutes: &mins, Labels: []string{"a"}, Dependencies: []string{"DEP-1"},
		CreatedBy: "user", Owner: "owner", DueAt: "2026-01-01T00:00:00Z", DeferUntil: "2026-01-02T00:00:00Z",
		SourceRepo: "api",
	}
	args := toCreateArgs(params)
	if args.ID != "ISSUE-1" || args.Parent != "EPIC-1" || args.SourceRepo != "api" || args.EstimatedMinutes == nil || *args.EstimatedMinutes != mins {
		t.Fatalf("toCreateArgs = %+v, want copied fields", args)
	}
	if err := validateCreateParams(params); err != nil {
		t.Fatalf("validateCreateParams valid: %v", err)
	}
	for _, invalid := range []*CreateIssueParams{
		{IssueType: string(types.TypeTask), Priority: 1},
		{Title: "x", Priority: 1},
		{Title: "x", IssueType: "invalid", Priority: 1},
		{Title: "x", IssueType: string(types.TypeTask), Priority: 5},
		{Title: "x", IssueType: string(types.TypeTask), Priority: 1, Status: "closed"},
		{Title: "x", IssueType: string(types.TypeTask), Priority: 1, Labels: make([]string, maxLabels+1)},
		{Title: "x", IssueType: string(types.TypeTask), Priority: 1, Dependencies: make([]string, maxDependencies+1)},
	} {
		if err := validateCreateParams(invalid); err == nil {
			t.Fatalf("validateCreateParams(%+v) succeeded, want error", invalid)
		}
	}

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	external := "jira-1"
	issue := &types.Issue{
		Title: "Moved", Description: "Existing", IssueType: types.TypeBug, Priority: 1,
		Design: "D", AcceptanceCriteria: "AC", Notes: "N", Assignee: "nova", Owner: "owner",
		ExternalRef: &external, EstimatedMinutes: &mins, Labels: []string{"bug"}, DueAt: &now, DeferUntil: &now,
	}
	moveArgs := buildMoveCreateArgs(issue, "OLD-1")
	if moveArgs.Title != "Moved" || !strings.Contains(moveArgs.Description, "Moved from OLD-1") ||
		moveArgs.ExternalRef != "jira-1" || moveArgs.DueAt != now.Format(time.RFC3339) ||
		moveArgs.CreatedBy != "web-ui" {
		t.Fatalf("buildMoveCreateArgs = %+v, want copied move fields", moveArgs)
	}
	if formatTimePtr(nil) != "" {
		t.Fatalf("formatTimePtr(nil) should be empty")
	}
}
