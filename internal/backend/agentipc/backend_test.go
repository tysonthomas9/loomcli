package agentipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// shortSocketPath returns a socket path short enough for macOS's 104-byte limit.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ipc")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// startTestServer creates a Unix socket server that accepts connections, reads
// one ipcRequest JSON line per connection, calls the handler, writes one
// ipcResponse JSON line, and closes the connection. Returns the socket path.
func startTestServer(t *testing.T, handler func(ipcRequest) ipcResponse) string {
	t.Helper()
	sockPath := shortSocketPath(t)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				scanner := bufio.NewScanner(c)
				if !scanner.Scan() {
					return
				}
				var req ipcRequest
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}
				resp := handler(req)
				data, _ := json.Marshal(resp)
				data = append(data, '\n')
				_, _ = c.Write(data)
			}(conn)
		}
	}()

	return sockPath
}

// --- IPC operation tests ---

func TestBackend_ClaimIssue_Success(t *testing.T) {
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Success: true}
	})
	b := New(sock, "test-agent")
	err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBackend_ClaimIssue_Conflict(t *testing.T) {
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Error: "already claimed", Kind: "conflict"}
	})
	b := New(sock, "test-agent")
	err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("expected KindConflict, got %v", err)
	}
}

func TestBackend_ClaimIssue_NotFound(t *testing.T) {
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Error: "issue not found", Kind: "not_found"}
	})
	b := New(sock, "test-agent")
	err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("expected KindNotFound, got %v", err)
	}
}

func TestBackend_ClaimIssue_LockTTL(t *testing.T) {
	var gotArgs json.RawMessage
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		gotArgs = req.Args
		return ipcResponse{Success: true}
	})
	b := New(sock, "test-agent")

	// With TTL > 0: should include lock_ttl_seconds
	err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123", LockTTL: 5 * time.Minute})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed struct {
		LockTTLSeconds int `json:"lock_ttl_seconds"`
	}
	if err := json.Unmarshal(gotArgs, &parsed); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if parsed.LockTTLSeconds != 300 {
		t.Fatalf("expected lock_ttl_seconds=300, got %d", parsed.LockTTLSeconds)
	}

	// With TTL == 0: Args should be nil
	gotArgs = nil
	err = b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotArgs != nil {
		t.Fatalf("expected nil Args for lockTTL=0, got %s", gotArgs)
	}
}

func TestBackend_ClaimIssue_OwnerActor(t *testing.T) {
	var gotArgs json.RawMessage
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		gotArgs = req.Args
		return ipcResponse{Success: true}
	})
	b := New(sock, "falcon")

	if err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123", OwnerActor: " falcon "}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed struct {
		OwnerActor string `json:"owner_actor"`
	}
	if err := json.Unmarshal(gotArgs, &parsed); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if parsed.OwnerActor != "falcon" {
		t.Fatalf("owner_actor = %q, want falcon", parsed.OwnerActor)
	}
}

func TestBackend_ClaimIssue_RejectsMismatchedOwnerActor(t *testing.T) {
	b := New("/tmp/unreachable.sock", "falcon")
	err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123", OwnerActor: "nova"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestBackend_Update_Success(t *testing.T) {
	var gotReq ipcRequest
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		gotReq = req
		return ipcResponse{Success: true}
	})
	b := New(sock, "test-agent")

	status := "in_progress"
	err := b.Update(context.Background(), "abc-123", backend.UpdateParams{Status: &status})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotReq.Operation != "update" {
		t.Fatalf("expected operation=update, got %s", gotReq.Operation)
	}

	var params backend.UpdateParams
	if err := json.Unmarshal(gotReq.Args, &params); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if params.Status == nil || *params.Status != "in_progress" {
		t.Fatalf("expected Status=in_progress, got %v", params.Status)
	}
}

func TestBackend_Update_NotFound(t *testing.T) {
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Error: "issue not found", Kind: "not_found"}
	})
	b := New(sock, "test-agent")
	err := b.Update(context.Background(), "abc-123", backend.UpdateParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("expected KindNotFound, got %v", err)
	}
}

func TestBackend_Close_Success(t *testing.T) {
	closeResult := backend.CloseResult{
		Closed:    &backend.IssueData{ID: "abc-123", Title: "Test"},
		Unblocked: []backend.IssueData{{ID: "abc-124", Title: "Next"}},
	}
	resultData, _ := json.Marshal(closeResult)

	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Success: true, Data: resultData}
	})
	b := New(sock, "test-agent")

	result, err := b.Close(context.Background(), "abc-123", backend.CloseParams{Reason: "done"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Closed == nil || result.Closed.ID != "abc-123" {
		t.Fatalf("expected closed issue abc-123, got %v", result.Closed)
	}
	if len(result.Unblocked) != 1 || result.Unblocked[0].ID != "abc-124" {
		t.Fatalf("expected 1 unblocked issue abc-124, got %v", result.Unblocked)
	}
}

func TestBackend_Close_NotFound(t *testing.T) {
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Error: "issue not found", Kind: "not_found"}
	})
	b := New(sock, "test-agent")
	result, err := b.Close(context.Background(), "abc-123", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("expected KindNotFound, got %v", err)
	}
}

func TestBackend_Close_MalformedData(t *testing.T) {
	// Data is valid JSON but cannot be unmarshaled into CloseResult (a string, not an object)
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Success: true, Data: json.RawMessage(`"just a string"`)}
	})
	b := New(sock, "test-agent")
	result, err := b.Close(context.Background(), "abc-123", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error for malformed data")
	}
	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Fatalf("expected KindInternal, got %v", err)
	}
}

// --- Transport error tests ---

func TestBackend_TransportError_NoSocket(t *testing.T) {
	b := New("/nonexistent/path/s.sock", "test-agent")
	err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
}

func TestBackend_TransportError_ServerHangs(t *testing.T) {
	sockPath := shortSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	// Server accepts but never writes a response
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Read the request but don't respond — hold the connection
			scanner := bufio.NewScanner(conn)
			scanner.Scan()
			// Don't write, don't close — let the client timeout
			// (defer close via cleanup)
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	// Use short timeouts for test speed
	resp, err := sendIPC(sockPath, ipcRequest{Operation: opClaim, AgentName: "test", IssueID: "x"}, 2*time.Second, 500*time.Millisecond)
	if resp != nil {
		t.Fatalf("expected nil response, got %v", resp)
	}
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
}

func TestBackend_TransportError_GarbageResponse(t *testing.T) {
	sockPath := shortSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				scanner := bufio.NewScanner(c)
				scanner.Scan()
				_, _ = c.Write([]byte("this is not json\n"))
			}(conn)
		}
	}()

	b := New(sockPath, "test-agent")
	err = b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
}

func TestBackend_TransportError_ConnectionReset(t *testing.T) {
	sockPath := shortSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	// Server accepts and immediately closes
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	b := New(sockPath, "test-agent")
	err = b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
}

// --- Not-implemented tests ---

func TestBackend_NotImplemented_Queries(t *testing.T) {
	b := New("/dummy/s.sock", "test-agent")
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Get", func() error { _, err := b.Get(ctx, "x"); return err }},
		{"List", func() error { _, err := b.List(ctx, backend.ListOpts{}); return err }},
		{"Ready", func() error { _, err := b.Ready(ctx, backend.ReadyOpts{}); return err }},
		{"Blocked", func() error { _, err := b.Blocked(ctx, backend.BlockedOpts{}); return err }},
		{"Stats", func() error { _, err := b.Stats(ctx); return err }},
		{"Count", func() error { _, err := b.Count(ctx, backend.CountOpts{}); return err }},
		{"GetChildren", func() error { _, err := b.GetChildren(ctx, "x"); return err }},
		{"SearchIssues", func() error { _, err := b.SearchIssues(ctx, "q", 0); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error")
			}
			if !backend.IsKind(err, backend.KindNotImplemented) {
				t.Fatalf("expected KindNotImplemented, got %v", err)
			}
		})
	}
}

func TestBackend_NotImplemented_Mutations(t *testing.T) {
	b := New("/dummy/s.sock", "test-agent")
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Create", func() error { _, err := b.Create(ctx, backend.CreateParams{}); return err }},
		{"Reopen", func() error { return b.Reopen(ctx, "x", backend.ReopenParams{}) }},
		{"Delete", func() error { return b.Delete(ctx, backend.DeleteParams{}) }},
		{"DeferIssue", func() error { return b.DeferIssue(ctx, "x", time.Time{}) }},
		{"UndeferIssue", func() error { return b.UndeferIssue(ctx, "x") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error")
			}
			if !backend.IsKind(err, backend.KindNotImplemented) {
				t.Fatalf("expected KindNotImplemented, got %v", err)
			}
		})
	}
}

func TestBackend_NotImplemented_Other(t *testing.T) {
	b := New("/dummy/s.sock", "test-agent")
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"AddDependency", func() error { return b.AddDependency(ctx, backend.DepAddParams{}) }},
		{"RemoveDependency", func() error { return b.RemoveDependency(ctx, backend.DepRemoveParams{}) }},
		{"AddLabel", func() error { return b.AddLabel(ctx, "x", "l") }},
		{"RemoveLabel", func() error { return b.RemoveLabel(ctx, "x", "l") }},
		{"ListComments", func() error { _, err := b.ListComments(ctx, "x"); return err }},
		{"AddComment", func() error { _, err := b.AddComment(ctx, backend.CommentAddParams{}); return err }},
		{"ListEvents", func() error { _, err := b.ListEvents(ctx, "x", 10); return err }},
		{"Batch", func() error { _, err := b.Batch(ctx, nil); return err }},
		{"GetMutations", func() error { _, err := b.GetMutations(ctx, 0); return err }},
		{"WaitForMutations", func() error { _, err := b.WaitForMutations(ctx, 0, 0); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error")
			}
			if !backend.IsKind(err, backend.KindNotImplemented) {
				t.Fatalf("expected KindNotImplemented, got %v", err)
			}
		})
	}
}

// --- Metadata tests ---

func TestBackend_BackendName(t *testing.T) {
	b := New("/dummy/s.sock", "test-agent")
	if got := b.BackendName(); got != "agent-ipc" {
		t.Fatalf("expected agent-ipc, got %s", got)
	}
}

func TestBackend_RequestFields(t *testing.T) {
	var gotReqs []ipcRequest
	var mu sync.Mutex
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		mu.Lock()
		gotReqs = append(gotReqs, req)
		mu.Unlock()
		// Return success with data for Close
		if req.Operation == opComplete {
			data, _ := json.Marshal(backend.CloseResult{})
			return ipcResponse{Success: true, Data: data}
		}
		return ipcResponse{Success: true}
	})
	b := New(sock, "my-agent")
	ctx := context.Background()

	_ = b.ClaimIssue(ctx, backend.ClaimIssueParams{ID: "issue-1"})
	_ = b.Update(ctx, "issue-2", backend.UpdateParams{})
	_, _ = b.Close(ctx, "issue-3", backend.CloseParams{})

	mu.Lock()
	defer mu.Unlock()

	expected := []struct {
		op      string
		agent   string
		issueID string
	}{
		{opClaim, "my-agent", "issue-1"},
		{opUpdate, "my-agent", "issue-2"},
		{opComplete, "my-agent", "issue-3"},
	}
	if len(gotReqs) != len(expected) {
		t.Fatalf("expected %d requests, got %d", len(expected), len(gotReqs))
	}
	for i, exp := range expected {
		r := gotReqs[i]
		if r.Operation != exp.op {
			t.Errorf("[%d] operation: want %s, got %s", i, exp.op, r.Operation)
		}
		if r.AgentName != exp.agent {
			t.Errorf("[%d] agent_name: want %s, got %s", i, exp.agent, r.AgentName)
		}
		if r.IssueID != exp.issueID {
			t.Errorf("[%d] issue_id: want %s, got %s", i, exp.issueID, r.IssueID)
		}
	}
}

// --- Concurrency test ---

func TestBackend_ConcurrentCalls(t *testing.T) {
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Success: true}
	})
	b := New(sock, "test-agent")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
		}()
	}
	wg.Wait()
}

// --- Constructor panic test ---

func TestBackend_New_PanicsOnEmptySocket(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty socketPath")
		}
	}()
	New("", "test-agent")
}

// --- Server error without Kind ---

func TestBackend_ServerErrorWithoutKind(t *testing.T) {
	sock := startTestServer(t, func(req ipcRequest) ipcResponse {
		return ipcResponse{Error: "unknown operation: \"foo\""}
	})
	b := New(sock, "test-agent")
	err := b.ClaimIssue(context.Background(), backend.ClaimIssueParams{ID: "abc-123"})
	if err == nil {
		t.Fatal("expected error")
	}
	// Should be KindInternal since Kind is empty
	if !backend.IsKind(err, backend.KindInternal) {
		t.Fatalf("expected KindInternal, got %v", err)
	}
}

// Ensure macOS socket path isn't too long in our test helper
func TestBackend_SocketPathNotTooLong(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	if len(sock) > 104 {
		t.Skipf("socket path too long for macOS: %d bytes", len(sock))
	}
	// Verify we can listen on it
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close()
	os.Remove(sock)
}
