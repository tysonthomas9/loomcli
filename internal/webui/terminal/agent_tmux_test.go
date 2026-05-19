package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/workspace"
)

func TestNewAgentTmuxManagerUsesLookPathAndDefaultEnv(t *testing.T) {
	tmux := writeFakeTmux(t)
	oldLookPath := lookPathTmux
	lookPathTmux = func(name string) (string, error) {
		if name != "tmux" {
			t.Fatalf("lookPathTmux name = %q, want tmux", name)
		}
		return tmux, nil
	}
	oldTmpDir, hadTmpDir := os.LookupEnv("TMUX_TMPDIR")
	_ = os.Unsetenv("TMUX_TMPDIR")
	t.Cleanup(func() {
		lookPathTmux = oldLookPath
		if hadTmpDir {
			_ = os.Setenv("TMUX_TMPDIR", oldTmpDir)
		} else {
			_ = os.Unsetenv("TMUX_TMPDIR")
		}
	})

	manager, err := NewAgentTmuxManager(0)
	if err != nil {
		t.Fatalf("NewAgentTmuxManager() error = %v", err)
	}
	if manager.tmuxPath != tmux {
		t.Fatalf("tmuxPath = %q, want %q", manager.tmuxPath, tmux)
	}
	if manager.MaxSessions() != defaultPTYMaxSessions {
		t.Fatalf("MaxSessions = %d, want default %d", manager.MaxSessions(), defaultPTYMaxSessions)
	}
	if !containsString(manager.env, "TMUX_TMPDIR=/tmp") {
		t.Fatalf("manager env missing TMUX_TMPDIR=/tmp: %v", manager.env)
	}
}

func TestAgentTmuxManagerListFindAndProbe(t *testing.T) {
	dir := t.TempDir()
	tmux := writeFakeTmux(t)
	sessionsPath := filepath.Join(dir, "sessions")
	wsPrefix := workspace.ShortWorkspaceID("WORKSPACE-123")
	if err := os.WriteFile(sessionsPath, []byte(strings.Join([]string{
		"malformed",
		"loom-" + wsPrefix + "-lead-nova-100\t100",
		"loom-" + wsPrefix + "-worker-nova-101\t101",
		"loom-" + wsPrefix + "-worker-nova-099\tbad",
		"loom-other-worker-nova-999\t999",
		"loom-" + wsPrefix + "-worker-orion-999\t999",
		"",
	}, "\n")), 0600); err != nil {
		t.Fatalf("write sessions: %v", err)
	}
	manager := &AgentTmuxManager{
		tmuxPath: tmux,
		env: append(os.Environ(),
			"TMUX_SESSIONS="+sessionsPath,
			"TMUX_DEAD_SESSION=dead-pane",
		),
		max:   2,
		conns: map[string]*AgentTmuxConn{},
	}

	sessions, err := manager.listTmuxSessions()
	if err != nil {
		t.Fatalf("listTmuxSessions() error = %v", err)
	}
	if len(sessions) != 4 {
		t.Fatalf("sessions len = %d, want 4: %#v", len(sessions), sessions)
	}
	got, ok, err := manager.FindLatestAgentSession("WORKSPACE-123", "nova")
	if err != nil {
		t.Fatalf("FindLatestAgentSession() error = %v", err)
	}
	if !ok || got != "loom-"+wsPrefix+"-worker-nova-101" {
		t.Fatalf("FindLatestAgentSession = %q/%v, want newest nova session", got, ok)
	}
	if _, ok, err := manager.FindLatestAgentSession("", "nova"); err != nil || ok {
		t.Fatalf("empty workspace FindLatestAgentSession ok=%v err=%v, want no match", ok, err)
	}
	if _, _, err := manager.FindLatestAgentSession("WORKSPACE-123", "../bad"); err == nil {
		t.Fatal("invalid agent name error = nil")
	}
	if !manager.HasSession("exists") || manager.HasSession("missing") {
		t.Fatal("HasSession did not follow fake tmux exit status")
	}
	if !manager.PaneDead("dead-pane") || manager.PaneDead("live-pane") {
		t.Fatal("PaneDead did not parse fake tmux pane state")
	}
	if got := manager.CapturePane("live-pane", 0); got != "line one\nline two" {
		t.Fatalf("CapturePane = %q", got)
	}
}

func TestAgentTmuxManagerListSessionsNoServerAndErrors(t *testing.T) {
	tmux := writeFakeTmux(t)
	for _, tt := range []struct {
		name string
		mode string
		err  bool
	}{
		{name: "no server", mode: "no-server"},
		{name: "hard error", mode: "hard-error", err: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := &AgentTmuxManager{
				tmuxPath: tmux,
				env:      append(os.Environ(), "TMUX_LIST_MODE="+tt.mode),
				max:      1,
				conns:    map[string]*AgentTmuxConn{},
			}
			sessions, err := manager.listTmuxSessions()
			if tt.err {
				if err == nil {
					t.Fatal("listTmuxSessions error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("listTmuxSessions() error = %v", err)
			}
			if len(sessions) != 0 {
				t.Fatalf("sessions = %#v, want none", sessions)
			}
		})
	}
}

func TestAgentTmuxManagerKillWorkspaceSessionsAndShutdown(t *testing.T) {
	dir := t.TempDir()
	tmux := writeFakeTmux(t)
	sessionsPath := filepath.Join(dir, "sessions")
	killsPath := filepath.Join(dir, "kills")
	wsPrefix := workspace.ShortWorkspaceID("WORKSPACE-123")
	if err := os.WriteFile(sessionsPath, []byte(strings.Join([]string{
		"loom-" + wsPrefix + "-lead-nova-100\t100",
		"loom-" + wsPrefix + "-worker-orion-101\t101",
		"loom-other-worker-nova-999\t999",
	}, "\n")), 0600); err != nil {
		t.Fatalf("write sessions: %v", err)
	}
	closeFile, err := os.CreateTemp(dir, "pty")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	manager := &AgentTmuxManager{
		tmuxPath: tmux,
		env: append(os.Environ(),
			"TMUX_SESSIONS="+sessionsPath,
			"TMUX_KILLS="+killsPath,
		),
		max: 3,
		conns: map[string]*AgentTmuxConn{
			"attached": {
				ConnID:      "attached",
				SessionName: "loom-" + wsPrefix + "-worker-orion-101",
				PTY:         closeFile,
				killCh:      make(chan struct{}),
			},
			"other": {
				ConnID:      "other",
				SessionName: "loom-other-worker-nova-999",
				killCh:      make(chan struct{}),
			},
		},
	}

	if err := manager.KillWorkspaceSessions("WORKSPACE-123"); err != nil {
		t.Fatalf("KillWorkspaceSessions() error = %v", err)
	}
	if _, ok := manager.conns["attached"]; ok {
		t.Fatal("workspace attach was not detached")
	}
	if _, ok := manager.conns["other"]; !ok {
		t.Fatal("non-workspace attach was removed")
	}
	killedData, err := os.ReadFile(killsPath)
	if err != nil {
		t.Fatalf("read kills: %v", err)
	}
	killed := string(killedData)
	for _, want := range []string{
		"loom-" + wsPrefix + "-lead-nova-100",
		"loom-" + wsPrefix + "-worker-orion-101",
	} {
		if !strings.Contains(killed, want) {
			t.Fatalf("kills missing %q: %s", want, killed)
		}
	}
	if strings.Contains(killed, "loom-other-worker-nova-999") {
		t.Fatalf("kills included other workspace session: %s", killed)
	}
	if err := manager.KillWorkspaceSessions(""); err == nil {
		t.Fatal("empty workspace KillWorkspaceSessions error = nil")
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if manager.SessionCount() != 0 {
		t.Fatalf("SessionCount = %d, want 0", manager.SessionCount())
	}
}

func TestAgentTmuxManagerAttachResizeAndDetachBranches(t *testing.T) {
	tmux := writeFakeTmux(t)
	manager := &AgentTmuxManager{
		tmuxPath: tmux,
		env:      os.Environ(),
		max:      1,
		conns:    map[string]*AgentTmuxConn{},
	}

	if _, err := manager.AttachExistingRaw("../bad", 80, 24); err == nil {
		t.Fatal("invalid session attach error = nil")
	}
	if _, err := manager.AttachExistingRaw("missing", 80, 24); err == nil {
		t.Fatal("missing session attach error = nil")
	}

	conn, err := manager.AttachExistingRaw("exists", 0, 0)
	if err != nil {
		t.Fatalf("AttachExistingRaw() error = %v", err)
	}
	if conn.ConnID == "" || conn.SessionName != "exists" || conn.KillCh() == nil {
		t.Fatalf("conn = %#v", conn)
	}
	if manager.SessionCount() != 1 {
		t.Fatalf("SessionCount = %d, want 1", manager.SessionCount())
	}

	if _, err := manager.AttachExistingRaw("exists", 80, 24); err != ErrMaxSessionsReached {
		t.Fatalf("max attach err = %v, want ErrMaxSessionsReached", err)
	}
	if err := manager.Resize("missing", 80, 24); err == nil {
		t.Fatal("Resize missing error = nil")
	}
	if err := manager.Resize(conn.ConnID, 90, 30); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	if err := manager.Detach(conn.ConnID); err != nil {
		t.Fatalf("Detach() error = %v", err)
	}
	if manager.SessionCount() != 0 {
		t.Fatalf("SessionCount after detach = %d, want 0", manager.SessionCount())
	}
	if err := manager.Detach(conn.ConnID); err == nil {
		t.Fatal("second Detach error = nil")
	}
	if err := manager.Resize(conn.ConnID, 80, 24); err == nil {
		t.Fatal("Resize detached error = nil")
	}

	closedFile, err := os.CreateTemp(t.TempDir(), "pty")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	closedConn := &AgentTmuxConn{ConnID: "closed", SessionName: "exists", PTY: closedFile, killCh: make(chan struct{})}
	if err := closedConn.Close(); err != nil {
		t.Fatalf("close test conn: %v", err)
	}
	manager.conns["closed"] = closedConn
	if err := manager.Resize("closed", 80, 24); err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("Resize closed err = %v", err)
	}
}

func TestAgentTmuxConnCloseIsIdempotent(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "pty")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	conn := &AgentTmuxConn{PTY: file, killCh: make(chan struct{})}
	if conn.KillCh() == nil {
		t.Fatal("KillCh returned nil")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func writeFakeTmux(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	content := `#!/bin/sh
case "$1" in
  has-session)
    [ "$3" = "missing" ] && exit 1
    exit 0
    ;;
  list-panes)
    [ "$3" = "$TMUX_DEAD_SESSION" ] && echo 1 || echo 0
    exit 0
    ;;
  capture-pane)
    echo "line one"
    echo "line two"
    exit 0
    ;;
  list-sessions)
    case "$TMUX_LIST_MODE" in
      no-server) echo "failed to connect to server" >&2; exit 1 ;;
      hard-error) echo "permission denied" >&2; exit 2 ;;
    esac
    cat "$TMUX_SESSIONS"
    exit 0
    ;;
  kill-session)
    echo "$3" >> "$TMUX_KILLS"
    exit 0
    ;;
  set-option|resize-window)
    exit 0
    ;;
  attach-session)
    sleep 60
    ;;
esac
exit 0
`
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	return path
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
