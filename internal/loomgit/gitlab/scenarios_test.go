//go:build gitlab_real

package gitlab

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"gopkg.in/yaml.v3"
)

type scenario struct {
	Name    string   `yaml:"name"`
	Task    string   `yaml:"task"`
	Git     []string `yaml:"git"`
	Fixture string   `yaml:"fixture"`
	Skip    string   `yaml:"skip"`
	Given   []step   `yaml:"given"`
	When    []step   `yaml:"when"`
	Then    []step   `yaml:"then"`
}

type step struct {
	Key   string
	Value yaml.Node
	Text  string
}

func (s *step) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return fmt.Errorf("step must contain exactly one named action")
	}
	s.Key = node.Content[0].Value
	s.Value = *node.Content[1]
	var out bytes.Buffer
	if err := yaml.NewEncoder(&out).Encode(node); err != nil {
		return err
	}
	s.Text = strings.TrimSpace(out.String())
	return nil
}

func (s step) decode(out any) error {
	data, err := yaml.Marshal(&s.Value)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return nil
}

type state struct {
	t        *testing.T
	dir      string
	home     string
	remote   string
	result   capture.Result
	previous capture.Result
	elapsed  time.Duration
	lastErr  error
}

func TestScenarios(t *testing.T) {
	version := os.Getenv("GITLAB_GIT_VERSION")
	if version != "min" && version != "latest" {
		t.Fatal("GITLAB_GIT_VERSION must be min or latest")
	}
	files, err := filepath.Glob("../../../test/gitlab/scenarios/*/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("scenario files: %v, %d found", err, len(files))
	}
	var selected int
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var sc scenario
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&sc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if sc.Name == "" || sc.Task == "" || sc.Fixture == "" || len(sc.Git) == 0 || len(sc.When) == 0 || len(sc.Then) == 0 {
			t.Fatalf("%s: name, task, git, fixture, when and then are required", file)
		}
		if os.Getenv("TASK") != "" && os.Getenv("TASK") != sc.Task {
			continue
		}
		glob := os.Getenv("SCENARIO")
		if glob == "" {
			glob = "*"
		}
		baseMatch, _ := filepath.Match(glob, filepath.Base(file))
		pathMatch, _ := filepath.Match(glob, file)
		if !baseMatch && !pathMatch {
			continue
		}
		if !slices.Contains(sc.Git, version) {
			continue
		}
		selected++
		t.Run(sc.Task+"/"+strings.TrimSuffix(filepath.Base(file), ".yaml"), func(t *testing.T) {
			if sc.Skip != "" {
				if !strings.Contains(sc.Skip, sc.Task) {
					t.Fatalf("%s: skip reason must name owning task %s", file, sc.Task)
				}
				t.Skip(sc.Skip)
			}
			st := &state{t: t}
			st.makeFixture(sc.Fixture)
			for _, group := range []struct {
				phase string
				steps []step
			}{{"given", sc.Given}, {"when", sc.When}, {"then", sc.Then}} {
				for i, item := range group.steps {
					if err := st.run(item); err != nil {
						t.Fatalf("%s %s step %d (%s): %v", file, group.phase, i+1, item.Text, err)
					}
				}
			}
		})
	}
	if selected == 0 {
		t.Fatal("no scenarios matched selection")
	}
}

func (s *state) run(item step) error {
	switch item.Key {
	case "edit", "write":
		var arg struct {
			Path    string `yaml:"path"`
			Append  string `yaml:"append"`
			Content string `yaml:"content"`
		}
		if err := item.decode(&arg); err != nil {
			return err
		}
		if arg.Path == "" || filepath.IsAbs(arg.Path) || strings.HasPrefix(filepath.Clean(arg.Path), "..") {
			return fmt.Errorf("unsafe path %q", arg.Path)
		}
		path := filepath.Join(s.dir, arg.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if item.Key == "write" {
			return os.WriteFile(path, []byte(arg.Content), 0644)
		}
		if arg.Append == "" {
			return fmt.Errorf("edit requires append")
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.WriteString(f, arg.Append)
		return err
	case "git":
		var args []string
		if err := item.decode(&args); err != nil {
			return err
		}
		_, s.lastErr = gitCommand(s.dir, args...)
		return s.lastErr
	case "branch":
		var name string
		if err := item.decode(&name); err != nil {
			return err
		}
		_, s.lastErr = gitCommand(s.dir, "branch", name)
		return s.lastErr
	case "commit":
		var message string
		if err := item.decode(&message); err != nil {
			return err
		}
		_, s.lastErr = gitCommand(s.dir, "commit", "-am", message)
		return s.lastErr
	case "mkdir":
		var path string
		if err := item.decode(&path); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Join(s.dir, path), 0755)
	case "symlink":
		var arg struct {
			Path   string `yaml:"path"`
			Target string `yaml:"target"`
		}
		if err := item.decode(&arg); err != nil {
			return err
		}
		return os.Symlink(arg.Target, filepath.Join(s.dir, arg.Path))
	case "chmod":
		var arg struct {
			Path string `yaml:"path"`
			Mode uint32 `yaml:"mode"`
		}
		if err := item.decode(&arg); err != nil {
			return err
		}
		return os.Chmod(filepath.Join(s.dir, arg.Path), os.FileMode(arg.Mode))
	case "capture":
		var arg struct {
			Attempt string `yaml:"attempt"`
		}
		if err := item.decode(&arg); err != nil {
			return err
		}
		if arg.Attempt == "" {
			return fmt.Errorf("capture requires attempt")
		}
		runner, err := gitexec.New(s.dir, gitexec.Options{})
		if err != nil {
			return err
		}
		if stat, err := gitCommand(s.dir, "diff", "--stat"); err == nil && stat != "" {
			s.t.Logf("fixture git diff --stat: %s", stat)
		}
		start := time.Now()
		s.previous = s.result
		s.result, s.lastErr = capture.Capture(context.Background(), runner, s.dir, capture.Params{Workspace: "lab", Attempt: arg.Attempt, TaskID: "P1.3", TaskTitle: "Git lab"})
		s.elapsed = time.Since(start)
		return s.lastErr
	case "diff_vs_head":
		var arg struct {
			Changed []string `yaml:"changed"`
		}
		if err := item.decode(&arg); err != nil {
			return err
		}
		out, err := gitCommand(s.dir, "diff", "--name-only", "HEAD", s.result.CaptureSHA)
		if err != nil {
			return err
		}
		got := strings.Fields(strings.TrimSpace(out))
		slices.Sort(got)
		slices.Sort(arg.Changed)
		if !reflect.DeepEqual(got, arg.Changed) {
			return fmt.Errorf("diff paths %q, want %q", got, arg.Changed)
		}
		return nil
	case "tree_has", "tree_lacks":
		var paths []string
		if err := item.decode(&paths); err != nil {
			return err
		}
		for _, path := range paths {
			_, err := gitCommand(s.dir, "cat-file", "-e", s.result.CaptureSHA+":"+path)
			if item.Key == "tree_has" && err != nil {
				return fmt.Errorf("missing %s: %w", path, err)
			}
			if item.Key == "tree_lacks" && err == nil {
				return fmt.Errorf("unexpected %s", path)
			}
		}
		return nil
	case "manifest":
		var want map[string]string
		if err := item.decode(&want); err != nil {
			return err
		}
		got := map[string]string{}
		for _, entry := range s.result.Manifest.Entries {
			got[entry.Path] = entry.Class
		}
		for path, class := range want {
			if got[path] != class {
				return fmt.Errorf("manifest %s=%q, want %q", path, got[path], class)
			}
		}
		return nil
	case "capture_complete":
		var want bool
		if err := item.decode(&want); err != nil {
			return err
		}
		if s.result.Manifest.Complete != want {
			return fmt.Errorf("capture complete=%v, want %v", s.result.Manifest.Complete, want)
		}
		return nil
	case "took_under":
		var duration string
		if err := item.decode(&duration); err != nil {
			return err
		}
		limit, err := time.ParseDuration(duration)
		if err != nil {
			return err
		}
		if s.elapsed >= limit {
			return fmt.Errorf("took %s, limit %s", s.elapsed, limit)
		}
		return nil
	case "go_check":
		var name string
		if err := item.decode(&name); err != nil {
			return err
		}
		s.lastErr = s.goCheck(name)
		return s.lastErr
	default:
		return fmt.Errorf("unknown step %q", item.Key)
	}
}

func gitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:norawexec // Git lab fixtures intentionally exercise real Git.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Git Lab", "GIT_AUTHOR_EMAIL=lab@example.test", "GIT_COMMITTER_NAME=Git Lab", "GIT_COMMITTER_EMAIL=lab@example.test", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}
