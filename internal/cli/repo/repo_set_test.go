package repo

import (
	"strings"
	"testing"
)

// The vocabulary is closed and validated client-side, so a typo names the
// accepted fields instead of round-tripping into a server 400.
func TestBuildRepoPatch_Fields(t *testing.T) {
	t.Run("remote_url", func(t *testing.T) {
		patch, err := buildRepoPatch("remote_url", "git@github.com:acme/app.git")
		if err != nil || patch.RemoteURL == nil || *patch.RemoteURL != "git@github.com:acme/app.git" {
			t.Fatalf("patch = %+v err = %v", patch, err)
		}
	})

	t.Run("remote and default_branch clear to their defaults", func(t *testing.T) {
		patch, err := buildRepoPatch("remote", "")
		if err != nil || patch.Remote == nil || *patch.Remote != "" {
			t.Fatalf("remote: patch = %+v err = %v", patch, err)
		}
		patch, err = buildRepoPatch("default_branch", "")
		if err != nil || patch.DefaultBranch == nil || *patch.DefaultBranch != "" {
			t.Fatalf("default_branch: patch = %+v err = %v", patch, err)
		}
	})

	t.Run("source_repo_id", func(t *testing.T) {
		patch, err := buildRepoPatch("source_repo_id", "app-core")
		if err != nil || patch.SourceRepoID == nil || *patch.SourceRepoID != "app-core" {
			t.Fatalf("patch = %+v err = %v", patch, err)
		}
	})
}

// A blank remote_url would persist a repo that looks registered and fails at
// first clone/push, so it is refused rather than stored.
func TestBuildRepoPatch_RemoteURLCannotBeBlanked(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if _, err := buildRepoPatch("remote_url", v); err == nil {
			t.Fatalf("blank remote_url %q must be refused", v)
		}
	}
}

func TestBuildRepoPatch_UnknownFieldNamesTheVocabulary(t *testing.T) {
	_, err := buildRepoPatch("branch", "v5")
	if err == nil {
		t.Fatal("unknown field must error")
	}
	for _, want := range []string{"default_branch", "remote_url", "groups", "source_repo_id", "remote"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should list %q", err.Error(), want)
		}
	}
}

// Groups are a list on the wire: blanks are dropped so "a,,b" and a trailing
// comma cannot persist empty group names, and an empty value clears.
func TestBuildRepoPatch_Groups(t *testing.T) {
	patch, err := buildRepoPatch("groups", " backend , , infra, ")
	if err != nil || patch.Groups == nil {
		t.Fatalf("patch = %+v err = %v", patch, err)
	}
	got := *patch.Groups
	if len(got) != 2 || got[0] != "backend" || got[1] != "infra" {
		t.Fatalf("groups = %v, want [backend infra]", got)
	}

	patch, err = buildRepoPatch("groups", "")
	if err != nil || patch.Groups == nil {
		t.Fatalf("clearing groups must send an empty list, got %+v err = %v", patch, err)
	}
	if len(*patch.Groups) != 0 {
		t.Fatalf("groups = %v, want empty", *patch.Groups)
	}
}

// Every documented field must be buildable, and every buildable field must be
// documented — the help text and the switch are one vocabulary.
func TestRepoSetFields_MatchTheSwitch(t *testing.T) {
	for field := range repoSetFields {
		value := "x"
		if _, err := buildRepoPatch(field, value); err != nil {
			t.Errorf("documented field %q is not accepted: %v", field, err)
		}
	}
	for _, field := range repoSetFieldNames() {
		if !strings.Contains(repoSetCmd.Long, field) {
			t.Errorf("field %q is accepted but missing from the help text", field)
		}
	}
}
