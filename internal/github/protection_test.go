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
		switch commands {
		case 1:
			return `{"baseRefName":"main"}`, "", nil
		case 2:
			return `{"nameWithOwner":"acme/project"}`, "", nil
		}
		return "{}", "", nil
	}, "17")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commands != 3 {
		t.Fatalf("commands=%d, want PR view, repo view, and protection", commands)
	}
}

func TestPRProtectionUsesRepositoryFromPullRequestURL(t *testing.T) {
	var commands []string
	err := PRProtection(context.Background(), func(_ context.Context, name string, args ...string) (string, string, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if args[0] == "pr" {
			return `{"baseRefName":"v5"}`, "", nil
		}
		return `{}`, "", nil
	}, "https://github.com/acme/project/pull/42")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || commands[1] != "gh api repos/acme/project/branches/v5/protection" {
		t.Fatalf("commands = %v, want URL-derived repository and no repo view", commands)
	}
}

func TestRepositorySlug(t *testing.T) {
	for _, remote := range []string{"git@github.com:acme/project.git", "https://github.com/acme/project.git"} {
		if got, ok := RepositorySlug(remote); !ok || got != "acme/project" {
			t.Fatalf("RepositorySlug(%q)=%q,%v", remote, got, ok)
		}
	}
}
