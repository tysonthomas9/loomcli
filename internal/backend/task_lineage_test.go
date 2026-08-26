package backend

import (
	"context"
	"reflect"
	"strings"
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

func TestValidateTaskLineageReferences(t *testing.T) {
	t.Parallel()
	inputs := map[string]*IssueDetailData{
		"A": {IssueData: IssueData{ID: "A", SourceRepo: "repo"}},
		"B": {IssueData: IssueData{ID: "B", SourceRepo: "repo"}},
	}
	get := func(_ context.Context, id string) (*IssueDetailData, error) { return inputs[id], nil }
	deps := []DependencyData{
		{IssueID: "I", DependsOnID: "A", Type: "blocks"},
		{IssueID: "I", DependsOnID: "B", Type: "blocks"},
	}
	if err := ValidateTaskLineageReferences(context.Background(), TaskLineageSpec{
		InheritsFrom: "A", IntegrationInputs: []string{"B"},
	}, "I", "repo", deps, get); err != nil {
		t.Fatalf("valid references: %v", err)
	}

	badDeps := deps[:1]
	err := ValidateTaskLineageReferences(context.Background(), TaskLineageSpec{
		InheritsFrom: "A", IntegrationInputs: []string{"B"},
	}, "I", "repo", badDeps, get)
	if err == nil || !strings.Contains(err.Error(), "not a direct blocks dependency") {
		t.Fatalf("missing blocker error = %v", err)
	}

	inputs["B"].SourceRepo = "other"
	err = ValidateTaskLineageReferences(context.Background(), TaskLineageSpec{
		InheritsFrom: "A", IntegrationInputs: []string{"B"},
	}, "I", "repo", deps, get)
	if err == nil || !strings.Contains(err.Error(), "belongs to source repo") {
		t.Fatalf("cross-repo error = %v", err)
	}
}

func TestTaskLineageSpecRejectsAmbiguousIntegration(t *testing.T) {
	for _, spec := range []TaskLineageSpec{{IntegrationInputs: []string{"B"}}, {InheritsFrom: "A", IntegrationInputs: []string{"A"}}, {InheritsFrom: "A", IntegrationInputs: []string{"B", "B"}}} {
		if err := spec.Validate("T"); err == nil {
			t.Fatalf("Validate(%#v) accepted invalid spec", spec)
		}
	}
}
