package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func requireIntPtr(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %d", name, want)
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

func TestLoadProjectFile(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		dir := t.TempDir()
		yamlContent := `daemon:
  pid_file: custom.pid
  log_dir: /var/log/loom
roles:
  plan:
    description: Planning agent
  reviewer:
    prompt_file: prompts/reviewer.md
    model: claude-sonnet-4-20250514
agents:
  - worktree: falcon
    role: plan
    auto: true
  - worktree: nova
    role: reviewer
`
		if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		pf, err := LoadProjectFile(dir)
		if err != nil {
			t.Fatalf("LoadProjectFile() error = %v", err)
		}
		if pf == nil {
			t.Fatal("LoadProjectFile() returned nil")
		}
		if pf.Daemon.PIDFile != "custom.pid" {
			t.Errorf("PIDFile = %q, want %q", pf.Daemon.PIDFile, "custom.pid")
		}
		if len(pf.Roles) != 2 {
			t.Fatalf("len(Roles) = %d, want 2", len(pf.Roles))
		}
		if pf.Roles["reviewer"].Model != "claude-sonnet-4-20250514" {
			t.Errorf("reviewer model = %q, want %q", pf.Roles["reviewer"].Model, "claude-sonnet-4-20250514")
		}
		if len(pf.Agents) != 2 {
			t.Fatalf("len(Agents) = %d, want 2", len(pf.Agents))
		}
		if !pf.Agents[0].Auto {
			t.Error("agents[0].Auto = false, want true")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		pf, err := LoadProjectFile(dir)
		if err != nil {
			t.Fatalf("LoadProjectFile() error = %v, want nil", err)
		}
		if pf != nil {
			t.Errorf("LoadProjectFile() = %+v, want nil", pf)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		pf, err := LoadProjectFile(dir)
		if err != nil {
			t.Fatalf("LoadProjectFile() error = %v", err)
		}
		if pf == nil {
			t.Fatal("LoadProjectFile() returned nil for empty file")
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte("{{bad yaml"), 0644); err != nil {
			t.Fatal(err)
		}
		pf, err := LoadProjectFile(dir)
		if err == nil {
			t.Fatalf("LoadProjectFile() error = nil, want error; pf = %+v", pf)
		}
	})
}

func TestLoadDaemonConfig(t *testing.T) {
	t.Run("defaults only", func(t *testing.T) {
		// No global config, no local config
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		if dc.Daemon.PIDFile != ".loom/daemon.pid" {
			t.Errorf("PIDFile = %q, want %q", dc.Daemon.PIDFile, ".loom/daemon.pid")
		}
		if dc.Daemon.LogDir != ".loom/logs" {
			t.Errorf("LogDir = %q, want %q", dc.Daemon.LogDir, ".loom/logs")
		}
		requireIntPtr(t, "MaxRetries", dc.Daemon.RestartPolicy.MaxRetries, 3)
		requireIntPtr(t, "BackoffInitial", dc.Daemon.RestartPolicy.BackoffInitial, 2)
		requireIntPtr(t, "BackoffMax", dc.Daemon.RestartPolicy.BackoffMax, 300)
		if len(dc.Roles) != 0 {
			t.Errorf("len(Roles) = %d, want 0", len(dc.Roles))
		}
		if len(dc.Agents) != 0 {
			t.Errorf("len(Agents) = %d, want 0", len(dc.Agents))
		}
	})

	t.Run("global config only", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", configDir)
		globalYAML := `daemon:
  pid_file: global.pid
  restart_policy:
    max_retries: 10
`
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(globalYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(t.TempDir())
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		if dc.Daemon.PIDFile != "global.pid" {
			t.Errorf("PIDFile = %q, want %q", dc.Daemon.PIDFile, "global.pid")
		}
		requireIntPtr(t, "MaxRetries", dc.Daemon.RestartPolicy.MaxRetries, 10)
		// LogDir should retain default
		if dc.Daemon.LogDir != ".loom/logs" {
			t.Errorf("LogDir = %q, want default %q", dc.Daemon.LogDir, ".loom/logs")
		}
	})

	t.Run("local config only", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()
		localYAML := `daemon:
  log_dir: /custom/logs
roles:
  task:
    description: Task executor
agents:
  - worktree: alpha
    role: task
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		if dc.Daemon.LogDir != "/custom/logs" {
			t.Errorf("LogDir = %q, want %q", dc.Daemon.LogDir, "/custom/logs")
		}
		if len(dc.Roles) != 1 {
			t.Fatalf("len(Roles) = %d, want 1", len(dc.Roles))
		}
		if dc.Roles["task"].Description != "Task executor" {
			t.Errorf("task description = %q", dc.Roles["task"].Description)
		}
		if len(dc.Agents) != 1 {
			t.Fatalf("len(Agents) = %d, want 1", len(dc.Agents))
		}
		if dc.Agents[0].Worktree != "alpha" {
			t.Errorf("agents[0].Worktree = %q, want %q", dc.Agents[0].Worktree, "alpha")
		}
	})

	t.Run("local overrides global", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", configDir)
		globalYAML := `daemon:
  pid_file: global.pid
  log_dir: /global/logs
  restart_policy:
    max_retries: 10
    backoff_initial: 5
`
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(globalYAML), 0644); err != nil {
			t.Fatal(err)
		}

		projectDir := t.TempDir()
		localYAML := `daemon:
  pid_file: local.pid
  restart_policy:
    max_retries: 7
agents:
  - worktree: beta
    role: plan
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		// local pid_file wins
		if dc.Daemon.PIDFile != "local.pid" {
			t.Errorf("PIDFile = %q, want %q", dc.Daemon.PIDFile, "local.pid")
		}
		// global log_dir (local didn't set it)
		if dc.Daemon.LogDir != "/global/logs" {
			t.Errorf("LogDir = %q, want %q", dc.Daemon.LogDir, "/global/logs")
		}
		// local max_retries wins
		requireIntPtr(t, "MaxRetries", dc.Daemon.RestartPolicy.MaxRetries, 7)
		// global backoff_initial (local didn't set it)
		requireIntPtr(t, "BackoffInitial", dc.Daemon.RestartPolicy.BackoffInitial, 5)
	})

	t.Run("local sets zero to override global", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", configDir)
		globalYAML := `daemon:
  restart_policy:
    max_retries: 10
`
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(globalYAML), 0644); err != nil {
			t.Fatal(err)
		}

		projectDir := t.TempDir()
		localYAML := `daemon:
  restart_policy:
    max_retries: 0
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		// local max_retries: 0 should override global max_retries: 10
		requireIntPtr(t, "MaxRetries", dc.Daemon.RestartPolicy.MaxRetries, 0)
	})

	t.Run("agent validation missing worktree", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()
		localYAML := `agents:
  - role: plan
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadDaemonConfig(projectDir)
		if err == nil {
			t.Fatal("expected error for missing worktree")
		}
		if !strings.Contains(err.Error(), "worktree is required") {
			t.Errorf("error = %q, want contains 'worktree is required'", err.Error())
		}
	})

	t.Run("agent validation missing role", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()
		localYAML := `agents:
  - worktree: alpha
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadDaemonConfig(projectDir)
		if err == nil {
			t.Fatal("expected error for missing role")
		}
		if !strings.Contains(err.Error(), "role is required") {
			t.Errorf("error = %q, want contains 'role is required'", err.Error())
		}
	})
}

func TestResolveRole(t *testing.T) {
	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"plan": {Description: "Planning agent"},
			"reviewer": {
				Description: "Code reviewer",
				PromptFile:  "prompts/reviewer.md",
				Model:       "claude-sonnet-4-20250514",
			},
		},
	}

	t.Run("found", func(t *testing.T) {
		rc, ok := dc.ResolveRole("plan")
		if !ok {
			t.Fatal("ResolveRole(plan) returned false")
		}
		if rc.Description != "Planning agent" {
			t.Errorf("Description = %q", rc.Description)
		}
	})

	t.Run("found with prompt_file", func(t *testing.T) {
		rc, ok := dc.ResolveRole("reviewer")
		if !ok {
			t.Fatal("ResolveRole(reviewer) returned false")
		}
		if rc.PromptFile != "prompts/reviewer.md" {
			t.Errorf("PromptFile = %q", rc.PromptFile)
		}
		if rc.Model != "claude-sonnet-4-20250514" {
			t.Errorf("Model = %q", rc.Model)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := dc.ResolveRole("nonexistent")
		if ok {
			t.Error("ResolveRole(nonexistent) returned true")
		}
	})
}

func TestLoadPromptTemplate(t *testing.T) {
	t.Run("valid template", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "prompt.md")
		content := `You are {{.AgentName}} working on {{.WorktreeName}} as {{.Role}}.
Task: {{.TaskID}}`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := LoadPromptTemplate(path, PromptData{
			AgentName:    "falcon",
			WorktreeName: "falcon-wt",
			Role:         "plan",
			TaskID:       "loomcli-123",
		})
		if err != nil {
			t.Fatalf("LoadPromptTemplate() error = %v", err)
		}
		want := "You are falcon working on falcon-wt as plan.\nTask: loomcli-123"
		if result != want {
			t.Errorf("result = %q, want %q", result, want)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadPromptTemplate("/nonexistent/prompt.md", PromptData{})
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("bad template syntax", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.md")
		if err := os.WriteFile(path, []byte("{{.Invalid template"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadPromptTemplate(path, PromptData{})
		if err == nil {
			t.Fatal("expected error for bad template")
		}
	})

	t.Run("template execution error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "exec_err.md")
		// {{.NonexistentField}} will cause an execution error on a struct
		if err := os.WriteFile(path, []byte("Hello {{.NonexistentField}}"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadPromptTemplate(path, PromptData{})
		if err == nil {
			t.Fatal("expected error for nonexistent field")
		}
	})

	t.Run("template with no variables", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "static.md")
		if err := os.WriteFile(path, []byte("Static prompt content"), 0644); err != nil {
			t.Fatal(err)
		}
		result, err := LoadPromptTemplate(path, PromptData{})
		if err != nil {
			t.Fatalf("LoadPromptTemplate() error = %v", err)
		}
		if result != "Static prompt content" {
			t.Errorf("result = %q, want %q", result, "Static prompt content")
		}
	})
}

func TestMaxAgents(t *testing.T) {
	t.Run("default max_agents is 20", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		requireIntPtr(t, "MaxAgents", dc.Daemon.MaxAgents, 20)
	})

	t.Run("global config sets max_agents", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", configDir)
		globalYAML := `daemon:
  max_agents: 10
`
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(globalYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(t.TempDir())
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		requireIntPtr(t, "MaxAgents", dc.Daemon.MaxAgents, 10)
	})

	t.Run("local config overrides global max_agents", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", configDir)
		globalYAML := `daemon:
  max_agents: 10
`
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(globalYAML), 0644); err != nil {
			t.Fatal(err)
		}

		projectDir := t.TempDir()
		localYAML := `daemon:
  max_agents: 5
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v", err)
		}
		requireIntPtr(t, "MaxAgents", dc.Daemon.MaxAgents, 5)
	})

	t.Run("max_agents 0 means unlimited", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()
		localYAML := `daemon:
  max_agents: 0
agents:
  - worktree: a1
    role: plan
  - worktree: a2
    role: plan
  - worktree: a3
    role: plan
  - worktree: a4
    role: plan
  - worktree: a5
    role: plan
  - worktree: a6
    role: plan
  - worktree: a7
    role: plan
  - worktree: a8
    role: plan
  - worktree: a9
    role: plan
  - worktree: a10
    role: plan
  - worktree: a11
    role: plan
  - worktree: a12
    role: plan
  - worktree: a13
    role: plan
  - worktree: a14
    role: plan
  - worktree: a15
    role: plan
  - worktree: a16
    role: plan
  - worktree: a17
    role: plan
  - worktree: a18
    role: plan
  - worktree: a19
    role: plan
  - worktree: a20
    role: plan
  - worktree: a21
    role: plan
  - worktree: a22
    role: plan
  - worktree: a23
    role: plan
  - worktree: a24
    role: plan
  - worktree: a25
    role: plan
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v, want nil (0 means unlimited)", err)
		}
		requireIntPtr(t, "MaxAgents", dc.Daemon.MaxAgents, 0)
		if len(dc.Agents) != 25 {
			t.Errorf("len(Agents) = %d, want 25", len(dc.Agents))
		}
	})

	t.Run("exceeding max_agents returns error", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()
		localYAML := `daemon:
  max_agents: 2
agents:
  - worktree: a1
    role: plan
  - worktree: a2
    role: plan
  - worktree: a3
    role: plan
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := LoadDaemonConfig(projectDir)
		if err == nil {
			t.Fatal("expected error for exceeding max_agents")
		}
		if !strings.Contains(err.Error(), "too many agents configured") {
			t.Errorf("error = %q, want contains 'too many agents configured'", err.Error())
		}
	})

	t.Run("exactly at max_agents passes", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()
		localYAML := `daemon:
  max_agents: 2
agents:
  - worktree: a1
    role: plan
  - worktree: a2
    role: plan
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		dc, err := LoadDaemonConfig(projectDir)
		if err != nil {
			t.Fatalf("LoadDaemonConfig() error = %v, want nil", err)
		}
		if len(dc.Agents) != 2 {
			t.Errorf("len(Agents) = %d, want 2", len(dc.Agents))
		}
	})

	t.Run("negative max_agents returns error", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		projectDir := t.TempDir()
		localYAML := `daemon:
  max_agents: -1
`
		if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := LoadDaemonConfig(projectDir)
		if err == nil {
			t.Fatal("expected error for negative max_agents")
		}
		if !strings.Contains(err.Error(), "must be non-negative") {
			t.Errorf("error = %q, want contains 'must be non-negative'", err.Error())
		}
	})
}

func TestResolveDaemonStatePath_DefaultConfig(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	projectDir := t.TempDir()

	got := ResolveDaemonStatePath(projectDir)
	want := filepath.Join(projectDir, ".loom", "daemon-agents.json")
	if got != want {
		t.Errorf("ResolveDaemonStatePath() = %q, want %q", got, want)
	}
}

func TestResolveDaemonStatePath_CustomPIDFile(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	projectDir := t.TempDir()
	localYAML := `daemon:
  pid_file: /tmp/custom/daemon.pid
`
	if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
		t.Fatal(err)
	}

	got := ResolveDaemonStatePath(projectDir)
	want := "/tmp/custom/daemon-agents.json"
	if got != want {
		t.Errorf("ResolveDaemonStatePath() = %q, want %q", got, want)
	}
}

func TestResolveDaemonStatePath_RelativePIDFile(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	projectDir := t.TempDir()
	localYAML := `daemon:
  pid_file: custom/daemon.pid
`
	if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
		t.Fatal(err)
	}

	got := ResolveDaemonStatePath(projectDir)
	want := filepath.Join(projectDir, "custom", "daemon-agents.json")
	if got != want {
		t.Errorf("ResolveDaemonStatePath() = %q, want %q", got, want)
	}
}

func TestResolveDaemonStatePath_InvalidConfig(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte("{{{invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	got := ResolveDaemonStatePath(projectDir)
	want := filepath.Join(projectDir, ".loom", "daemon-agents.json")
	if got != want {
		t.Errorf("ResolveDaemonStatePath() = %q, want %q (fallback on invalid config)", got, want)
	}
}

func TestRoleConfig_YAMLRoundtrip(t *testing.T) {
	original := RoleConfig{
		Description:    "reviewer",
		PromptFile:     "prompts/review.md",
		Model:          "claude-sonnet-4-20250514",
		TaskFilter:     "has_design",
		Backend:        "openai",
		PathPatterns:   []string{"internal/**", "*.go"},
		Skills:         []string{"review", "security"},
		MaxPriority:    intPtr(2),
		MaxConcurrency: intPtr(3),
		ReadOnly:       true,
		AllowedTools:   []string{"read", "grep"},
		DeniedTools:    []string{"bash"},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded RoleConfig
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Description != original.Description {
		t.Errorf("Description = %q, want %q", decoded.Description, original.Description)
	}
	if decoded.PromptFile != original.PromptFile {
		t.Errorf("PromptFile = %q, want %q", decoded.PromptFile, original.PromptFile)
	}
	if decoded.Model != original.Model {
		t.Errorf("Model = %q, want %q", decoded.Model, original.Model)
	}
	if decoded.TaskFilter != original.TaskFilter {
		t.Errorf("TaskFilter = %q, want %q", decoded.TaskFilter, original.TaskFilter)
	}
	if decoded.Backend != original.Backend {
		t.Errorf("Backend = %q, want %q", decoded.Backend, original.Backend)
	}
	if !reflect.DeepEqual(decoded.PathPatterns, original.PathPatterns) {
		t.Errorf("PathPatterns = %v, want %v", decoded.PathPatterns, original.PathPatterns)
	}
	if !reflect.DeepEqual(decoded.Skills, original.Skills) {
		t.Errorf("Skills = %v, want %v", decoded.Skills, original.Skills)
	}
	requireIntPtr(t, "MaxPriority", decoded.MaxPriority, 2)
	requireIntPtr(t, "MaxConcurrency", decoded.MaxConcurrency, 3)
	if decoded.ReadOnly != true {
		t.Errorf("ReadOnly = %v, want true", decoded.ReadOnly)
	}
	if !reflect.DeepEqual(decoded.AllowedTools, original.AllowedTools) {
		t.Errorf("AllowedTools = %v, want %v", decoded.AllowedTools, original.AllowedTools)
	}
	if !reflect.DeepEqual(decoded.DeniedTools, original.DeniedTools) {
		t.Errorf("DeniedTools = %v, want %v", decoded.DeniedTools, original.DeniedTools)
	}
}

func TestRoleConfig_YAMLRoundtrip_Minimal(t *testing.T) {
	original := RoleConfig{
		Description: "basic role",
		PromptFile:  "prompts/basic.md",
		Model:       "claude-sonnet-4-20250514",
		TaskFilter:  "open",
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded RoleConfig
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Description != original.Description {
		t.Errorf("Description = %q, want %q", decoded.Description, original.Description)
	}
	if decoded.Backend != "" {
		t.Errorf("Backend = %q, want empty", decoded.Backend)
	}
	if decoded.PathPatterns != nil {
		t.Errorf("PathPatterns = %v, want nil", decoded.PathPatterns)
	}
	if decoded.Skills != nil {
		t.Errorf("Skills = %v, want nil", decoded.Skills)
	}
	if decoded.MaxPriority != nil {
		t.Errorf("MaxPriority = %v, want nil", decoded.MaxPriority)
	}
	if decoded.MaxConcurrency != nil {
		t.Errorf("MaxConcurrency = %v, want nil", decoded.MaxConcurrency)
	}
	if decoded.ReadOnly != false {
		t.Errorf("ReadOnly = %v, want false", decoded.ReadOnly)
	}
	if decoded.AllowedTools != nil {
		t.Errorf("AllowedTools = %v, want nil", decoded.AllowedTools)
	}
	if decoded.DeniedTools != nil {
		t.Errorf("DeniedTools = %v, want nil", decoded.DeniedTools)
	}
}

func TestAgentEntry_YAMLRoundtrip(t *testing.T) {
	original := AgentEntry{
		Worktree:     "falcon",
		Role:         "plan",
		Auto:         true,
		Backend:      "anthropic",
		PathPatterns: []string{"cmd/**", "internal/cli/**"},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded AgentEntry
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Worktree != original.Worktree {
		t.Errorf("Worktree = %q, want %q", decoded.Worktree, original.Worktree)
	}
	if decoded.Role != original.Role {
		t.Errorf("Role = %q, want %q", decoded.Role, original.Role)
	}
	if decoded.Auto != original.Auto {
		t.Errorf("Auto = %v, want %v", decoded.Auto, original.Auto)
	}
	if decoded.Backend != original.Backend {
		t.Errorf("Backend = %q, want %q", decoded.Backend, original.Backend)
	}
	if !reflect.DeepEqual(decoded.PathPatterns, original.PathPatterns) {
		t.Errorf("PathPatterns = %v, want %v", decoded.PathPatterns, original.PathPatterns)
	}
}

func TestAgentEntry_YAMLRoundtrip_Minimal(t *testing.T) {
	original := AgentEntry{
		Worktree: "nova",
		Role:     "task",
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded AgentEntry
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Worktree != original.Worktree {
		t.Errorf("Worktree = %q, want %q", decoded.Worktree, original.Worktree)
	}
	if decoded.Role != original.Role {
		t.Errorf("Role = %q, want %q", decoded.Role, original.Role)
	}
	if decoded.PathPatterns != nil {
		t.Errorf("PathPatterns = %v, want nil", decoded.PathPatterns)
	}
}

func TestLoadProjectFile_WithNewRoleFields(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `roles:
  reviewer:
    description: Code reviewer
    prompt_file: prompts/reviewer.md
    model: claude-sonnet-4-20250514
    task_filter: has_design
    backend: openai
    path_patterns:
      - "internal/**"
      - "*.go"
    skills:
      - review
      - security
    max_priority: 2
    max_concurrency: 3
    read_only: true
    allowed_tools:
      - read
      - grep
    denied_tools:
      - bash
agents:
  - worktree: falcon
    role: reviewer
    path_patterns:
      - "cmd/**"
`
	if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	pf, err := LoadProjectFile(dir)
	if err != nil {
		t.Fatalf("LoadProjectFile() error = %v", err)
	}
	if pf == nil {
		t.Fatal("LoadProjectFile() returned nil")
	}

	rc := pf.Roles["reviewer"]
	if rc.Description != "Code reviewer" {
		t.Errorf("Description = %q", rc.Description)
	}
	if rc.Backend != "openai" {
		t.Errorf("Backend = %q, want %q", rc.Backend, "openai")
	}
	if !reflect.DeepEqual(rc.PathPatterns, []string{"internal/**", "*.go"}) {
		t.Errorf("PathPatterns = %v", rc.PathPatterns)
	}
	if !reflect.DeepEqual(rc.Skills, []string{"review", "security"}) {
		t.Errorf("Skills = %v", rc.Skills)
	}
	requireIntPtr(t, "MaxPriority", rc.MaxPriority, 2)
	requireIntPtr(t, "MaxConcurrency", rc.MaxConcurrency, 3)
	if !rc.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
	if !reflect.DeepEqual(rc.AllowedTools, []string{"read", "grep"}) {
		t.Errorf("AllowedTools = %v", rc.AllowedTools)
	}
	if !reflect.DeepEqual(rc.DeniedTools, []string{"bash"}) {
		t.Errorf("DeniedTools = %v", rc.DeniedTools)
	}

	// Verify agent PathPatterns
	if len(pf.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(pf.Agents))
	}
	if !reflect.DeepEqual(pf.Agents[0].PathPatterns, []string{"cmd/**"}) {
		t.Errorf("Agent PathPatterns = %v", pf.Agents[0].PathPatterns)
	}
}

func TestLoadDaemonConfig_NewRoleFieldsPreserved(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	globalYAML := `roles:
  plan:
    description: Global planner
    max_priority: 1
    skills:
      - planning
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(globalYAML), 0644); err != nil {
		t.Fatal(err)
	}

	projectDir := t.TempDir()
	localYAML := `roles:
  plan:
    description: Local planner
    backend: anthropic
    max_concurrency: 2
    read_only: true
    path_patterns:
      - "docs/**"
agents:
  - worktree: alpha
    role: plan
`
	if err := os.WriteFile(filepath.Join(projectDir, "loom.yaml"), []byte(localYAML), 0644); err != nil {
		t.Fatal(err)
	}

	dc, err := LoadDaemonConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error = %v", err)
	}

	rc := dc.Roles["plan"]
	// Local replaces entire role entry, so global fields are gone
	if rc.Description != "Local planner" {
		t.Errorf("Description = %q, want %q", rc.Description, "Local planner")
	}
	if rc.Backend != "anthropic" {
		t.Errorf("Backend = %q, want %q", rc.Backend, "anthropic")
	}
	requireIntPtr(t, "MaxConcurrency", rc.MaxConcurrency, 2)
	if !rc.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
	if !reflect.DeepEqual(rc.PathPatterns, []string{"docs/**"}) {
		t.Errorf("PathPatterns = %v", rc.PathPatterns)
	}
	// Global-only fields should be gone since local replaces entire entry
	if rc.MaxPriority != nil {
		t.Errorf("MaxPriority = %v, want nil (local replaced entire entry)", rc.MaxPriority)
	}
	if rc.Skills != nil {
		t.Errorf("Skills = %v, want nil (local replaced entire entry)", rc.Skills)
	}
}

func TestLoadProjectFile_BackwardCompatibility(t *testing.T) {
	dir := t.TempDir()
	// Config using only original fields — no new fields present
	yamlContent := `roles:
  plan:
    description: Planning agent
    prompt_file: prompts/plan.md
    model: claude-sonnet-4-20250514
    task_filter: open
agents:
  - worktree: falcon
    role: plan
    auto: true
    backend: claude
`
	if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	pf, err := LoadProjectFile(dir)
	if err != nil {
		t.Fatalf("LoadProjectFile() error = %v", err)
	}
	if pf == nil {
		t.Fatal("LoadProjectFile() returned nil")
	}

	rc := pf.Roles["plan"]
	if rc.Description != "Planning agent" {
		t.Errorf("Description = %q", rc.Description)
	}
	// All new fields should be zero values
	if rc.Backend != "" {
		t.Errorf("Backend = %q, want empty", rc.Backend)
	}
	if rc.PathPatterns != nil {
		t.Errorf("PathPatterns = %v, want nil", rc.PathPatterns)
	}
	if rc.Skills != nil {
		t.Errorf("Skills = %v, want nil", rc.Skills)
	}
	if rc.MaxPriority != nil {
		t.Errorf("MaxPriority = %v, want nil", rc.MaxPriority)
	}
	if rc.MaxConcurrency != nil {
		t.Errorf("MaxConcurrency = %v, want nil", rc.MaxConcurrency)
	}
	if rc.ReadOnly {
		t.Error("ReadOnly = true, want false")
	}
	if rc.AllowedTools != nil {
		t.Errorf("AllowedTools = %v, want nil", rc.AllowedTools)
	}
	if rc.DeniedTools != nil {
		t.Errorf("DeniedTools = %v, want nil", rc.DeniedTools)
	}

	// Agent PathPatterns should be nil
	if pf.Agents[0].PathPatterns != nil {
		t.Errorf("Agent PathPatterns = %v, want nil", pf.Agents[0].PathPatterns)
	}
}
