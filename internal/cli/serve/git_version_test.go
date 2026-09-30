package serve

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func TestServeRejectsOldGitAtStartup(t *testing.T) {
	if os.Getenv("LOOM_TEST_SERVE_GIT_CHILD") == "1" {
		runServe(nil, nil)
		return
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho 'git version 2.39.5'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if err := loomgit.CheckGitVersion(); err == nil || !strings.Contains(err.Error(), "git_version_unsupported") {
		t.Fatalf("expected stable unsupported Git error, got %v", err)
	}
	t.Setenv("LOOM_TEST_SERVE_GIT_CHILD", "1")
	output, err := exec.Command(os.Args[0], "-test.run=^TestServeRejectsOldGitAtStartup$").CombinedOutput()
	if err == nil || !strings.Contains(string(output), "git_version_unsupported") {
		t.Fatalf("serve did not fail at startup with stable code: %v: %s", err, output)
	}
}
