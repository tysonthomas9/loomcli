package terminal

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type hookRecorder struct {
	mu     sync.Mutex
	issued []string
	ended  []string
	next   int
}

func (h *hookRecorder) hooks() SpawnHooks {
	return SpawnHooks{
		ExtraEnv: func(key SessionKey, launch *LaunchSpec) map[string]string {
			if launch == nil || launch.Env["WANT_SECRET"] != "1" {
				return nil
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			h.next++
			tok := key.Name + "-tok-" + string(rune('0'+h.next))
			h.issued = append(h.issued, tok)
			return map[string]string{"LOOM_AGENT_BROWSER_SESSION": tok}
		},
		Ended: func(_ SessionKey, extra map[string]string) {
			h.mu.Lock()
			h.ended = append(h.ended, extra["LOOM_AGENT_BROWSER_SESSION"])
			h.mu.Unlock()
		},
	}
}

func (h *hookRecorder) endedTokens() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.ended...)
}

func newShellManager(t *testing.T) *PTYManager {
	t.Helper()
	m := NewPTYManager("/bin/sh", 0, t.TempDir())
	m.SetGracePeriod(100 * time.Millisecond)
	t.Cleanup(func() { _ = m.Shutdown() })
	return m
}

func TestSpawnHooksInjectEnvAndRevokeOnKill(t *testing.T) {
	m := newShellManager(t)
	rec := &hookRecorder{}
	m.SetSpawnHooks(rec.hooks())
	key := SessionKey{Workspace: "ws", Name: "lead-term"}
	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{
		Argv: []string{"-c", `echo "SECRET=$LOOM_AGENT_BROWSER_SESSION"; sleep 30`},
		Env:  map[string]string{"WANT_SECRET": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !readChunkContains(t, att, []byte("SECRET=lead-term-tok-1"), 3*time.Second) {
		t.Fatal("spawned process did not receive the hook env")
	}
	if err := m.Kill(key); err != nil {
		t.Fatal(err)
	}
	if got := rec.endedTokens(); len(got) != 1 || got[0] != "lead-term-tok-1" {
		t.Fatalf("ended = %v", got)
	}
}

func TestSpawnHooksRevokeOnNaturalExitAndIgnorePlainSessions(t *testing.T) {
	m := newShellManager(t)
	rec := &hookRecorder{}
	m.SetSpawnHooks(rec.hooks())
	plain := SessionKey{Workspace: "ws", Name: "shell"}
	if _, _, err := m.AttachSession(plain, 80, 24, &LaunchSpec{Argv: []string{"-c", "exit 0"}}); err != nil {
		t.Fatal(err)
	}
	agent := SessionKey{Workspace: "ws", Name: "agent"}
	if _, _, err := m.AttachSession(agent, 80, 24, &LaunchSpec{Argv: []string{"-c", "exit 0"}, Env: map[string]string{"WANT_SECRET": "1"}}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return len(rec.endedTokens()) == 1 && m.SessionCount() == 0 }, 3*time.Second, "agent session ended")
	if got := rec.endedTokens(); got[0] != "agent-tok-1" {
		t.Fatalf("ended = %v (plain sessions must not trigger Ended)", got)
	}
}

// A respawn under the same key must not be revoked by the previous
// process's end: Ended receives the exact env that process was given.
func TestSpawnHooksEndedCarriesPerSpawnSecret(t *testing.T) {
	m := newShellManager(t)
	rec := &hookRecorder{}
	m.SetSpawnHooks(rec.hooks())
	key := SessionKey{Workspace: "ws", Name: "lead"}
	launch := &LaunchSpec{Argv: []string{"-c", "sleep 30"}, Env: map[string]string{"WANT_SECRET": "1"}}
	if _, _, err := m.AttachSession(key, 80, 24, launch); err != nil {
		t.Fatal(err)
	}
	_ = m.Kill(key)
	if _, _, err := m.AttachSession(key, 80, 24, launch); err != nil {
		t.Fatal(err)
	}
	_ = m.Shutdown()
	got := rec.endedTokens()
	if len(got) != 2 || got[0] != "lead-tok-1" || got[1] != "lead-tok-2" {
		t.Fatalf("ended = %v", got)
	}
}

func TestSpawnEnvDropsInheritedAgentSecrets(t *testing.T) {
	env := terminalSpawnEnv([]string{"PATH=/bin", "LOOM_AGENT_BROWSER_SESSION=leaked", "LOOM_AGENT_BROWSER_URL=http://x",
		"LOOM_BROWSER_SESSION_SOCKET=/tmp/runtime.sock"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "leaked") || strings.Contains(joined, "LOOM_AGENT_BROWSER_URL") ||
		strings.Contains(joined, "LOOM_BROWSER_SESSION_SOCKET") {
		t.Fatalf("inherited agent secret reached spawn env: %q", joined)
	}
}

func TestMultiManagerAppliesHooksToNewWorkspaces(t *testing.T) {
	mm := NewMultiPTYManager("/bin/sh", 0)
	t.Cleanup(func() { _ = mm.Close() })
	rec := &hookRecorder{}
	mm.SetSpawnHooks(rec.hooks())
	dir := t.TempDir()
	if err := mm.Register("ws", dir); err != nil {
		t.Fatal(err)
	}
	key := SessionKey{Workspace: "ws", Name: "a"}
	if _, _, err := mm.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "sleep 30"}, Env: map[string]string{"WANT_SECRET": "1"}}); err != nil {
		t.Fatal(err)
	}
	_ = mm.Kill(key)
	if got := rec.endedTokens(); len(got) != 1 {
		t.Fatalf("ended = %v", got)
	}
}
