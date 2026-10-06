package opencode

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestContractPresets proves Loom's presets work as loom-<name>.md agent
// files in <worktrees>/.opencode/agent on a service Loom did not start: the
// persona reaches the model, a preset added later works without a restart,
// a removed one is refused, and a session outside the worktrees root or on a
// service that disables project config is refused clearly. The user's own
// agent file beside Loom's is untouched. Pinned b30c4d0, /tmp sandbox users,
// fake model.
func TestContractPresets(t *testing.T) {
	bin := realOpenCode(t)
	model := newFakeModel(t)
	sbx := newSandbox(t, "loom-opencode-presets-", fakeModelConfig(model.URL))
	root := filepath.Join(sbx, "worktrees")
	repo := filepath.Join(root, "repo", "key") // agentworktree's <root>/<repo>/<key>
	agents := filepath.Join(root, ".opencode", "agent")
	for _, d := range []string{repo, agents} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mine := filepath.Join(agents, "mine.md")
	if err := os.WriteFile(mine, []byte("---\nmode: primary\n---\nthe user's own agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	user := startService(t, bin, sbx, contractEnv(sbx)) // the sandbox user's service: no Loom presets in its environment
	tester := loomharness.PresetConfig{Name: "tester", Persona: "LOOM-PERSONA-TESTER"}
	added := loomharness.PresetConfig{Name: "added", Persona: "LOOM-PERSONA-ADDED"}
	a := New(Config{Bin: bin, Env: contractEnv(sbx), Worktrees: root, Presets: []loomharness.PresetConfig{tester}})
	var owned []loomharness.NativeRef
	t.Cleanup(func() {
		if err := a.Purge(context.Background(), owned); err != nil {
			t.Errorf("cleanup purge: %v", err)
		}
		a.Stop()
	})
	waitFor(t, "fake/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "fake/m") })
	if serverPID(a) != user.PID {
		t.Fatalf("Loom uses service %d; want the user's %d", serverPID(a), user.PID)
	}
	open := func(key string, p loomharness.PresetConfig, dir string) (loomharness.NativeRef, error) {
		ref, err := a.Open(ctx, loomharness.OpenSpec{Key: key, Launch: loomharness.Launch{Root: sbx}, Preset: p, Dir: dir, Model: "fake/m"})
		if err == nil {
			owned = append(owned, ref)
		}
		return ref, err
	}
	persona := func(t *testing.T, ref loomharness.NativeRef, key, marker string) {
		t.Helper()
		text := "persona check " + key
		if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID(key, "r1"), Text: text}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the preset turn's model request", func() bool { return model.requests(text) > 0 })
		if !model.sawSystem(marker) {
			t.Fatalf("the %s persona never reached the model", key)
		}
	}

	var testerRef, addedRef loomharness.NativeRef
	t.Run("OnServiceLoomDidNotStart", func(t *testing.T) {
		var err error
		if testerRef, err = open("preset-tester", tester, repo); err != nil {
			t.Fatal(err)
		}
		persona(t, testerRef, "preset-tester", tester.Persona)
	})

	t.Run("SessionPersonaIsolation", func(t *testing.T) {
		cases := []struct {
			key     string
			persona string
		}{
			{"persona-a", "PERSONA_A literal {{.AgentName}}"},
			{"persona-b", "PERSONA_B"},
			{"persona-default", tester.Persona},
		}
		for _, tc := range cases {
			ref, err := open(tc.key, loomharness.PresetConfig{Name: tester.Name, Persona: tc.persona}, repo)
			if err != nil {
				t.Fatal(err)
			}
			text := "persona isolation " + tc.key
			if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID(tc.key, "r1"), Text: text}); err != nil {
				t.Fatal(err)
			}
			if err := model.awaitRequest(ctx, text); err != nil {
				t.Fatal(err)
			}
			system := model.systemFor(text)
			if !strings.Contains(system, tc.persona) {
				t.Fatalf("%s system omitted its persona: %q", tc.key, system)
			}
			for _, other := range cases {
				if other.key != tc.key && other.persona != tester.Persona && strings.Contains(system, other.persona) {
					t.Fatalf("%s system contained %s persona: %q", tc.key, other.key, system)
				}
			}
		}
	})

	t.Run("HotAdd", func(t *testing.T) {
		if err := a.SetPresets([]loomharness.PresetConfig{tester, added}); err != nil {
			t.Fatal(err)
		}
		var err error
		if addedRef, err = open("preset-added", added, repo); err != nil {
			t.Fatal(err)
		}
		persona(t, addedRef, "preset-added", added.Persona)
	})

	t.Run("Removal", func(t *testing.T) {
		if err := a.SetPresets([]loomharness.PresetConfig{tester}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(agents, "loom-added.md")); !os.IsNotExist(err) {
			t.Fatalf("loom-added.md still exists: %v", err)
		}
		if b, err := os.ReadFile(mine); err != nil || !strings.Contains(string(b), "the user's own agent") {
			t.Fatal("Loom touched the user's agent file")
		}
		if _, err := open("preset-added-2", added, repo); !isCode(err, "bad_request") {
			t.Fatalf("Open with a removed preset = %v; want bad_request", err)
		}
		if _, err := a.Session(addedRef).Resume(ctx, loomharness.Launch{Root: sbx}, nil); !isCode(err, "bad_request") {
			t.Fatalf("Resume of a removed preset's session = %v; want bad_request", err)
		}
		if _, err := a.Session(testerRef).Resume(ctx, loomharness.Launch{Root: sbx}, nil); err != nil {
			t.Fatalf("Resume of a kept preset's session: %v", err)
		}
		// The running service drops the removed agent without a restart.
		waitFor(t, "loom-added to leave the service's agent list", func() bool {
			var r struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := a.call(ctx, "GET", "/api/agent?location[directory]="+url.QueryEscape(repo), nil, &r); err != nil {
				return false
			}
			ids := map[string]bool{}
			for _, d := range r.Data {
				ids[d.ID] = true
			}
			return ids["loom-tester"] && !ids["loom-added"]
		})
	})

	t.Run("OutsideWorktreesRoot", func(t *testing.T) {
		if _, err := open("preset-outside", tester, filepath.Join(sbx, "repo")); !isCode(err, "bad_request") || !strings.Contains(err.Error(), "worktrees root") {
			t.Fatalf("Open outside the worktrees root = %v; want bad_request", err)
		}
	})

	t.Run("ProjectConfigDisabled", func(t *testing.T) {
		other := newSandbox(t, "loom-opencode-presets-off-", fakeModelConfig(model.URL))
		root := filepath.Join(other, "worktrees")
		repo := filepath.Join(root, "repo", "key")
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		startService(t, bin, other, contractEnv(other, "OPENCODE_DISABLE_PROJECT_CONFIG=1"))
		b := New(Config{Bin: bin, Env: contractEnv(other), Worktrees: root, Presets: []loomharness.PresetConfig{tester}})
		t.Cleanup(b.Stop)
		waitFor(t, "fake/m in Models", func() bool { models, err := b.Models(ctx); return err == nil && hasModel(models, "fake/m") })
		_, err := b.Open(ctx, loomharness.OpenSpec{Key: "preset-off", Launch: loomharness.Launch{Root: other}, Preset: tester, Dir: repo, Model: "fake/m"})
		if !isCode(err, "bad_request") || !strings.Contains(err.Error(), "project config") {
			t.Fatalf("Open on a service without project config = %v; want bad_request naming project config", err)
		}
	})
}
