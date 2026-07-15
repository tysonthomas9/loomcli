package terminal

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestTerminalHost(t *testing.T, max int) (*TerminalHostClient, context.CancelFunc) {
	t.Helper()
	socketDir, err := os.MkdirTemp("/tmp", "loom-termhost-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "host.sock")
	ctx, cancel := context.WithCancel(context.Background())
	server := NewTerminalHostServer(socket, "cat", max, nil)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	client := NewTerminalHostClient(socket, max)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := client.Ping(); err == nil {
			break
		}
		select {
		case err := <-done:
			cancel()
			if err != nil && strings.Contains(err.Error(), "operation not permitted") {
				t.Skipf("sandbox denied unix socket bind: %v", err)
			}
			t.Fatalf("terminal host Serve exited before ping: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("terminal host did not start")
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("terminal host Serve returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("terminal host did not stop")
		}
	})
	return client, cancel
}

func readAttachmentUntil(t *testing.T, att Attachment, want []byte) []byte {
	t.Helper()
	deadline := time.After(2 * time.Second)
	var got []byte
	for {
		select {
		case chunk, ok := <-att.Output():
			if !ok {
				t.Fatalf("attachment closed before output %q; got %q", want, got)
			}
			got = append(got, chunk...)
			if bytes.Contains(got, want) {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q; got %q", want, got)
		}
	}
}

func TestTerminalHostClient_AttachWriteDetachReattachKill(t *testing.T) {
	client, _ := startTestTerminalHost(t, 4)
	workspace := "ws1"
	if err := client.EnsureRegistered(workspace, t.TempDir()); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	key := SessionKey{Workspace: workspace, Name: "s1"}

	att, reattached, err := client.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if reattached {
		t.Fatal("first attach reattached=true, want false")
	}
	if _, err := att.WriteInput([]byte("hello-host\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	_ = readAttachmentUntil(t, att, []byte("hello-host"))
	if !client.HasSession(key) {
		t.Fatal("HasSession=false after attach")
	}
	if got := client.AttachmentCount(key); got != 1 {
		t.Fatalf("AttachmentCount=%d want 1", got)
	}
	client.Detach(key, att.ConnID())

	reatt, reattached, err := client.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("reattach AttachSession: %v", err)
	}
	if !reattached {
		t.Fatal("reattach returned reattached=false")
	}
	if !bytes.Contains(reatt.Scrollback(), []byte("hello-host")) {
		t.Fatalf("scrollback %q does not contain prior output", reatt.Scrollback())
	}
	if err := client.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-reatt.Output():
			if !ok {
				if got := reatt.ExitReason(); got != ExitReasonKilled {
					t.Fatalf("ExitReason=%q want %q", got, ExitReasonKilled)
				}
				if !client.SessionClosed(key) {
					t.Fatal("SessionClosed=false after Kill")
				}
				return
			}
		case <-deadline:
			t.Fatal("reattached output did not close after Kill")
		}
	}
}

func TestTerminalHostClient_EnsureSessionWriteToSession(t *testing.T) {
	client, _ := startTestTerminalHost(t, 4)
	workspace := "ws1"
	if err := client.EnsureRegistered(workspace, t.TempDir()); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	key := SessionKey{Workspace: workspace, Name: "setup"}
	created, err := client.EnsureSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if !created {
		t.Fatal("EnsureSession created=false, want true")
	}
	if err := client.WriteToSession(key, []byte("setup-line\n")); err != nil {
		t.Fatalf("WriteToSession: %v", err)
	}
	att, reattached, err := client.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if !reattached {
		t.Fatal("AttachSession after EnsureSession reattached=false")
	}
	if !bytes.Contains(att.Scrollback(), []byte("setup-line")) {
		t.Fatalf("scrollback %q does not contain setup write", att.Scrollback())
	}
}
