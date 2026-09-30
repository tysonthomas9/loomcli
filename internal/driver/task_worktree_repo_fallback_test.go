package driver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// captureWarnings redirects slog for the duration of a test and returns the
// accumulated output.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestSelectRepo_NoSelectorRequiresSelection(t *testing.T) {
	buf := captureWarnings(t)

	repos := []*domain.Repo{
		{Name: "fleet-db"},
		{Name: "harness-wrapper"},
		{Name: "loomcli"},
	}
	r := LocalTaskWorktreeResolver{}

	got, err := r.selectRepo(context.Background(), "the live workspace", repos, TaskExecRequest{TaskID: "task-42"})
	if got != nil || !errors.Is(err, loomgit.NewError(loomgit.RepoSelectionRequired, "", nil)) {
		t.Fatalf("selectRepo = (%v, %v), want repo_selection_required", got, err)
	}
	for _, want := range []string{"task-42", "fleet-db", "harness-wrapper", "loomcli"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected warning: %s", buf.String())
	}
}

// A resolved selector is not a fallback and must stay quiet, or the warning
// becomes noise that gets filtered out.
func TestSelectRepo_ResolvedSelectorDoesNotWarn(t *testing.T) {
	buf := captureWarnings(t)

	repos := []*domain.Repo{
		{Name: "fleet-db"},
		{Name: "loomcli"},
	}
	r := LocalTaskWorktreeResolver{}
	req := TaskExecRequest{TaskID: ""}
	req.RunnerPlacement.RepoRef = "loomcli"

	got, err := r.selectRepo(context.Background(), "the live workspace", repos, req)
	if err != nil {
		t.Fatalf("selectRepo returned error: %v", err)
	}
	if got == nil || got.Name != "loomcli" {
		t.Fatalf("selectRepo = %v, want loomcli", got)
	}
	if logged := buf.String(); strings.Contains(logged, "no repo selector") {
		t.Errorf("resolved selector should not warn:\n%s", logged)
	}
}

// A single-repo workspace has nothing to choose between, so the pick is not
// arbitrary and should not warn either.
func TestSelectRepo_SingleRepoDoesNotWarn(t *testing.T) {
	buf := captureWarnings(t)

	repos := []*domain.Repo{{Name: "loomcli"}}
	r := LocalTaskWorktreeResolver{}

	got, err := r.selectRepo(context.Background(), "the live workspace", repos, TaskExecRequest{TaskID: ""})
	if err != nil {
		t.Fatalf("selectRepo returned error: %v", err)
	}
	if got == nil || got.Name != "loomcli" {
		t.Fatalf("selectRepo = %v, want loomcli", got)
	}
	if logged := buf.String(); strings.Contains(logged, "no repo selector") {
		t.Errorf("single-repo workspace should not warn:\n%s", logged)
	}
}
