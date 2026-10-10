package loomagent

import (
	"context"
	"go/build"
	"strings"
	"sync"
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

type fakeWorkspace struct {
	ensured []WorkspaceSpec
	path    string // every Ensure's path; "" is /wt/<key>
	mu      sync.Mutex
	failing error                  // every Ensure fails with it, as with its base ref gone; under mu
	tree    string                 // the working copy's content a capture saves; under mu
	refs    map[string]string      // each checkpoint ref: the tree it saved; under mu
	capture func(ref string) error // runs before each new capture, outside mu; an error fails it
}

// setTree sets the working copy's content the next capture saves.
func (f *fakeWorkspace) setTree(tree string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tree = tree
}

// checkpoint is ref's saved tree, and whether ref exists.
func (f *fakeWorkspace) checkpoint(ref string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tree, ok := f.refs[ref]
	return tree, ok
}

func (f *fakeWorkspace) Checkpoint(_ context.Context, _ WorkspaceSpec, ref string) error {
	if _, ok := f.checkpoint(ref); ok {
		return nil
	}
	if f.capture != nil {
		if err := f.capture(ref); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refs == nil {
		f.refs = map[string]string{}
	}
	if _, ok := f.refs[ref]; !ok {
		f.refs[ref] = f.tree
	}
	return nil
}

func (f *fakeWorkspace) CheckpointDiff(context.Context, string, string, string) (CheckpointDiff, error) {
	return CheckpointDiff{}, nil
}

func (f *fakeWorkspace) DropCheckpoints(context.Context, string, string) error { return nil }

// setEnsureErr makes every Ensure fail with err; nil restores it.
func (f *fakeWorkspace) setEnsureErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = err
}

func (f *fakeWorkspace) Ensure(_ context.Context, s WorkspaceSpec) (WorkingCopy, error) {
	f.mu.Lock()
	err := f.failing
	f.mu.Unlock()
	if err != nil {
		return WorkingCopy{}, err
	}
	f.ensured = append(f.ensured, s)
	path := f.path
	if path == "" {
		path = "/wt/" + s.Key
	}
	return WorkingCopy{Path: path, Branch: s.Branch, HEAD: "abc"}, nil
}

func (f *fakeWorkspace) Status(_ context.Context, s WorkspaceSpec) (WorkspaceStatus, error) {
	return WorkspaceStatus{Branch: s.Branch, HEAD: "abc"}, nil
}

func (f *fakeWorkspace) CheckBase(context.Context, string, string) error { return nil }

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
