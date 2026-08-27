package github

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBranchProtectionFailsClosed(t *testing.T) {
	called := false
	err := BranchProtection(context.Background(), func(_ context.Context, name string, args ...string) (string, string, error) {
		called = true
		if name != "gh" || !strings.Contains(strings.Join(args, " "), "branches/main/protection") {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		return "", "404 Not Found", errors.New("exit status 1")
	}, "acme/project", "main")
	if !called || err == nil || !strings.Contains(err.Error(), "merge disabled") {
		t.Fatalf("expected fail-closed protection error, called=%v err=%v", called, err)
	}
}

func TestPRProtectionRequiresVerifiedRepositoryAndBase(t *testing.T) {
	commands := 0
	err := PRProtection(context.Background(), func(_ context.Context, name string, args ...string) (string, string, error) {
		commands++
		if commands == 1 {
			return `{"baseRefName":"main","repository":{"nameWithOwner":"acme/project"}}`, "", nil
		}
		return "{}", "", nil
	}, "17")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commands != 2 {
		t.Fatalf("commands=%d, want PR view and protection", commands)
	}
}

func TestRepositorySlug(t *testing.T) {
	for _, remote := range []string{"git@github.com:acme/project.git", "https://github.com/acme/project.git"} {
		if got, ok := RepositorySlug(remote); !ok || got != "acme/project" {
			t.Fatalf("RepositorySlug(%q)=%q,%v", remote, got, ok)
		}
	}
}
