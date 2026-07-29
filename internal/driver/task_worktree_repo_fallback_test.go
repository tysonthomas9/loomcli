package driver

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
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

// A task with no repo selector lands in the alphabetically first workspace repo
// because the list is sorted by name. That is arbitrary: with both registered, a
// loomcli task is worked inside the fleet-db checkout. The pick is kept (most
// issues currently arrive without a source_repo, and failing them would stop
// dispatch) but it must be visible in the log.
func TestSelectRepo_NoSelectorWarnsAboutTheArbitraryPick(t *testing.T) {
	buf := captureWarnings(t)

	repos := []*domain.Repo{
		{Name: "fleet-db"},
		{Name: "harness-wrapper"},
		{Name: "loomcli"},
	}
	r := LocalTaskWorktreeResolver{}

	got, err := r.selectRepo(context.Background(), "the live workspace", repos, TaskExecRequest{TaskID: "task-42"})
	if err != nil {
		t.Fatalf("selectRepo returned error: %v", err)
	}
	if got == nil || got.Name != "fleet-db" {
		t.Fatalf("selectRepo = %v, want the first repo (fleet-db)", got)
	}

	logged := buf.String()
	if !strings.Contains(logged, "no repo selector") {
		t.Errorf("fallback was not logged:\n%s", logged)
	}
	// The task id makes a wrong-repo diff traceable back to this pick, and the
	// full candidate list is what tells a reader the pick was arbitrary rather
	// than resolved — a chosen repo on its own looks deliberate.
	for _, want := range []string{"task-42", "fleet-db", "harness-wrapper", "loomcli"} {
		if !strings.Contains(logged, want) {
			t.Errorf("warning does not mention %q:\n%s", want, logged)
		}
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
