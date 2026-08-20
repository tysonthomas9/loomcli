package data

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// resetUpdateFieldFlags clears any Changed state leaked onto updateCmd's
// field flags by earlier tests in the package, so dependency-flag tests see
// a deterministic "no field flags set" baseline.
func resetUpdateFieldFlags(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"status", "assignee", "notes", "design", "priority",
		"title", "description", "description-from-file",
		"add-label", "remove-label", "force",
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

// resetUpdateLabelFlagVars clears the label slice vars (and the dependency
// slices, whose LENGTH — not Changed() — decides whether RunE skips Update) so
// each label test starts and ends from a clean, order-independent baseline.
//
// Nilling the slices is also what keeps repeated real parsing safe: pflag's
// stringArrayValue tracks its own "changed" bool that no test helper can reach,
// so the second ParseFlags of a --add-label in one test binary APPENDS instead
// of replacing. Appending to a nil slice yields the expected result anyway.
func resetUpdateLabelFlagVars(t *testing.T) {
	t.Helper()
	updateAddLabels = nil
	updateRemoveLabels = nil
	updateAddDeps = nil
	updateRemoveDeps = nil
	updateForce = false
	t.Cleanup(func() {
		updateAddLabels = nil
		updateRemoveLabels = nil
		updateAddDeps = nil
		updateRemoveDeps = nil
		updateForce = false
	})
}

func TestDataUpdate_AddLabelOnly(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateAddLabels = []string{"criticized"}
		setTestFlagChanged(t, updateCmd.Flags(), "add-label", true)

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-40"})
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !strings.Contains(out, "updated loom-40") {
			t.Fatalf("update output = %q, want success message", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" || stub.calls[0].id != "loom-40" {
			t.Fatalf("calls = %#v, want one Update call for loom-40", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if want := []string{"criticized"}; !reflect.DeepEqual(params.AddLabels, want) {
			t.Fatalf("Update AddLabels = %#v, want %#v", params.AddLabels, want)
		}
		if params.RemoveLabels != nil {
			t.Errorf("Update RemoveLabels = %#v, want nil", params.RemoveLabels)
		}
		// --add-label is a delta, never a wholesale replacement: SetLabels must
		// stay nil so the backend keeps the issue's other labels.
		if params.SetLabels != nil {
			t.Errorf("Update SetLabels = %#v, want nil (deltas only, never a set)", params.SetLabels)
		}
	})
}

func TestDataUpdate_RemoveLabelOnly(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateRemoveLabels = []string{"needs-revision"}
		setTestFlagChanged(t, updateCmd.Flags(), "remove-label", true)

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-41"})
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !strings.Contains(out, "updated loom-41") {
			t.Fatalf("update output = %q, want success message", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want one Update call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if want := []string{"needs-revision"}; !reflect.DeepEqual(params.RemoveLabels, want) {
			t.Fatalf("Update RemoveLabels = %#v, want %#v", params.RemoveLabels, want)
		}
		if params.AddLabels != nil {
			t.Errorf("Update AddLabels = %#v, want nil", params.AddLabels)
		}
		if params.SetLabels != nil {
			t.Errorf("Update SetLabels = %#v, want nil (deltas only, never a set)", params.SetLabels)
		}
	})
}

// TestDataUpdate_RepeatedLabelFlagsForwardedVerbatim pins what the CLI layer
// does with already-parsed occurrences: forwards them in order, byte-for-byte,
// without normalizing (no dedup, no trimming), and never reads the issue's
// current labels first (deltas, not read-modify-write). It sets the slice vars
// directly, so it does NOT cover parsing — TestUpdateLabelFlags_ParseSemantics
// owns the StringArray-vs-StringSlice contract. Note "x,y" is forwarded as one
// label here but fleet-db rejects commas; that rejection is the backend's call,
// which is exactly why the CLI stays out of it.
func TestDataUpdate_RepeatedLabelFlagsForwardedVerbatim(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateAddLabels = []string{"a", "b", "c", "a", " spaced ", "x,y"}
		updateRemoveLabels = []string{"z", "z"}
		setTestFlagChanged(t, updateCmd.Flags(), "add-label", true)
		setTestFlagChanged(t, updateCmd.Flags(), "remove-label", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-42"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		// Exactly one call, and it is Update: no Get/fetch of current labels.
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want only an Update call (no Get of current labels)", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		wantAdd := []string{"a", "b", "c", "a", " spaced ", "x,y"}
		if !reflect.DeepEqual(params.AddLabels, wantAdd) {
			t.Fatalf("Update AddLabels = %#v, want %#v (order preserved, no dedup/trim/split)", params.AddLabels, wantAdd)
		}
		wantRemove := []string{"z", "z"}
		if !reflect.DeepEqual(params.RemoveLabels, wantRemove) {
			t.Fatalf("Update RemoveLabels = %#v, want %#v", params.RemoveLabels, wantRemove)
		}
		if params.SetLabels != nil {
			t.Errorf("Update SetLabels = %#v, want nil (deltas only, never a set)", params.SetLabels)
		}
	})
}

// TestUpdateLabelFlags_ParseSemantics drives real pflag parsing, which the
// stub-based tests above deliberately bypass. It is what pins the flags to
// StringArray: StringSlice would comma-split "x,y" into two labels, and a plain
// StringVar would drop all but the last occurrence.
func TestUpdateLabelFlags_ParseSemantics(t *testing.T) {
	// resetUpdateFieldFlags must run first: setTestFlagChanged captures the
	// prior Changed bit and restores it on cleanup, so the Changed = true that
	// ParseFlags sets below is reverted and later tests stay unaffected.
	resetUpdateFieldFlags(t)
	resetUpdateLabelFlagVars(t)

	if err := updateCmd.ParseFlags([]string{
		"--add-label", "x,y",
		"--add-label", "a",
		"--remove-label", " z ",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if want := []string{"x,y", "a"}; !reflect.DeepEqual(updateAddLabels, want) {
		t.Fatalf("updateAddLabels = %#v, want %#v (StringArray: no comma-split, every occurrence kept)", updateAddLabels, want)
	}
	if want := []string{" z "}; !reflect.DeepEqual(updateRemoveLabels, want) {
		t.Fatalf("updateRemoveLabels = %#v, want %#v (no trimming)", updateRemoveLabels, want)
	}
	if !updateCmd.Flags().Changed("add-label") || !updateCmd.Flags().Changed("remove-label") {
		t.Fatal("parsing --add-label/--remove-label must mark both flags Changed")
	}
}

func TestDataUpdate_FieldsAndLabelsShareOneUpdateCall(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateTitle = "retitled with labels"
		updateAddLabels = []string{"criticized"}
		updateRemoveLabels = []string{"needs-revision"}
		t.Cleanup(func() { updateTitle = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "title", true)
		setTestFlagChanged(t, updateCmd.Flags(), "add-label", true)
		setTestFlagChanged(t, updateCmd.Flags(), "remove-label", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-43"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want a single Update carrying both fields and labels", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.Title == nil || *params.Title != "retitled with labels" {
			t.Fatalf("Update title = %#v, want %q", params.Title, "retitled with labels")
		}
		if want := []string{"criticized"}; !reflect.DeepEqual(params.AddLabels, want) {
			t.Fatalf("Update AddLabels = %#v, want %#v", params.AddLabels, want)
		}
		if want := []string{"needs-revision"}; !reflect.DeepEqual(params.RemoveLabels, want) {
			t.Fatalf("Update RemoveLabels = %#v, want %#v", params.RemoveLabels, want)
		}
	})
}

// TestDataUpdate_LabelWithDependencyFlag_StillUpdates is the fieldsChanged-gate
// regression guard. RunE only calls Update when `fieldsChanged || !depsChanged`,
// so if a label flag failed to set changed=true in updateParamsFromFlags, then
// `update ID --add-label x --depends-on Y` would skip Update entirely and
// silently drop the label while still printing success.
func TestDataUpdate_LabelWithDependencyFlag_StillUpdates(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateAddLabels = []string{"criticized"}
		updateAddDeps = []string{"dep-9"}
		setTestFlagChanged(t, updateCmd.Flags(), "add-label", true)

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-44"})
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !strings.Contains(out, "updated loom-44") {
			t.Fatalf("update output = %q, want success message", out)
		}
		if len(stub.calls) != 2 {
			t.Fatalf("calls = %#v, want Update + AddDependency (label must not be dropped)", stub.calls)
		}
		if stub.calls[0].method != "Update" {
			t.Fatalf("calls[0].method = %q, want Update before AddDependency", stub.calls[0].method)
		}
		if stub.calls[1].method != "AddDependency" {
			t.Fatalf("calls[1].method = %q, want AddDependency", stub.calls[1].method)
		}
		if dep := stub.calls[1].args.(backend.DepAddParams); dep.FromID != "loom-44" || dep.ToID != "dep-9" {
			t.Errorf("AddDependency params = %#v, want loom-44 -> dep-9", dep)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if want := []string{"criticized"}; !reflect.DeepEqual(params.AddLabels, want) {
			t.Fatalf("Update AddLabels = %#v, want %#v", params.AddLabels, want)
		}
	})
}

func TestDataUpdate_NoLabelFlags_LeavesLabelDeltasNil(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateTitle = "labels untouched"
		// Slice vars deliberately left populated: without the Changed bit the
		// command must not forward them.
		updateAddLabels = []string{"stale-leak"}
		updateRemoveLabels = []string{"stale-leak"}
		t.Cleanup(func() { updateTitle = "" })
		setTestFlagChanged(t, updateCmd.Flags(), "title", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-45"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want one Update call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.AddLabels != nil {
			t.Errorf("Update AddLabels = %#v, want nil when --add-label was not passed", params.AddLabels)
		}
		if params.RemoveLabels != nil {
			t.Errorf("Update RemoveLabels = %#v, want nil when --remove-label was not passed", params.RemoveLabels)
		}
		if params.SetLabels != nil {
			t.Errorf("Update SetLabels = %#v, want nil", params.SetLabels)
		}
	})
}

// TestDataUpdate_UpdateErrorPropagates covers COMMAND ERROR PROPAGATION, NOT
// atomicity: a failing Update aborts RunE, so the dependency calls that would
// have followed never run and no success line is printed. The backend applies
// fields and dependency edges sequentially, so partial success in the other
// direction (Update succeeds, AddDependency fails) is accepted behavior and is
// deliberately not asserted here.
func TestDataUpdate_UpdateErrorPropagates(t *testing.T) {
	wantErr := errors.New("backend rejected update")
	stub := &localBackendStub{updateErr: wantErr}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateAddLabels = []string{"criticized"}
		updateAddDeps = []string{"dep-10"}
		setTestFlagChanged(t, updateCmd.Flags(), "add-label", true)

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-46"})
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("update err = %v, want %v", err, wantErr)
		}
		if strings.Contains(out, "updated loom-46") {
			t.Fatalf("update output = %q, want no success message on failure", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want only the failed Update (dependency calls must not run)", stub.calls)
		}
	})
}

// --force is the operator's un-park path: fleet-db protects reserved labels
// (currently "operator") and refuses to remove one without it. The CLI's job is
// only to carry the flag through to the backend, and to carry nothing when it
// is absent.
func TestDataUpdate_RemoveLabelForce(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateRemoveLabels = []string{"operator"}
		updateForce = true
		setTestFlagChanged(t, updateCmd.Flags(), "remove-label", true)
		setTestFlagChanged(t, updateCmd.Flags(), "force", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-43"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" {
			t.Fatalf("calls = %#v, want one Update call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if want := []string{"operator"}; !reflect.DeepEqual(params.RemoveLabels, want) {
			t.Fatalf("Update RemoveLabels = %#v, want %#v", params.RemoveLabels, want)
		}
		if !params.Force {
			t.Error("Update Force = false, want true when --force is given")
		}
	})
}

func TestDataUpdate_RemoveLabelWithoutForce(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		resetUpdateFieldFlags(t)
		resetUpdateLabelFlagVars(t)
		outputFormat = "text"
		updateRemoveLabels = []string{"operator"}
		setTestFlagChanged(t, updateCmd.Flags(), "remove-label", true)

		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-44"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.Force {
			t.Error("Update Force = true, want false when --force is absent")
		}
	})
}

// --force is a modifier on the label deltas, not a field: on its own it must
// not make the command look like a field update, so the backend's canonical
// "no fields" validation still surfaces.
func TestDataUpdate_ForceAloneIsNotAFieldChange(t *testing.T) {
	cmd := updateCmd
	resetUpdateFieldFlags(t)
	resetUpdateLabelFlagVars(t)
	updateForce = true
	setTestFlagChanged(t, cmd.Flags(), "force", true)

	params, changed, err := updateParamsFromFlags(cmd)
	if err != nil {
		t.Fatalf("updateParamsFromFlags: %v", err)
	}
	if changed {
		t.Error("changed = true for --force alone, want false")
	}
	if !params.Force {
		t.Error("params.Force = false, want true")
	}
}
