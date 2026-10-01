package loomagent

import (
	"context"
	"go/build"
	"strings"
	"testing"
)

const module = "github.com/tysonthomas9/loomcli/"

func TestWorkspaceImportBoundary(t *testing.T) {
	seen := map[string]bool{}
	var walk func(path, dir string)
	walk = func(path, dir string) {
		if seen[path] {
			return
		}
		seen[path] = true
		for _, banned := range []string{"internal/agentworktree", "internal/loomgit", "internal/gitrunner"} {
			if path == module+banned || strings.HasPrefix(path, module+banned+"/") {
				t.Errorf("loomagent depends on %s", path)
			}
		}
		pkg, err := build.Import(path, dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range pkg.Imports {
			if strings.HasPrefix(imp, module) {
				walk(imp, pkg.Dir)
			}
		}
	}
	walk(module+"internal/loomagent", ".")
}

type fakeWorkspace struct{ ensured []WorkspaceSpec }

func (f *fakeWorkspace) Ensure(_ context.Context, s WorkspaceSpec) (WorkingCopy, error) {
	f.ensured = append(f.ensured, s)
	return WorkingCopy{Path: "/wt/" + s.Key, Branch: s.Branch, HEAD: "abc"}, nil
}

func (f *fakeWorkspace) Status(_ context.Context, s WorkspaceSpec) (WorkspaceStatus, error) {
	return WorkspaceStatus{Branch: s.Branch, HEAD: "abc"}, nil
}

func (f *fakeWorkspace) Remove(context.Context, WorkspaceSpec) error { return nil }

func (f *fakeWorkspace) Publish(context.Context, PublishRequest) (PublishResult, error) {
	return PublishResult{Number: 1}, nil
}

func TestWorkspaceImportBoundaryAcceptsFake(t *testing.T) {
	f := &fakeWorkspace{}
	var w Workspace = f
	s := WorkspaceSpec{Key: "agt_1", Repo: "/repo", BaseRef: "main", Branch: "loom/agent/agt_1"}
	wc, err := w.Ensure(context.Background(), s)
	if err != nil || wc.Branch != s.Branch || len(f.ensured) != 1 {
		t.Fatalf("Ensure through fake = %+v, %v", wc, err)
	}
	st, err := w.Status(context.Background(), s)
	if err != nil || st.HEAD != wc.HEAD {
		t.Fatalf("Status through fake = %+v, %v", st, err)
	}
}
