package data

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

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
