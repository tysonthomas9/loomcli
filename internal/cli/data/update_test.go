package data

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func strptr(s string) *string { return &s }

// TestEnforceBlockReason guards the rule that an issue can't be moved to
// "blocked" without a reason — so a blocked card always carries a human-readable
// note the board surfaces (isBlockedWithNotes), instead of a silent blocked chip.
func TestEnforceBlockReason(t *testing.T) {
	withNotes := &backend.IssueDetailData{IssueData: backend.IssueData{Notes: "prev reason"}}
	noNotes := &backend.IssueDetailData{}
	cases := []struct {
		name    string
		params  backend.UpdateParams
		detail  *backend.IssueDetailData
		wantErr bool
	}{
		{"non-block status is ignored", backend.UpdateParams{Status: strptr("open")}, noNotes, false},
		{"block with inline notes ok", backend.UpdateParams{Status: strptr("blocked"), Notes: strptr("BLOCKED: x")}, noNotes, false},
		{"block with existing notes ok", backend.UpdateParams{Status: strptr("blocked")}, withNotes, false},
		{"block with empty inline notes + none existing errors", backend.UpdateParams{Status: strptr("blocked"), Notes: strptr("  ")}, noNotes, true},
		{"bare block, no reason errors", backend.UpdateParams{Status: strptr("blocked")}, noNotes, true},
		{"bare block, issue not found fails open", backend.UpdateParams{Status: strptr("blocked")}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &localBackendStub{detail: tc.detail}
			err := enforceBlockReason(context.Background(), stub, "WEB-1", tc.params)
			if (err != nil) != tc.wantErr {
				t.Fatalf("enforceBlockReason err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestDataUpdate_TitleAndDescriptionFlags(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		updateTitle = "new title"
		updateDescription = "new body"
		t.Cleanup(func() { updateTitle = ""; updateDescription = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "title", true)
		setTestFlagChanged(t, updateCmd.Flags(), "description", true)

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-99"})
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !strings.Contains(out, "updated loom-99") {
			t.Fatalf("update output = %q, want success message", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want one Update call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.Title == nil || *params.Title != "new title" {
			t.Fatalf("Update title = %#v, want %q", params.Title, "new title")
		}
		if params.Description == nil || *params.Description != "new body" {
			t.Fatalf("Update description = %#v, want %q", params.Description, "new body")
		}
	})
}

func TestDataUpdate_DescriptionFromFile(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		body := "long\nmulti-line\nbody"
		dir := t.TempDir()
		path := filepath.Join(dir, "body.txt")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write tempfile: %v", err)
		}

		outputFormat = "text"
		updateDescFile = path
		t.Cleanup(func() { updateDescFile = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "description-from-file", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-100"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 1 {
			t.Fatalf("calls = %#v, want one call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.Description == nil || *params.Description != body {
			t.Fatalf("Update description = %#v, want %q", params.Description, body)
		}
	})
}

func TestDataUpdate_DescriptionFromStdin(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		body := "piped\ndescription"
		outputFormat = "text"
		updateDescFile = "-"
		t.Cleanup(func() { updateDescFile = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "description-from-file", true)

		updateCmd.SetIn(strings.NewReader(body))
		t.Cleanup(func() { updateCmd.SetIn(os.Stdin) })

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-101"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 1 {
			t.Fatalf("calls = %#v, want one call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.Description == nil || *params.Description != body {
			t.Fatalf("Update description = %#v, want %q", params.Description, body)
		}
	})
}

func TestDataUpdate_DescriptionAndFileAreMutuallyExclusive(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		updateDescription = "inline body"
		updateDescFile = "/tmp/does-not-matter"
		t.Cleanup(func() { updateDescription = ""; updateDescFile = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "description", true)
		setTestFlagChanged(t, updateCmd.Flags(), "description-from-file", true)

		_, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-102"})
		})
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("error = %v, want 'mutually exclusive'", err)
		}
		if len(stub.calls) != 0 {
			t.Fatalf("calls = %#v, want no backend calls", stub.calls)
		}
	})
}

func TestDataUpdate_EmptyDescriptionClearsField(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		updateDescription = ""
		t.Cleanup(func() { updateDescription = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "description", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-103"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 1 {
			t.Fatalf("calls = %#v, want one call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.Description == nil {
			t.Fatalf("Update description = nil, want non-nil pointer to empty string")
		}
		if *params.Description != "" {
			t.Fatalf("Update description = %q, want empty string", *params.Description)
		}
	})
}

func TestDataUpdate_ExternalRefFlag(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		updateExternalRef = "local-branch:loom/TASK-1@abcdef1234567890"
		t.Cleanup(func() { updateExternalRef = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "external-ref", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"TASK-1"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want one Update call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.ExternalRef == nil || *params.ExternalRef != updateExternalRef {
			t.Fatalf("Update external ref = %#v, want %q", params.ExternalRef, updateExternalRef)
		}
	})
}

// resetUpdateFieldFlags clears any Changed state leaked onto updateCmd's
// field flags by earlier tests in the package, so dependency-flag tests see
// a deterministic "no field flags set" baseline.
func resetUpdateFieldFlags(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"status", "assignee", "notes", "design", "priority",
		"title", "description", "description-from-file", "external-ref",
	} {
		setTestFlagChanged(t, updateCmd.Flags(), name, false)
	}
}

func TestDataUpdate_DependsOnOnly_SkipsFieldUpdate(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		outputFormat = "text"
		updateAddDeps = []string{"dep-1", "dep-2"}
		t.Cleanup(func() { updateAddDeps = nil })

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-7"})
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !strings.Contains(out, "updated loom-7") {
			t.Fatalf("update output = %q, want success message", out)
		}
		if len(stub.calls) != 2 {
			t.Fatalf("calls = %#v, want exactly two AddDependency calls (no field Update)", stub.calls)
		}
		for i, wantDep := range []string{"dep-1", "dep-2"} {
			call := stub.calls[i]
			if call.method != "AddDependency" {
				t.Fatalf("calls[%d].method = %q, want AddDependency", i, call.method)
			}
			params := call.args.(backend.DepAddParams)
			if params.FromID != "loom-7" || params.ToID != wantDep || params.DepType != "blocks" {
				t.Errorf("calls[%d] params = %#v, want loom-7 -> %s (blocks)", i, params, wantDep)
			}
		}
	})
}

func TestDataUpdate_FieldsAndDependencyFlags_BothApplied(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		outputFormat = "text"
		updateTitle = "retitled"
		updateAddDeps = []string{"dep-3"}
		updateRemoveDeps = []string{"dep-4"}
		t.Cleanup(func() {
			updateTitle = ""
			updateAddDeps = nil
			updateRemoveDeps = nil
		})
		setTestFlagChanged(t, updateCmd.Flags(), "title", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-8"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 3 {
			t.Fatalf("calls = %#v, want Update + AddDependency + RemoveDependency", stub.calls)
		}
		if stub.calls[0].method != "Update" {
			t.Fatalf("calls[0].method = %q, want Update (fields apply before dependency edges)", stub.calls[0].method)
		}
		if got := stub.calls[1]; got.method != "AddDependency" || got.args.(backend.DepAddParams).ToID != "dep-3" {
			t.Fatalf("calls[1] = %#v, want AddDependency dep-3", got)
		}
		rm := stub.calls[2]
		if rm.method != "RemoveDependency" {
			t.Fatalf("calls[2].method = %q, want RemoveDependency", rm.method)
		}
		rmParams := rm.args.(backend.DepRemoveParams)
		if rmParams.FromID != "loom-8" || rmParams.ToID != "dep-4" {
			t.Errorf("RemoveDependency params = %#v, want loom-8 -> dep-4", rmParams)
		}
	})
}
