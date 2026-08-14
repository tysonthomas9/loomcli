package hooks

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/pty"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

func newPTYHookFixture(t *testing.T) (*PTYHook, *pty.Runtime) {
	t.Helper()
	multi := pty.NewRuntime("bash", 0)
	t.Cleanup(func() { _ = multi.Close() })
	return NewPTYHook(multi, slog.Default()), multi
}

func TestPTYHook_Name(t *testing.T) {
	hook, _ := newPTYHookFixture(t)
	if got := hook.Name(); got != "pty-manager" {
		t.Errorf("Name() = %q, want %q", got, "pty-manager")
	}
}

func TestPTYHook_Critical(t *testing.T) {
	hook, _ := newPTYHookFixture(t)
	if hook.Critical() {
		t.Error("Critical() = true, want false")
	}
}

func TestPTYHook_NilMulti_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic with nil multi, got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if !strings.Contains(msg, "must not be nil") {
			t.Errorf("panic message = %q, want containing 'must not be nil'", msg)
		}
	}()
	NewPTYHook(nil, slog.Default())
}

func TestPTYHook_DefaultLogger(t *testing.T) {
	multi := pty.NewRuntime("bash", 0)
	t.Cleanup(func() { _ = multi.Close() })
	hook := NewPTYHook(multi, nil)
	if hook.logger == nil {
		t.Error("expected default logger, got nil")
	}
}

func TestPTYHook_OnRegister_ValidPath(t *testing.T) {
	hook, multi := newPTYHookFixture(t)
	ws := "ws-valid"
	ctx := regCtx(ws, t.TempDir())

	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	// AttachSession should not fail with ErrWorkspaceNotRegistered.
	// We can't spawn an actual shell reliably in unit tests, so assert the
	// workspace entry exists via AttachSession's error classification: if the
	// workspace is registered, AttachSession reaches managerForWS and then
	// PTYManager.AttachSession. Instead, use the in-package test helper
	// hasManager to confirm the entry exists without spawning a shell.
	_, _, err := multi.Attach(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("AttachSession after register returned ErrWorkspaceNotRegistered: %v", err)
	}
	// Kill any session we may have created so cleanup Close() is clean.
	_ = multi.Kill(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"})
}

func TestPTYHook_OnRegister_InvalidPath_NonexistentDir(t *testing.T) {
	hook, multi := newPTYHookFixture(t)
	ws := "ws-bad"
	ctx := regCtx(ws, "/this/path/definitely/does/not/exist")

	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister should swallow invalid-path errors, got: %v", err)
	}

	_, _, err := multi.Attach(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if !errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("AttachSession for invalid-path workspace = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestPTYHook_OnRegister_InvalidPath_IsFile(t *testing.T) {
	hook, multi := newPTYHookFixture(t)
	ws := "ws-file"
	tmpFile := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(tmpFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := regCtx(ws, tmpFile)

	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister should swallow invalid-path errors, got: %v", err)
	}

	_, _, err := multi.Attach(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if !errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("AttachSession for file-path workspace = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestPTYHook_OnRegister_DoesNotProvide(t *testing.T) {
	hook, _ := newPTYHookFixture(t)
	ctx := regCtx("ws-1", t.TempDir())

	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	if res := ctx.Resources(); len(res) != 0 {
		t.Errorf("expected no resources in bag, got %d keys: %v", len(res), res)
	}
}

func TestPTYHook_OnRegister_MultiPTYManagerClosed(t *testing.T) {
	multi := pty.NewRuntime("bash", 0)
	_ = multi.Close()
	hook := NewPTYHook(multi, slog.Default())
	ctx := regCtx("ws-1", t.TempDir())

	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister on closed manager should swallow, got: %v", err)
	}
}

func TestPTYHook_OnDeregister_RemovesEntry(t *testing.T) {
	hook, multi := newPTYHookFixture(t)
	ws := "ws-dereg"
	ctx := regCtx(ws, t.TempDir())

	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}
	hook.OnDeregister(deregCtx(ws))

	_, _, err := multi.Attach(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if !errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("AttachSession after deregister = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestPTYHook_OnDeregister_UnknownWorkspace_NoOp(t *testing.T) {
	hook, _ := newPTYHookFixture(t)
	// Should not panic or error.
	hook.OnDeregister(deregCtx("never-registered"))
}

func TestPTYHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook, multi := newPTYHookFixture(t)
	ws := "ws-rb"
	ctx := regCtx(ws, t.TempDir())

	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}
	hook.OnRollback(deregCtx(ws))

	_, _, err := multi.Attach(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if !errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("AttachSession after rollback = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestPTYHook_IntegrationWithRegistry(t *testing.T) {
	multi := pty.NewRuntime("bash", 0)
	t.Cleanup(func() { _ = multi.Close() })

	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	if err := registry.AddHook(NewPTYHook(multi, slog.Default())); err != nil {
		t.Fatalf("AddHook: %v", err)
	}

	ws := "ws-integ"
	path := t.TempDir()
	if err := registry.Register(ws, path); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	_, _, err := multi.Attach(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("post-Register AttachSession returned ErrWorkspaceNotRegistered: %v", err)
	}
	_ = multi.Kill(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t"})

	registry.Deregister(ws)

	_, _, err = multi.Attach(interaction.TerminalKey{WorkspaceKey: ws, TerminalID: "t2"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if !errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("post-Deregister AttachSession = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestPTYHook_IntegrationWithRegistry_InvalidPath(t *testing.T) {
	multi := pty.NewRuntime("bash", 0)
	t.Cleanup(func() { _ = multi.Close() })

	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	if err := registry.AddHook(NewPTYHook(multi, slog.Default())); err != nil {
		t.Fatalf("AddHook: %v", err)
	}

	// PTYHook is non-critical, so registry.Register must succeed even when the
	// hook's Register internally fails on a bad path.
	if err := registry.Register("ws-bad", "/no/such/dir"); err != nil {
		t.Fatalf("registry.Register should succeed for non-critical hook failure, got: %v", err)
	}

	_, _, err := multi.Attach(interaction.TerminalKey{WorkspaceKey: "ws-bad", TerminalID: "t"}, 80, 24, &interaction.LaunchSpec{Argv: []string{"/bin/true"}})
	if !errors.Is(err, interaction.ErrTerminalPlacement) {
		t.Fatalf("AttachSession on downgraded workspace = %v, want ErrWorkspaceNotRegistered", err)
	}
}
