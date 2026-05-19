package svcimpl

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestDiffServiceCommitsFilesAndPatch(t *testing.T) {
	ctx := context.Background()
	fake := &fakeGitOps{
		wt:        &ops.AgentWorktree{Name: "agent", Path: "/repo", Branch: "feature", DefaultBranch: "main"},
		mergeBase: "base-sha",
		commits:   []ops.DiffCommitResult{{Hash: "abc", Subject: "change"}},
		files:     []ops.DiffFileResult{{Path: "file.go", Status: "M", Additions: 3}},
		patch:     &ops.DiffFilePatchResult{Patch: "diff --git", Additions: 3},
	}
	svc := NewDiffService(fake, nil)

	commits, err := svc.DiffCommits(ctx, "WS", "agent", "", 10)
	if err != nil || len(commits) != 1 || commits[0].Hash != "abc" {
		t.Fatalf("DiffCommits = %#v err=%v", commits, err)
	}
	files, err := svc.DiffFiles(ctx, "WS", "agent", "", "HEAD")
	if err != nil || len(files) != 1 || files[0].Path != "file.go" {
		t.Fatalf("DiffFiles = %#v err=%v", files, err)
	}
	patch, err := svc.DiffFilePatch(ctx, "WS", "agent", "", "HEAD", "file.go")
	if err != nil || patch == nil || patch.Patch == "" {
		t.Fatalf("DiffFilePatch = %#v err=%v", patch, err)
	}

	fake.commits = nil
	commits, err = svc.DiffCommits(ctx, "WS", "agent", "feature", 0)
	if err != nil || commits == nil || len(commits) != 0 {
		t.Fatalf("DiffCommits nil normalization = %#v err=%v", commits, err)
	}
	fake.files = nil
	files, err = svc.DiffFiles(ctx, "WS", "agent", "main", "feature")
	if err != nil || files == nil || len(files) != 0 {
		t.Fatalf("DiffFiles nil normalization = %#v err=%v", files, err)
	}
}

func TestDiffServiceValidationAndErrors(t *testing.T) {
	ctx := context.Background()
	baseWT := &ops.AgentWorktree{Name: "agent", Path: "/repo", Branch: "feature", DefaultBranch: "main"}

	if _, err := NewDiffService(&fakeGitOps{resolveErr: errors.New("missing")}, nil).DiffCommits(ctx, "WS", "agent", "", 1); err == nil {
		t.Fatal("missing agent DiffCommits error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{wt: baseWT}, nil).DiffCommits(ctx, "WS", "../bad", "", 1); err == nil {
		t.Fatal("invalid agent DiffCommits error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{wt: baseWT}, nil).DiffCommits(ctx, "WS", "agent", "bad..ref", 1); err == nil {
		t.Fatal("invalid from ref DiffCommits error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{wt: baseWT, mergeErr: ops.ErrDiffBaseNotFound}, nil).DiffCommits(ctx, "WS", "agent", "", 1); err == nil {
		t.Fatal("diff base not found error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{wt: baseWT, mergeErr: errors.New("git failed")}, nil).DiffCommits(ctx, "WS", "agent", "", 1); err == nil {
		t.Fatal("merge-base internal error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{wt: baseWT, mergeBase: "base", commitErr: errors.New("log failed")}, nil).DiffCommits(ctx, "WS", "agent", "", 1); err == nil {
		t.Fatal("DiffCommits git error = nil")
	}

	svc := NewDiffService(&fakeGitOps{wt: baseWT, mergeBase: "base"}, nil)
	if _, err := svc.DiffFiles(ctx, "WS", "agent", "", ""); err == nil {
		t.Fatal("missing to DiffFiles error = nil")
	}
	if _, err := svc.DiffFiles(ctx, "WS", "agent", "", "bad..to"); err == nil {
		t.Fatal("invalid to DiffFiles error = nil")
	}
	if _, err := svc.DiffFiles(ctx, "WS", "agent", "bad..from", "HEAD"); err == nil {
		t.Fatal("invalid from DiffFiles error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{wt: baseWT, mergeBase: "base", filesErr: errors.New("diff failed")}, nil).DiffFiles(ctx, "WS", "agent", "", "HEAD"); err == nil {
		t.Fatal("DiffFiles git error = nil")
	}

	if _, err := svc.DiffFilePatch(ctx, "WS", "agent", "", "HEAD", ""); err == nil {
		t.Fatal("missing patch path error = nil")
	}
	if _, err := svc.DiffFilePatch(ctx, "WS", "agent", "", "HEAD", "../secret"); err == nil {
		t.Fatal("invalid patch path error = nil")
	}
	if _, err := svc.DiffFilePatch(ctx, "WS", "agent", "", "", "file.go"); err == nil {
		t.Fatal("missing patch to ref error = nil")
	}
	if _, err := svc.DiffFilePatch(ctx, "WS", "agent", "", "bad..to", "file.go"); err == nil {
		t.Fatal("invalid patch to ref error = nil")
	}
	if _, err := svc.DiffFilePatch(ctx, "WS", "agent", "bad..from", "HEAD", "file.go"); err == nil {
		t.Fatal("invalid patch from ref error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{wt: baseWT, mergeBase: "base", patchErr: errors.New("show failed")}, nil).DiffFilePatch(ctx, "WS", "agent", "", "HEAD", "file.go"); err == nil {
		t.Fatal("DiffFilePatch git error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{}, nil).GetIssueDiffStat(ctx, "WS", ""); err == nil {
		t.Fatal("empty issue diff stat error = nil")
	}
	if _, err := NewDiffService(&fakeGitOps{}, nil).GetIssueDiffStat(ctx, "WS", "ISSUE-1"); err == nil {
		t.Fatal("nil pool issue diff stat error = nil")
	}
}
