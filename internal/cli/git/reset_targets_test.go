package git

import (
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestResetTargetsHonorPerRepoAndExplicitBranches(t *testing.T) {
	worktrees := []cli.WorktreeInfo{
		{Name: "custom", Repo: &config.RepoConfig{DefaultBranch: "develop"}},
		{Name: "other", Repo: &config.RepoConfig{DefaultBranch: "staging"}},
		{Name: "default", Repo: &config.RepoConfig{}},
		{Name: "unconfigured"},
	}
	for _, tc := range []struct {
		name, target string
		explicit     bool
		want         []string
		perRepo      bool
	}{
		{"per repo", "main", false, []string{"develop", "staging", "main", "main"}, true},
		{"explicit override", "release", true, []string{"release", "release", "release", "release"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets, perRepo := buildResetTargets(worktrees, tc.target, tc.explicit)
			if perRepo != tc.perRepo || len(targets) != len(tc.want) {
				t.Fatalf("targets=%v perRepo=%v", targets, perRepo)
			}
			for i, target := range targets {
				if target.branch != tc.want[i] {
					t.Errorf("%s branch=%q want=%q", target.wt.Name, target.branch, tc.want[i])
				}
			}
		})
	}
}

func TestResetSummaryReportsPartialAndAllFailures(t *testing.T) {
	for _, failed := range [][]string{{"first"}, {"first", "second"}} {
		err := printResetSummary(failed, "main", false)
		if err == nil || !strings.Contains(err.Error(), failed[len(failed)-1]) || !strings.Contains(err.Error(), "failed to reset") {
			t.Fatalf("failed=%v error=%v", failed, err)
		}
	}
	if err := printResetSummary(nil, "main", false); err != nil {
		t.Fatalf("success summary: %v", err)
	}
}

func TestConfirmActionAcceptsYesAndDefaultsToNo(t *testing.T) {
	previous := os.Stdin
	defer func() { os.Stdin = previous }()
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"yes\n", true}, {" Y \n", true}, {"no\n", false}, {"\n", false}, {"maybe\n", false},
	} {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := write.WriteString(tc.input); err != nil {
			t.Fatal(err)
		}
		_ = write.Close()
		os.Stdin = read
		if got := ConfirmAction("Reset?"); got != tc.want {
			t.Errorf("input=%q got=%v want=%v", tc.input, got, tc.want)
		}
		_ = read.Close()
	}
}
