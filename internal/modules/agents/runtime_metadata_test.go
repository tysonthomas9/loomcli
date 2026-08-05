package agents

import (
	"errors"
	"reflect"
	"testing"
)

func TestRuntimeMetadataRoundTripPreservesUnrelatedMetadata(t *testing.T) {
	want := RuntimeMetadata{
		RoleKind: "interactive", Backend: "codex",
		FallbackBackends: []string{"claude", "gemini"},
		Repos:            []string{"fleet-db", "loomcli"}, RepoGroups: []string{"core"},
		CrossRepo: true, Auto: false,
	}
	metadata, err := WithRuntimeMetadata(map[string]string{"owner": "ui"}, want)
	if err != nil {
		t.Fatal(err)
	}
	if metadata["owner"] != "ui" {
		t.Fatalf("unrelated metadata = %#v", metadata)
	}
	got, err := ParseRuntimeMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime metadata = %#v, want %#v", got, want)
	}
}

func TestRuntimeMetadataBackendOnlyIsManagedAgentMetadata(t *testing.T) {
	got, err := ParseRuntimeMetadata(map[string]string{MetadataBackend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != "codex" || got.RoleKind != "" || got.Repos != nil {
		t.Fatalf("runtime metadata = %#v", got)
	}
}

func TestRuntimeMetadataRejectsPartialOrNonCanonicalState(t *testing.T) {
	for name, metadata := range map[string]map[string]string{
		"partial":           {MetadataRoleKind: "interactive"},
		"invalid list":      {MetadataRoleKind: "interactive", MetadataFallbackBackends: "null", MetadataRepos: "[]", MetadataRepoGroups: "[]", MetadataCrossRepo: "false", MetadataAuto: "false"},
		"noncanonical list": {MetadataRoleKind: "interactive", MetadataFallbackBackends: "[]", MetadataRepos: `[" z"]`, MetadataRepoGroups: "[]", MetadataCrossRepo: "false", MetadataAuto: "false"},
		"noncanonical bool": {MetadataRoleKind: "interactive", MetadataFallbackBackends: "[]", MetadataRepos: "[]", MetadataRepoGroups: "[]", MetadataCrossRepo: "FALSE", MetadataAuto: "false"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRuntimeMetadata(metadata); !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("ParseRuntimeMetadata error = %v", err)
			}
		})
	}
}

func TestWithRuntimeMetadataRejectsNonCanonicalInput(t *testing.T) {
	if _, err := WithRuntimeMetadata(nil, RuntimeMetadata{Repos: []string{" loom "}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("WithRuntimeMetadata error = %v", err)
	}
}
