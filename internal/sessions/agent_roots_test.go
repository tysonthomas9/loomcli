package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stageProfileDir creates <projectDir>/.loom/agent-profiles/<agent>/<backend>
// and returns it — the layout the supervisor injects as CLAUDE_CONFIG_DIR /
// CODEX_HOME.
func stageProfileDir(t *testing.T, projectDir, agent, backend string) string {
	t.Helper()
	dir := filepath.Join(projectDir, ".loom", "agent-profiles", agent, backend)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	return dir
}

// TestClaudeConfigDirFor_Precedence pins profile → env → $HOME. The profile
// must win over the environment: in the daemon and in `loom doctor` the
// ambient CLAUDE_CONFIG_DIR belongs to a different identity than the agent
// whose transcript is being read, so honoring it there reads the wrong roots.
func TestClaudeConfigDirFor_Precedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	envDir := t.TempDir()

	// Nothing set: legacy ~/.claude.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got, want := ClaudeConfigDirFor(project, "jack"), filepath.Join(home, ".claude"); got != want {
		t.Errorf("no profile, no env: got %q, want %q", got, want)
	}

	// Env only: env wins over $HOME (the pre-existing contract).
	t.Setenv("CLAUDE_CONFIG_DIR", envDir)
	if got := ClaudeConfigDirFor(project, "jack"); got != envDir {
		t.Errorf("no profile, env set: got %q, want %q", got, envDir)
	}

	// Profile present: it beats a conflicting env var.
	profile := stageProfileDir(t, project, "jack", "claude")
	if got := ClaudeConfigDirFor(project, "jack"); got != profile {
		t.Errorf("profile + conflicting env: got %q, want %q", got, profile)
	}

	// A different agent has no profile and falls back to the env var.
	if got := ClaudeConfigDirFor(project, "jill"); got != envDir {
		t.Errorf("other agent: got %q, want %q", got, envDir)
	}

	// No agent context at all resolves exactly like the process-scoped call.
	if got := ClaudeConfigDirFor("", ""); got != ClaudeConfigDir() {
		t.Errorf("empty context: got %q, want %q", got, ClaudeConfigDir())
	}
}

func TestClaudeProjectsRootFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	project := t.TempDir()
	profile := stageProfileDir(t, project, "jack", "claude")

	if got, want := ClaudeProjectsRootFor(project, "jack"), filepath.Join(profile, "projects"); got != want {
		t.Errorf("profiled agent: got %q, want %q", got, want)
	}
	if got, want := ClaudeProjectsRootFor(project, "jill"), filepath.Join(home, ".claude", "projects"); got != want {
		t.Errorf("unprofiled agent: got %q, want %q", got, want)
	}
}

// TestCodexSessionsRootFor_Precedence mirrors the Claude test and additionally
// pins the stat guard: a profile with no sessions/ dir yields "", exactly as
// CodexSessionsRoot does, so callers keep treating "" as "nothing to mirror".
func TestCodexSessionsRootFor_Precedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envHome := t.TempDir()
	project := t.TempDir()

	// Legacy: ~/.codex/sessions once it exists.
	t.Setenv("CODEX_HOME", "")
	if got := CodexSessionsRootFor(project, "jack"); got != "" {
		t.Errorf("nothing staged: got %q, want \"\"", got)
	}
	legacy := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CodexSessionsRootFor(project, "jack"); got != legacy {
		t.Errorf("legacy: got %q, want %q", got, legacy)
	}

	// Env wins over $HOME.
	envSessions := filepath.Join(envHome, "sessions")
	if err := os.MkdirAll(envSessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", envHome)
	if got := CodexSessionsRootFor(project, "jack"); got != envSessions {
		t.Errorf("env: got %q, want %q", got, envSessions)
	}

	// Profile dir present but empty (provisioned, never used): the stat guard
	// returns "" rather than a path that cannot be walked.
	profile := stageProfileDir(t, project, "jack", "codex")
	if got := CodexSessionsRootFor(project, "jack"); got != "" {
		t.Errorf("profile without sessions/: got %q, want \"\"", got)
	}

	// Profile with sessions/ beats the env var.
	profileSessions := filepath.Join(profile, "sessions")
	if err := os.MkdirAll(profileSessions, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CodexSessionsRootFor(project, "jack"); got != profileSessions {
		t.Errorf("profile: got %q, want %q", got, profileSessions)
	}
	// Unprofiled agent still gets the env root.
	if got := CodexSessionsRootFor(project, "jill"); got != envSessions {
		t.Errorf("other agent: got %q, want %q", got, envSessions)
	}
}

func TestStoreRootDir(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := store.RootDir(); got != runtimeDir {
		t.Errorf("RootDir = %q, want %q", got, runtimeDir)
	}
	var nilStore *Store
	if got := nilStore.RootDir(); got != "" {
		t.Errorf("nil store RootDir = %q, want \"\"", got)
	}
	if got := (&Store{}).RootDir(); got != "" {
		t.Errorf("zero store RootDir = %q, want \"\"", got)
	}
}

// newProfiledSession returns a store rooted at projectDir plus a session owned
// by agent — the shape the daemon finalizes.
func newProfiledSession(t *testing.T, projectDir, agent, backend string) (*Store, *Session) {
	t.Helper()
	store, err := NewStore(projectDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(CreateOptions{AgentName: agent, Backend: backend, Phase: "implementation"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return store, sess
}

// TestSyncLatestClaudeTranscript_ReadsAgentProfile is the regression this task
// exists for: the daemon finalizes after the agent is reaped, so it has no
// CLAUDE_CONFIG_DIR of its own. Discovery must find the transcript under the
// agent's profile anyway, or the session mirrors nothing and records 0 tokens.
func TestSyncLatestClaudeTranscript_ReadsAgentProfile(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "") // the daemon's env: no override

	project := t.TempDir()
	const agent = "jack-worker"
	store, sess := newProfiledSession(t, project, agent, "claude")

	profile := stageProfileDir(t, project, agent, "claude")
	workDir := "/work/tree/" + agent
	dir := filepath.Join(profile, "projects", encodeClaudeCWD(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n"
	src := filepath.Join(dir, "uuid-1111.jsonl")
	if err := os.WriteFile(src, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := sess.SyncLatestClaudeTranscript(workDir, "uuid-1111", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("SyncLatestClaudeTranscript: %v", err)
	}
	if got != src {
		t.Fatalf("resolved %q, want %q", got, src)
	}
	data, err := os.ReadFile(store.NativeTranscriptPath(sess.SessionID()))
	if err != nil {
		t.Fatalf("read mirrored transcript: %v", err)
	}
	if string(data) != want {
		t.Errorf("mirrored content = %q, want %q", data, want)
	}
}

// TestSyncLatestClaudeTranscript_NoCrossAgentLeak is the negative twin: a
// session belonging to a different agent must not pick up this profile's
// transcript.
func TestSyncLatestClaudeTranscript_NoCrossAgentLeak(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	project := t.TempDir()
	_, sess := newProfiledSession(t, project, "jill-worker", "claude")

	profile := stageProfileDir(t, project, "jack-worker", "claude")
	workDir := "/work/tree/shared"
	dir := filepath.Join(profile, "projects", encodeClaudeCWD(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uuid-1111.jsonl"), []byte("LEAK\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := sess.SyncLatestClaudeTranscript(workDir, "uuid-1111", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("SyncLatestClaudeTranscript: %v", err)
	}
	if got != "" {
		t.Errorf("resolved %q from another agent's profile, want \"\"", got)
	}
}

// TestSyncLatestClaudeTranscript_NoProfileIsUnchanged pins the opt-in
// contract: with no profile directory the session still resolves through
// CLAUDE_CONFIG_DIR exactly as before this change.
func TestSyncLatestClaudeTranscript_NoProfileIsUnchanged(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	_, sess := newProfiledSession(t, project, "jack-worker", "claude")

	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	workDir := "/work/tree/jack-worker"
	dir := filepath.Join(configDir, "projects", encodeClaudeCWD(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "uuid-1111.jsonl")
	if err := os.WriteFile(src, []byte("ENV\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := sess.SyncLatestClaudeTranscript(workDir, "uuid-1111", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("SyncLatestClaudeTranscript: %v", err)
	}
	if got != src {
		t.Errorf("resolved %q, want %q (env must still be honored without a profile)", got, src)
	}
}

// TestSyncLatestCodexRollout_ReadsAgentProfile is the Codex half of the daemon
// regression. The rollout sits directly under sessions/ (codex's flat layout),
// which findLatestCodexRollout walks when no date directories exist.
func TestSyncLatestCodexRollout_ReadsAgentProfile(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")

	project := t.TempDir()
	const agent = "jack-worker"
	store, sess := newProfiledSession(t, project, agent, "codex")

	workDir := t.TempDir()
	sessionsRoot := filepath.Join(stageProfileDir(t, project, agent, "codex"), "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	want := `{"type":"session_meta","payload":{"cwd":"` + workDir + `"}}` + "\n"
	src := filepath.Join(sessionsRoot, "rollout-2026-08-18T10-00-00-11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.WriteFile(src, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := sess.SyncLatestCodexRollout(workDir, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("SyncLatestCodexRollout: %v", err)
	}
	if got != src {
		t.Fatalf("resolved %q, want %q", got, src)
	}
	data, err := os.ReadFile(store.NativeTranscriptPath(sess.SessionID()))
	if err != nil {
		t.Fatalf("read mirrored rollout: %v", err)
	}
	if string(data) != want {
		t.Errorf("mirrored content = %q, want %q", data, want)
	}
}

func TestSyncLatestCodexRollout_NoCrossAgentLeak(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")

	project := t.TempDir()
	_, sess := newProfiledSession(t, project, "jill-worker", "codex")

	workDir := t.TempDir()
	sessionsRoot := filepath.Join(stageProfileDir(t, project, "jack-worker", "codex"), "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"session_meta","payload":{"cwd":"` + workDir + `"}}` + "\n"
	src := filepath.Join(sessionsRoot, "rollout-2026-08-18T10-00-00-11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.WriteFile(src, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := sess.SyncLatestCodexRollout(workDir, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("SyncLatestCodexRollout: %v", err)
	}
	if got != "" {
		t.Errorf("resolved %q from another agent's profile, want \"\"", got)
	}
}

// TestLatestHarnessSessionIDFor_ProfileRoot covers the id recovery the
// supervisor uses when the worker's in-process capture is already gone.
func TestLatestHarnessSessionIDFor_ProfileRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	project := t.TempDir()
	const agent = "jack-worker"
	const uuid = "11111111-2222-3333-4444-555555555555"
	workDir := "/work/tree/" + agent
	dir := filepath.Join(stageProfileDir(t, project, agent, "claude"), "projects", encodeClaudeCWD(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, uuid+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := LatestHarnessSessionIDFor(project, agent, "claude", workDir, "", time.Now().Add(-time.Hour)); got != uuid {
		t.Errorf("LatestHarnessSessionIDFor = %q, want %q", got, uuid)
	}
	// The process-scoped entry point sees nothing — that is the bug this fixes.
	if got := LatestHarnessSessionID("claude", workDir, "", time.Now().Add(-time.Hour)); got != "" {
		t.Errorf("LatestHarnessSessionID = %q, want \"\" (no profile context)", got)
	}
}
