package backend

import (
	"context"
	"reflect"
	"testing"
)

func TestTaskLineageSpecRoundTripAndSchedulingUnion(t *testing.T) {
	spec := TaskLineageSpec{InheritsFrom: "A", IntegrationInputs: []string{"B", "C"}}
	metadata, err := spec.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseTaskLineage(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("ParseTaskLineage(Metadata()) = %#v, want %#v", got, spec)
	}
	want := []string{"E", "A", "B", "C"}
	if deps := spec.SchedulingDependencies([]string{"E", "A"}); !reflect.DeepEqual(deps, want) {
		t.Fatalf("SchedulingDependencies = %v, want %v", deps, want)
	}
}

func TestValidateTaskLineageInputs(t *testing.T) {
	t.Parallel()
	inputs := map[string]*IssueDetailData{
		"A": {IssueData: IssueData{ID: "A", SourceRepo: "repo"}},
		"B": {IssueData: IssueData{ID: "B", SourceRepo: "repo"}},
	}
	get := func(_ context.Context, id string) (*IssueDetailData, error) { return inputs[id], nil }
	if err := ValidateTaskLineageInputs(context.Background(), TaskLineageSpec{
		InheritsFrom: "A", IntegrationInputs: []string{"B"},
	}, "repo", get); err != nil {
		t.Fatalf("valid references: %v", err)
	}
}

func TestTaskLineageSpecRejectsAmbiguousIntegration(t *testing.T) {
	for _, spec := range []TaskLineageSpec{{IntegrationInputs: []string{"B"}}, {InheritsFrom: "A", IntegrationInputs: []string{"A"}}, {InheritsFrom: "A", IntegrationInputs: []string{"B", "B"}}} {
		if err := spec.Validate("T"); err == nil {
			t.Fatalf("Validate(%#v) accepted invalid spec", spec)
		}
	}
}
