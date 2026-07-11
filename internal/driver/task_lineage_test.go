package driver

import (
	"encoding/json"
	"testing"
)

func TestWithLineageRoundTrip(t *testing.T) {
	in := json.RawMessage(`{"sourceRepo":"acme/widgets","rubric":"be strict"}`)
	lin := TaskLineage{StackID: "epic:E", BaseRef: "loom/stack/epic:E/A", OutputBranch: "loom/stack/epic:E/B"}

	merged, err := WithLineage(in, lin)
	if err != nil {
		t.Fatalf("WithLineage: %v", err)
	}

	// Sibling keys survive the merge.
	var obj map[string]any
	if err := json.Unmarshal(merged, &obj); err != nil {
		t.Fatalf("merged not an object: %v", err)
	}
	if obj["sourceRepo"] != "acme/widgets" || obj["rubric"] != "be strict" {
		t.Fatalf("sibling keys lost: %v", obj)
	}

	got, ok := LineageFromInput(merged)
	if !ok {
		t.Fatalf("LineageFromInput: ok=false, want true")
	}
	if got != lin {
		t.Fatalf("round-trip = %+v, want %+v", got, lin)
	}
}

func TestWithLineageEmptyIsNoop(t *testing.T) {
	in := json.RawMessage(`{"k":"v"}`)
	merged, err := WithLineage(in, TaskLineage{})
	if err != nil {
		t.Fatalf("WithLineage: %v", err)
	}
	if string(merged) != string(in) {
		t.Fatalf("empty lineage altered input: %q", merged)
	}
	if _, ok := LineageFromInput(merged); ok {
		t.Fatalf("LineageFromInput: ok=true for input without lineage")
	}
}

func TestWithLineageNilInput(t *testing.T) {
	lin := TaskLineage{StackID: "epic:E", BaseRef: "main"}
	merged, err := WithLineage(nil, lin)
	if err != nil {
		t.Fatalf("WithLineage: %v", err)
	}
	got, ok := LineageFromInput(merged)
	if !ok || got != lin {
		t.Fatalf("nil-input round-trip = %+v ok=%v, want %+v", got, ok, lin)
	}
}

func TestLineageFromInputNonObject(t *testing.T) {
	// A non-object input (e.g. a bare JSON array/string) must not panic and
	// must report no lineage rather than corrupting anything.
	for _, raw := range []string{``, `[]`, `"a string"`, `not json`} {
		if _, ok := LineageFromInput(json.RawMessage(raw)); ok {
			t.Fatalf("LineageFromInput(%q): ok=true, want false", raw)
		}
		// WithLineage on a non-object input leaves it unchanged.
		out, err := WithLineage(json.RawMessage(raw), TaskLineage{StackID: "epic:E"})
		if err != nil {
			t.Fatalf("WithLineage(%q): %v", raw, err)
		}
		if raw != `` && string(out) != raw {
			t.Fatalf("WithLineage(%q) = %q, want unchanged", raw, out)
		}
	}
}
