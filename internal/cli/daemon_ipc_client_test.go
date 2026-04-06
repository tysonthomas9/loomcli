//go:build ignore

package cli

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// startTestIPCServer starts a minimal Unix socket server that accepts
// connections, reads one AgentIPCRequest per connection, calls the handler,
// writes the AgentIPCResponse, and closes. Runs in a goroutine; cleaned up
// via t.Cleanup.
func startTestIPCServer(t *testing.T, socketPath string, handler func(AgentIPCRequest) AgentIPCResponse) {
	t.Helper()

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()

				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				scanner := bufio.NewScanner(c)
				if !scanner.Scan() {
					return
				}

				var req AgentIPCRequest
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
}

func TestAgentIPCClient_Claim_Success(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		return AgentIPCResponse{Success: true}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	if err := client.Claim("abc-123", 0); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
}

func TestAgentIPCClient_Claim_Conflict(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		return AgentIPCResponse{Error: "already claimed by nova", Kind: string(backend.KindConflict)}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	err := client.Claim("abc-123", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Errorf("expected KindConflict, got: %v", err)
	}
}

func TestAgentIPCClient_Claim_NotFound(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		return AgentIPCResponse{Error: "issue not found", Kind: string(backend.KindNotFound)}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	err := client.Claim("abc-123", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound, got: %v", err)
	}
}

func TestAgentIPCClient_Claim_WithLockTTL(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	var capturedReq AgentIPCRequest
	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		capturedReq = req
		return AgentIPCResponse{Success: true}
	})

	client := NewAgentIPCClient(socketPath, "falcon")

	t.Run("with TTL", func(t *testing.T) {
		if err := client.Claim("abc-123", 5*time.Minute); err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
		var args ipcClaimArgs
		if err := json.Unmarshal(capturedReq.Args, &args); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		if args.LockTTLSeconds != 300 {
			t.Errorf("LockTTLSeconds = %d, want 300", args.LockTTLSeconds)
		}
	})

	t.Run("without TTL", func(t *testing.T) {
		capturedReq = AgentIPCRequest{} // reset
		if err := client.Claim("abc-123", 0); err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
		if len(capturedReq.Args) != 0 {
			t.Errorf("Args = %q, want empty (omitted)", string(capturedReq.Args))
		}
	})
}

func TestAgentIPCClient_Update_Success(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	var capturedReq AgentIPCRequest
	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		capturedReq = req
		return AgentIPCResponse{Success: true}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	status := "in_progress"
	err := client.Update("abc-123", backend.UpdateParams{Status: &status})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify the Args contain the UpdateParams
	var params backend.UpdateParams
	if err := json.Unmarshal(capturedReq.Args, &params); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if params.Status == nil || *params.Status != "in_progress" {
		t.Errorf("Status = %v, want %q", params.Status, "in_progress")
	}
}

func TestAgentIPCClient_Update_NotFound(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		return AgentIPCResponse{Error: "issue not found", Kind: string(backend.KindNotFound)}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	err := client.Update("abc-123", backend.UpdateParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound, got: %v", err)
	}
}

func TestAgentIPCClient_Complete_Success(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	wantResult := &backend.CloseResult{
		Closed:    &backend.IssueData{ID: "abc-123", Title: "test", Status: "closed"},
		Unblocked: []backend.IssueData{{ID: "def-456", Title: "unblocked", Status: "open"}},
	}
	resultData, _ := json.Marshal(wantResult)

	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		return AgentIPCResponse{Success: true, Data: resultData}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	result, err := client.Complete("abc-123", backend.CloseParams{Reason: "done"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.Closed.ID != "abc-123" {
		t.Errorf("result.Closed.ID = %q, want %q", result.Closed.ID, "abc-123")
	}
	if len(result.Unblocked) != 1 || result.Unblocked[0].ID != "def-456" {
		t.Errorf("result.Unblocked = %v, want [{ID: def-456}]", result.Unblocked)
	}
}

func TestAgentIPCClient_Complete_NotFound(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		return AgentIPCResponse{Error: "issue not found", Kind: string(backend.KindNotFound)}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	_, err := client.Complete("abc-123", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound, got: %v", err)
	}
}

func TestAgentIPCClient_Complete_MalformedData(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	// Data is valid JSON but not a valid CloseResult (string, not object)
	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		return AgentIPCResponse{Success: true, Data: json.RawMessage(`"not an object"`)}
	})

	client := NewAgentIPCClient(socketPath, "falcon")
	_, err := client.Complete("abc-123", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error for malformed data")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("expected KindInternal, got: %v", err)
	}
}

func TestAgentIPCClient_TransportError_NoSocket(t *testing.T) {
	client := NewAgentIPCClient("/nonexistent/path/ipc.sock", "falcon")

	t.Run("claim", func(t *testing.T) {
		err := client.Claim("abc-123", 0)
		if err == nil {
			t.Fatal("expected error")
		}
		if !backend.IsKind(err, backend.KindUnavailable) {
			t.Errorf("expected KindUnavailable, got: %v", err)
		}
	})

	t.Run("update", func(t *testing.T) {
		err := client.Update("abc-123", backend.UpdateParams{})
		if err == nil {
			t.Fatal("expected error")
		}
		if !backend.IsKind(err, backend.KindUnavailable) {
			t.Errorf("expected KindUnavailable, got: %v", err)
		}
	})

	t.Run("complete", func(t *testing.T) {
		_, err := client.Complete("abc-123", backend.CloseParams{})
		if err == nil {
			t.Fatal("expected error")
		}
		if !backend.IsKind(err, backend.KindUnavailable) {
			t.Errorf("expected KindUnavailable, got: %v", err)
		}
	})
}

func TestAgentIPCClient_TransportError_ServerHangs(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	// Server accepts but never writes a response
	ln, err := net.Listen("unix", socketPath)
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
			// Read the request but never respond — hold connection open briefly
			scanner := bufio.NewScanner(conn)
			scanner.Scan()
			// Close after a delay shorter than the client's 10s timeout
			// but long enough to prove the client doesn't hang forever
			time.AfterFunc(100*time.Millisecond, func() { _ = conn.Close() })
		}
	}()

	client := NewAgentIPCClient(socketPath, "falcon")
	err = client.Claim("abc-123", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Errorf("expected KindUnavailable, got: %v", err)
	}
}

func TestAgentIPCClient_TransportError_GarbageResponse(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	ln, err := net.Listen("unix", socketPath)
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

	client := NewAgentIPCClient(socketPath, "falcon")
	err = client.Claim("abc-123", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Errorf("expected KindUnavailable, got: %v", err)
	}
}

func TestAgentIPCClient_AgentNamePopulated(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	var capturedReq AgentIPCRequest
	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		capturedReq = req
		return AgentIPCResponse{Success: true}
	})

	client := NewAgentIPCClient(socketPath, "my-custom-agent")
	if err := client.Claim("abc-123", 0); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	if capturedReq.AgentName != "my-custom-agent" {
		t.Errorf("AgentName = %q, want %q", capturedReq.AgentName, "my-custom-agent")
	}
	if capturedReq.Operation != ipcOpClaim {
		t.Errorf("Operation = %q, want %q", capturedReq.Operation, ipcOpClaim)
	}
	if capturedReq.IssueID != "abc-123" {
		t.Errorf("IssueID = %q, want %q", capturedReq.IssueID, "abc-123")
	}
}
