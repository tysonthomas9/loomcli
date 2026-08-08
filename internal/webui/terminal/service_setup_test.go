package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
)

type setupEnsureCall struct {
	key  SessionKey
	argv []string
}

type setupWriteCall struct {
	key  SessionKey
	data string
}

type fakeSetupPTYSource struct {
	alive       map[SessionKey]bool
	created     bool
	ensureCalls []setupEnsureCall
	writeCalls  []setupWriteCall
}

func newFakeSetupPTYSource(created bool) *fakeSetupPTYSource {
	return &fakeSetupPTYSource{alive: map[SessionKey]bool{}, created: created}
}

func (f *fakeSetupPTYSource) AttachSession(_ SessionKey, _, _ uint16, _ *LaunchSpec) (Attachment, bool, error) {
	panic("AttachSession not used in setup service tests")
}
func (f *fakeSetupPTYSource) Detach(_ SessionKey, _ string) {}
func (f *fakeSetupPTYSource) Kill(key SessionKey) error {
	delete(f.alive, key)
	return nil
}
func (f *fakeSetupPTYSource) HasSession(key SessionKey) bool  { return f.alive[key] }
func (f *fakeSetupPTYSource) SessionClosed(_ SessionKey) bool { return false }
func (f *fakeSetupPTYSource) AttachmentCount(key SessionKey) int {
	if f.alive[key] {
		return 1
	}
	return 0
}
func (f *fakeSetupPTYSource) SessionCount() int            { return len(f.alive) }
func (f *fakeSetupPTYSource) SessionCountFor(_ string) int { return len(f.alive) }
func (f *fakeSetupPTYSource) MaxSessions() int             { return 100 }
func (f *fakeSetupPTYSource) EnsureSession(key SessionKey, _ uint16, _ uint16, argv []string) (bool, error) {
	f.ensureCalls = append(f.ensureCalls, setupEnsureCall{key: key, argv: argv})
	f.alive[key] = true
	return f.created, nil
}
func (f *fakeSetupPTYSource) WriteToSession(key SessionKey, p []byte) error {
	f.writeCalls = append(f.writeCalls, setupWriteCall{key: key, data: string(p)})
	return nil
}

func newSetupTestSvc(t *testing.T, fake *fakeSetupPTYSource) (*terminalServiceImpl, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &terminalServiceImpl{
		tabStore:    localredis.NewTabMetadataStore(rdb, nil),
		redisClient: rdb,
		ptyMgr:      fake,
	}, mr
}

func TestStartSetupCreatesTabAndStartsSetupSession(t *testing.T) {
	fake := newFakeSetupPTYSource(true)
	svc, mr := newSetupTestSvc(t, fake)

	result, err := svc.StartSetup(context.Background(), "HELLO-WORLD", TerminalSetupRequest{
		Backend: "codex",
		Action:  "install",
	})
	if err != nil {
		t.Fatalf("StartSetup: %v", err)
	}

	wantSession := "HELLO-WORLD--lead-shell-setup-codex"
	if result.SessionName != wantSession {
		t.Fatalf("SessionName = %q, want %q", result.SessionName, wantSession)
	}
	if result.Command != "npm install -g @openai/codex" {
		t.Fatalf("Command = %q", result.Command)
	}
	if len(fake.ensureCalls) != 1 {
		t.Fatalf("EnsureSession calls = %d, want 1", len(fake.ensureCalls))
	}
	if fake.ensureCalls[0].key != (SessionKey{Workspace: "HELLO-WORLD", Name: wantSession}) {
		t.Fatalf("EnsureSession key = %+v", fake.ensureCalls[0].key)
	}
	if got := strings.Join(fake.ensureCalls[0].argv, "\n"); !strings.Contains(got, result.Command) || !strings.Contains(got, "exec \"${SHELL:-/bin/sh}\" -l") {
		t.Fatalf("setup argv did not include command and final shell: %q", got)
	}
	if len(fake.writeCalls) != 0 {
		t.Fatalf("new setup session should not receive duplicate WriteToSession, got %d", len(fake.writeCalls))
	}

	meta, err := svc.tabStore.Get(context.Background(), "HELLO-WORLD", wantSession)
	if err != nil {
		t.Fatalf("Get setup tab: %v", err)
	}
	if meta == nil || meta.Label != "Codex setup" {
		t.Fatalf("setup tab metadata = %+v", meta)
	}
	if got := mr.HGet(terminalUIStateKey("HELLO-WORLD"), "active_tab"); got != wantSession {
		t.Fatalf("active_tab = %q, want %q", got, wantSession)
	}
}

func TestStartSetupWritesCommandIntoExistingSetupSession(t *testing.T) {
	fake := newFakeSetupPTYSource(false)
	svc, _ := newSetupTestSvc(t, fake)

	result, err := svc.StartSetup(context.Background(), "HELLO-WORLD", TerminalSetupRequest{
		Backend: "codex",
		Action:  "login",
	})
	if err != nil {
		t.Fatalf("StartSetup: %v", err)
	}

	if len(fake.ensureCalls) != 1 {
		t.Fatalf("EnsureSession calls = %d, want 1", len(fake.ensureCalls))
	}
	if len(fake.writeCalls) != 1 {
		t.Fatalf("WriteToSession calls = %d, want 1", len(fake.writeCalls))
	}
	if fake.writeCalls[0].data != "codex login\n" {
		t.Fatalf("WriteToSession data = %q", fake.writeCalls[0].data)
	}
	if result.Created {
		t.Fatal("Created = true, want false for existing session")
	}
}

func TestStartSetupSupportsCursorInstaller(t *testing.T) {
	fake := newFakeSetupPTYSource(true)
	svc, _ := newSetupTestSvc(t, fake)

	result, err := svc.StartSetup(context.Background(), "HELLO-WORLD", TerminalSetupRequest{
		Backend: "cursor",
		Action:  "install",
	})
	if err != nil {
		t.Fatalf("StartSetup: %v", err)
	}

	if result.SessionName != "HELLO-WORLD--lead-shell-setup-cursor" {
		t.Fatalf("SessionName = %q", result.SessionName)
	}
	if result.Command != "curl https://cursor.com/install -fsS | bash" {
		t.Fatalf("Command = %q", result.Command)
	}
	if result.Manual {
		t.Fatal("Manual = true, want false for Cursor installer")
	}
	if result.Title != "Install Cursor" {
		t.Fatalf("Title = %q", result.Title)
	}
	if !strings.Contains(result.Message, "backend started this command") {
		t.Fatalf("Message = %q", result.Message)
	}
}

func TestStartSetupSupportsCursorCredentialGuidance(t *testing.T) {
	fake := newFakeSetupPTYSource(true)
	svc, _ := newSetupTestSvc(t, fake)

	result, err := svc.StartSetup(context.Background(), "HELLO-WORLD", TerminalSetupRequest{
		Backend: "cursor",
		Action:  "login",
	})
	if err != nil {
		t.Fatalf("StartSetup: %v", err)
	}

	if !strings.Contains(result.Command, "CURSOR_API_KEY") {
		t.Fatalf("Command = %q", result.Command)
	}
	if !result.Manual {
		t.Fatal("Manual = false, want true for Cursor credential guidance")
	}
	if result.Title != "Configure Cursor credentials" {
		t.Fatalf("Title = %q", result.Title)
	}
	if !strings.Contains(result.Message, "detects Cursor credentials") {
		t.Fatalf("Message = %q", result.Message)
	}
}

// P2.3 — setupShellArgv shellQuotes the printf argument but pastes the body
// line raw. Safe today (only internal map values flow in), but creates an
// asymmetry: a future entry with a single-quote will print fine via printf
// but the unquoted body line will break shell parsing or behave differently.
//
// This test exercises a hypothetical command containing a single quote and
// inspects the generated script to confirm the asymmetry exists.
func TestSetupShellArgvHasAsymmetricQuotingBetweenPrintfAndBody(t *testing.T) {
	cmd := `echo "it's set"`

	argv := setupShellArgv(cmd)
	if len(argv) != 2 || argv[0] != "-lc" {
		t.Fatalf("argv = %v", argv)
	}
	script := argv[1]

	// Printf arg is `shellQuote`'d, so the quote becomes '\''.
	wantQuotedSegment := shellQuote(cmd)
	if !strings.Contains(script, wantQuotedSegment) {
		t.Fatalf("printf line missing quoted command:\n%s\nexpected substring: %s", script, wantQuotedSegment)
	}

	// Body line: command pasted raw. Look for the raw line (no surrounding
	// single-quote escaping) somewhere in the script.
	rawLine := "\n" + cmd + "\n"
	if !strings.Contains(script, rawLine) {
		t.Fatalf("expected raw command line in body:\n%s\nlooking for: %q", script, rawLine)
	}

	// The asymmetry itself: the printf-quoted form and the body-raw form
	// are not the same. Confirms that if the input contained shell-special
	// characters, the two locations would diverge.
	if strings.Contains(rawLine, "'\\''") {
		t.Fatal("raw body line is shell-escaped — asymmetry already removed")
	}
	if !strings.Contains(wantQuotedSegment, "'\\''") {
		t.Fatal("printf quoted form lost escaping — test setup is wrong")
	}
	t.Logf("ASYMMETRY CONFIRMED: printf quoted=%q vs body raw=%q", wantQuotedSegment, rawLine)
}

// Also verify the WriteToSession path: when the session exists, the service
// writes `command+"\n"` directly. Same asymmetry — no shell quoting.
func TestStartSetupWritesRawCommandToExistingSessionWithoutQuoting(t *testing.T) {
	// Swap in a temporary setupCommandSpecs entry that contains a single
	// quote. Restore on cleanup so other tests aren't affected.
	original := setupCommandSpecs
	t.Cleanup(func() { setupCommandSpecs = original })
	setupCommandSpecs = map[string]setupCommandSpec{
		"unsafe": {
			displayName: "Unsafe",
			commands:    map[string]string{"login": `echo "it's set"`},
		},
	}

	fake := newFakeSetupPTYSource(false) // not created => triggers WriteToSession
	svc, _ := newSetupTestSvc(t, fake)

	_, err := svc.StartSetup(context.Background(), "HELLO-WORLD", TerminalSetupRequest{
		Backend: "unsafe",
		Action:  "login",
	})
	if err != nil {
		t.Fatalf("StartSetup: %v", err)
	}

	if len(fake.writeCalls) != 1 {
		t.Fatalf("WriteToSession calls = %d, want 1", len(fake.writeCalls))
	}
	got := fake.writeCalls[0].data
	want := `echo "it's set"` + "\n"
	if got != want {
		t.Fatalf("WriteToSession data = %q, want raw %q", got, want)
	}
	// Bug-shape confirmed: the WriteToSession path does NOT shell-quote
	// the command, in contrast to setupShellArgv's printf argument.
}

func TestStartSetupRejectsUnsupportedBackend(t *testing.T) {
	fake := newFakeSetupPTYSource(true)
	svc, _ := newSetupTestSvc(t, fake)

	_, err := svc.StartSetup(context.Background(), "HELLO-WORLD", TerminalSetupRequest{
		Backend: "unknown",
		Action:  "install",
	})
	if err == nil {
		t.Fatal("StartSetup unexpectedly succeeded")
	}
	if len(fake.ensureCalls) != 0 {
		t.Fatalf("EnsureSession should not be called for invalid backend, got %d", len(fake.ensureCalls))
	}
}
