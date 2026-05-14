package data

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestCreateCommand_UsesLocalBackend(t *testing.T) {
	stub := &localBackendStub{
		createItem: &backend.IssueData{
			ID:         "loom-123",
			Title:      "Add local mode setup",
			Status:     "open",
			IssueType:  "task",
			Priority:   1,
			SourceRepo: "loomcli",
		},
	}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		cmd := newCreateCmd()
		cmd.SetArgs([]string{
			"--title", "Add local mode setup",
			"--type", "task",
			"--priority", "1",
			"--status", "open",
			"--parent", "epic-1",
			"--source-repo", "loomcli",
			"--description", "Make setup one command",
			"--label", "local-mode",
			"--label", "podman",
			"--depends-on", "dep-1",
			"--estimated-minutes", "45",
		})

		out, err := captureDataStdout(t, cmd.Execute)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if !strings.Contains(out, "created loom-123") || !strings.Contains(out, "Add local mode setup") {
			t.Fatalf("create output = %q, want created issue", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Create" {
			t.Fatalf("calls = %#v, want one Create call", stub.calls)
		}
		params := stub.calls[0].args.(backend.CreateParams)
		if params.Title != "Add local mode setup" || params.IssueType != "task" || params.Priority != 1 {
			t.Fatalf("Create params basic fields = %#v", params)
		}
		if params.Status != "open" || params.Parent != "epic-1" || params.SourceRepo != "loomcli" {
			t.Fatalf("Create params routing fields = %#v", params)
		}
		if len(params.Labels) != 2 || params.Labels[0] != "local-mode" || params.Labels[1] != "podman" {
			t.Fatalf("Create labels = %#v", params.Labels)
		}
		if len(params.Dependencies) != 1 || params.Dependencies[0] != "dep-1" {
			t.Fatalf("Create dependencies = %#v", params.Dependencies)
		}
		if params.EstimatedMinutes == nil || *params.EstimatedMinutes != 45 {
			t.Fatalf("Create estimated minutes = %#v", params.EstimatedMinutes)
		}
	})
}

func TestCreateCommand_JSONPrintsCreatedIssue(t *testing.T) {
	stub := &localBackendStub{
		createItem: &backend.IssueData{ID: "epic-1", Title: "Local mode", IssueType: "epic", Priority: 2},
	}
	withLocalBackend(t, stub, func() {
		outputFormat = "json"
		cmd := newCreateCmd()
		cmd.SetArgs([]string{"--title", "Local mode", "--type", "epic"})

		out, err := captureDataStdout(t, cmd.Execute)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var decoded backend.IssueData
		if err := json.Unmarshal([]byte(out), &decoded); err != nil {
			t.Fatalf("decode JSON: %v (out=%q)", err, out)
		}
		if decoded.ID != "epic-1" || decoded.Title != "Local mode" {
			t.Fatalf("decoded created issue = %#v", decoded)
		}
	})
}

func TestCreateCommand_RequiresTitle(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		cmd := newCreateCmd()
		cmd.SetArgs([]string{"--type", "task"})

		_, err := captureDataStdout(t, cmd.Execute)
		if err == nil {
			t.Fatal("expected missing title error")
		}
		if !strings.Contains(err.Error(), "--title is required") {
			t.Fatalf("error = %q, want title requirement", err.Error())
		}
		if len(stub.calls) != 0 {
			t.Fatalf("calls = %#v, want no backend calls", stub.calls)
		}
	})
}

func TestCreateCommand_RejectsIDFlag(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		cmd := newCreateCmd()
		cmd.SetArgs([]string{"--title", "Local mode", "--id", "E2E-99"})

		_, err := captureDataStdout(t, cmd.Execute)
		if err == nil {
			t.Fatal("expected unknown --id flag error")
		}
		if !strings.Contains(err.Error(), "unknown flag: --id") {
			t.Fatalf("error = %q, want unknown --id flag", err.Error())
		}
		if len(stub.calls) != 0 {
			t.Fatalf("calls = %#v, want no backend calls", stub.calls)
		}
	})
}
