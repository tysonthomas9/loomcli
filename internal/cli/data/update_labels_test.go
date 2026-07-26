package data

import (
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var errStubUpdate = errors.New("stub update failed")

// setLabelFlags arms the repeatable label flags for one test and restores the
// package-level state afterwards.
func setLabelFlags(t *testing.T, add, remove []string) {
	t.Helper()
	resetUpdateFieldFlags(t)
	if add != nil {
		updateAddLabels = add
		setTestFlagChanged(t, updateCmd.Flags(), "add-label", true)
	}
	if remove != nil {
		updateRemoveLabels = remove
		setTestFlagChanged(t, updateCmd.Flags(), "remove-label", true)
	}
	t.Cleanup(func() { updateAddLabels = nil; updateRemoveLabels = nil })
}

// onlyUpdateParams asserts exactly one Update call and returns its params.
func onlyUpdateParams(t *testing.T, stub *localBackendStub) backend.UpdateParams {
	t.Helper()
	if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
		t.Fatalf("calls = %#v, want exactly one Update call", stub.calls)
	}
	return stub.calls[0].args.(backend.UpdateParams)
}

func TestDataUpdate_AddLabelOnly(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		setLabelFlags(t, []string{"criticized"}, nil)

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-40"})
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !strings.Contains(out, "updated loom-40") {
			t.Fatalf("output = %q, want success message", out)
		}
		params := onlyUpdateParams(t, stub)
		assertLabels(t, "AddLabels", params.AddLabels, []string{"criticized"})
		if params.RemoveLabels != nil {
			t.Errorf("RemoveLabels = %v, want nil", params.RemoveLabels)
		}
		// A label-only update must not disturb ordinary fields.
		if params.Status != nil || params.Title != nil || params.Notes != nil {
			t.Errorf("label-only update set an ordinary field: %#v", params)
		}
		// Replacement semantics are explicitly out of scope.
		if params.SetLabels != nil {
			t.Errorf("SetLabels = %v, want nil — deltas only", params.SetLabels)
		}
	})
}

func TestDataUpdate_RemoveLabelOnly(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		setLabelFlags(t, nil, []string{"needs-revision"})

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-41"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		params := onlyUpdateParams(t, stub)
		assertLabels(t, "RemoveLabels", params.RemoveLabels, []string{"needs-revision"})
		if params.AddLabels != nil {
			t.Errorf("AddLabels = %v, want nil", params.AddLabels)
		}
	})
}

// Each occurrence is one exact label: no splitting, trimming, or dedup.
func TestDataUpdate_RepeatedLabelsForwardedVerbatim(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		setLabelFlags(t, []string{"a", "b b", "c,d", "a"}, []string{"x", "y"})

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-42"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		params := onlyUpdateParams(t, stub)
		assertLabels(t, "AddLabels", params.AddLabels, []string{"a", "b b", "c,d", "a"})
		assertLabels(t, "RemoveLabels", params.RemoveLabels, []string{"x", "y"})
	})
}

func TestDataUpdate_LabelsComposeWithFields(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		setLabelFlags(t, []string{"reviewed"}, nil)
		updateTitle = "renamed"
		setTestFlagChanged(t, updateCmd.Flags(), "title", true)
		t.Cleanup(func() { updateTitle = "" })

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-43"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		params := onlyUpdateParams(t, stub)
		assertLabels(t, "AddLabels", params.AddLabels, []string{"reviewed"})
		if params.Title == nil || *params.Title != "renamed" {
			t.Errorf("Title = %#v, want renamed — labels must not displace fields", params.Title)
		}
	})
}

// The regression this feature's fieldsChanged wiring exists to prevent: with a
// dependency flag also set, RunE takes the depsChanged branch and skips Update
// entirely unless the label flags mark the params as changed.
func TestDataUpdate_LabelWithDependency_StillCallsUpdate(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		setLabelFlags(t, []string{"criticized"}, nil)
		updateAddDeps = []string{"loom-99"}
		t.Cleanup(func() { updateAddDeps = nil })

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-44"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		var updates, deps int
		var params backend.UpdateParams
		for _, c := range stub.calls {
			switch c.method {
			case "Update":
				updates++
				params = c.args.(backend.UpdateParams)
			case "AddDependency":
				deps++
			}
		}
		if updates != 1 {
			t.Fatalf("Update calls = %d, want 1 — the label was silently dropped", updates)
		}
		if deps != 1 {
			t.Fatalf("AddDependency calls = %d, want 1", deps)
		}
		assertLabels(t, "AddLabels", params.AddLabels, []string{"criticized"})
	})
}

// Update runs before dependencies, and a failed Update stops the chain.
func TestDataUpdate_LabelFailureSkipsDependencies(t *testing.T) {
	stub := &localBackendStub{updateErr: errStubUpdate}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		setLabelFlags(t, []string{"criticized"}, nil)
		updateAddDeps = []string{"loom-99"}
		t.Cleanup(func() { updateAddDeps = nil })

		_, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-45"})
		})
		if err == nil {
			t.Fatal("want the Update error surfaced")
		}
		for _, c := range stub.calls {
			if c.method == "AddDependency" {
				t.Error("dependencies must not run after a failed Update")
			}
		}
	})
}

// Omitted flags leave both slices absent, preserving existing behavior.
func TestDataUpdate_NoLabelFlagsLeavesParamsClean(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		resetUpdateFieldFlags(t)
		updateTitle = "just a title"
		setTestFlagChanged(t, updateCmd.Flags(), "title", true)
		t.Cleanup(func() { updateTitle = "" })

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-46"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		params := onlyUpdateParams(t, stub)
		if params.AddLabels != nil || params.RemoveLabels != nil {
			t.Errorf("labels = add:%v remove:%v, want both nil", params.AddLabels, params.RemoveLabels)
		}
	})
}

func assertLabels(t *testing.T, field string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v (order is preserved verbatim)", field, got, want)
		}
	}
}
