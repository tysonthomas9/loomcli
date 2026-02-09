package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// startMockDaemonServer creates a Unix socket server that handles the RPC
// protocol for health and ping operations. This enables testing connection
// success paths without a real daemon. Returns the socket path.
//
// Uses /tmp with a short random name because macOS $TMPDIR paths are too long
// for Unix sockets (104-byte limit).
func startMockDaemonServer(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "daemon-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "bd.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go handleMockConnection(conn)
		}
	}()

	return socketPath
}

func handleMockConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req rpc.Request
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}

		var resp rpc.Response
		switch req.Operation {
		case "health":
			healthData := rpc.HealthResponse{
				Status:     "healthy",
				Version:    "test",
				Compatible: true,
				Uptime:     100,
			}
			data, _ := json.Marshal(healthData)
			resp = rpc.Response{Success: true, Data: data}
		case "ping":
			pingData := rpc.PingResponse{
				Message: "pong",
				Version: "test",
			}
			data, _ := json.Marshal(pingData)
			resp = rpc.Response{Success: true, Data: data}
		default:
			resp = rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}

		respJSON, _ := json.Marshal(resp)
		respJSON = append(respJSON, '\n')
		if _, err := conn.Write(respJSON); err != nil {
			return
		}
	}
}
