//go:build darwin || linux

package browserauth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// shortDataDir keeps the socket path under the darwin sun_path limit;
// t.TempDir() paths include the test name and can exceed it.
func shortDataDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lbs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startSocket(t *testing.T, validate WorkspaceValidator) (*OperatorSocketServer, *OperatorSessionRegistry, string, *syncBuffer) {
	t.Helper()
	path := OperatorSocketPath(shortDataDir(t))
	reg := NewOperatorSessionRegistry()
	logs := &syncBuffer{}
	srv, err := ListenOperatorSocket(path, reg, validate, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, reg, path, logs
}

func TestOperatorSocketIssueRefreshRevoke(t *testing.T) {
	_, reg, path, logs := startSocket(t, func(_ context.Context, ws string) error {
		if ws != "ws" {
			return errors.New("no such workspace")
		}
		return nil
	})
	ctx := context.Background()

	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v %v", fi.Mode(), err)
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir mode = %o", di.Mode().Perm())
	}

	bad, err := CallOperatorSocket(ctx, path, SocketRequest{Op: OpIssue, Workspace: "missing"})
	if err != nil || bad.OK || bad.Code != "workspace_invalid" {
		t.Fatalf("unknown workspace = %+v %v", bad, err)
	}

	issued, err := CallOperatorSocket(ctx, path, SocketRequest{Op: OpIssue, Workspace: "ws"})
	if err != nil || !issued.OK || issued.Token == "" || !strings.HasPrefix(issued.SessionID, "los_") {
		t.Fatalf("issue = %+v %v", issued, err)
	}
	sess, err := reg.Validate(issued.Token, "ws")
	if err != nil || sess.UID != uint32(os.Getuid()) { //nolint:gosec
		t.Fatalf("issued session = %+v %v", sess, err)
	}

	refreshed, err := CallOperatorSocket(ctx, path, SocketRequest{Op: OpRefresh, Token: issued.Token})
	if err != nil || !refreshed.OK || refreshed.Token != "" {
		t.Fatalf("refresh = %+v %v (refresh must not echo the bearer)", refreshed, err)
	}
	if _, err := CallOperatorSocket(ctx, path, SocketRequest{Op: OpRevoke, Token: issued.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Validate(issued.Token, "ws"); err == nil {
		t.Fatal("revoked session still valid")
	}
	gone, _ := CallOperatorSocket(ctx, path, SocketRequest{Op: OpRefresh, Token: issued.Token})
	if gone.OK || gone.Code != "session_inactive" {
		t.Fatalf("refresh after revoke = %+v", gone)
	}

	if strings.Contains(logs.String(), issued.Token) {
		t.Fatal("bearer written to logs")
	}
}

func TestOperatorSocketRejectsForeignPeer(t *testing.T) {
	orig := peerUIDLookup
	peerUIDLookup = func(net.Conn) (uint32, error) { return uint32(os.Getuid()) + 1, nil } //nolint:gosec
	t.Cleanup(func() { peerUIDLookup = orig })
	_, reg, path, _ := startSocket(t, nil)
	resp, err := CallOperatorSocket(context.Background(), path, SocketRequest{Op: OpIssue, Workspace: "ws"})
	if err != nil || resp.OK || resp.Code != "peer_rejected" || resp.Token != "" {
		t.Fatalf("foreign peer = %+v %v", resp, err)
	}
	if reg.Len() != 0 {
		t.Fatal("foreign peer created a session")
	}
}

func TestOperatorSocketRestartInvalidatesSessions(t *testing.T) {
	srv, reg, path, _ := startSocket(t, nil)
	issued, err := CallOperatorSocket(context.Background(), path, SocketRequest{Op: OpIssue, Workspace: "ws"})
	if err != nil || !issued.OK {
		t.Fatal(issued, err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Validate(issued.Token, "ws"); err == nil {
		t.Fatal("session survived shutdown")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket not removed: %v", err)
	}
	// A new runtime (fresh registry) listens on the same path; the old bearer is unknown.
	reg2 := NewOperatorSessionRegistry()
	srv2, err := ListenOperatorSocket(path, reg2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Close()
	resp, _ := CallOperatorSocket(context.Background(), path, SocketRequest{Op: OpRefresh, Token: issued.Token})
	if resp.OK {
		t.Fatal("old bearer accepted after restart")
	}
}

func TestOperatorSocketFailsClosed(t *testing.T) {
	long := "/tmp/" + strings.Repeat("x", 120) + "/browser-session.sock"
	if _, err := ListenOperatorSocket(long, NewOperatorSessionRegistry(), nil, nil); err == nil {
		t.Fatal("over-long socket path accepted")
	}
	// A regular file squatting on the path is never deleted.
	dir := shortDataDir(t)
	path := OperatorSocketPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenOperatorSocket(path, NewOperatorSessionRegistry(), nil, nil); err == nil {
		t.Fatal("listener replaced a regular file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("regular file removed")
	}
	// The client refuses a socket dir readable by others.
	_, _, spath, _ := startSocket(t, nil)
	if err := os.Chmod(filepath.Dir(spath), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CallOperatorSocket(context.Background(), spath, SocketRequest{Op: OpIssue, Workspace: "ws"}); err == nil {
		t.Fatal("client dialed a non-private socket dir")
	}
}

// A second runtime must not delete and take over a socket that a live runtime
// is still serving, and a closing runtime must not remove a socket it no
// longer owns.
func TestOperatorSocketNeverTakesOverALiveSocket(t *testing.T) {
	_, _, path, _ := startSocket(t, nil)
	if _, err := ListenOperatorSocket(path, NewOperatorSessionRegistry(), nil, nil); err == nil {
		t.Fatal("second runtime took over a live socket")
	}
	resp, err := CallOperatorSocket(context.Background(), path, SocketRequest{Op: OpIssue, Workspace: "ws"})
	if err != nil || !resp.OK {
		t.Fatalf("original socket broken after refused takeover: %v %v", resp, err)
	}
}

func TestOperatorSocketCloseSparesAReplacementSocket(t *testing.T) {
	srv, _, path, _ := startSocket(t, nil)
	// Simulate another runtime having replaced the file at path.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	other, err := ListenOperatorSocket(path, NewOperatorSessionRegistry(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("closing the old runtime removed the replacement socket: %v", err)
	}
	if resp, err := CallOperatorSocket(context.Background(), path, SocketRequest{Op: OpIssue, Workspace: "ws"}); err != nil || !resp.OK {
		t.Fatalf("replacement socket broken: %v %v", resp, err)
	}
}
