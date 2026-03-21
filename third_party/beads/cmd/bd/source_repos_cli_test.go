package main

import (
	"testing"
)

func TestReadySourceReposFlagExists(t *testing.T) {
	flag := readyCmd.Flags().Lookup("source-repos")
	if flag == nil {
		t.Fatal("ready command should have --source-repos flag")
	}

	if flag.Value.Type() != "stringSlice" {
		t.Errorf("Expected --source-repos to be stringSlice, got %q", flag.Value.Type())
	}

	if flag.DefValue != "[]" {
		t.Errorf("Expected default source-repos='[]', got %q", flag.DefValue)
	}

	if flag.Usage == "" {
		t.Error("Expected --source-repos to have usage text")
	}
}

func TestListSourceReposFlagExists(t *testing.T) {
	flag := listCmd.Flags().Lookup("source-repos")
	if flag == nil {
		t.Fatal("list command should have --source-repos flag")
	}

	if flag.Value.Type() != "stringSlice" {
		t.Errorf("Expected --source-repos to be stringSlice, got %q", flag.Value.Type())
	}

	if flag.DefValue != "[]" {
		t.Errorf("Expected default source-repos='[]', got %q", flag.DefValue)
	}

	if flag.Usage == "" {
		t.Error("Expected --source-repos to have usage text")
	}
}

func TestCreateSourceRepoFlagExists(t *testing.T) {
	// Note: create uses singular --source-repo (not --source-repos)
	flag := createCmd.Flags().Lookup("source-repo")
	if flag == nil {
		t.Fatal("create command should have --source-repo flag")
	}

	// create uses a single string, not a slice
	if flag.Value.Type() != "string" {
		t.Errorf("Expected --source-repo to be string, got %q", flag.Value.Type())
	}

	if flag.DefValue != "" {
		t.Errorf("Expected default source-repo='', got %q", flag.DefValue)
	}

	if flag.Usage == "" {
		t.Error("Expected --source-repo to have usage text")
	}
}

func TestSourceReposFlagNaming(t *testing.T) {
	// Verify that ready and list use plural --source-repos (StringSlice)
	// while create uses singular --source-repo (String).
	// This is intentional: ready/list filter by multiple repos,
	// while create assigns a single source repo.

	// ready should NOT have singular --source-repo
	if readyCmd.Flags().Lookup("source-repo") != nil {
		t.Error("ready command should use --source-repos (plural), not --source-repo")
	}

	// list should NOT have singular --source-repo
	if listCmd.Flags().Lookup("source-repo") != nil {
		t.Error("list command should use --source-repos (plural), not --source-repo")
	}

	// create should NOT have plural --source-repos
	if createCmd.Flags().Lookup("source-repos") != nil {
		t.Error("create command should use --source-repo (singular), not --source-repos")
	}
}
