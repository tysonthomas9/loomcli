package git

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func TestDiffServiceIssueDiffStatAdditionalBranches(t *testing.T) {
	ctx := context.Background()

	if _, err := NewDiffService(&mockGitOps{}, nil).GetIssueDiffStat(ctx, "WS", ""); err == nil {
		t.Fatal("empty issue ID error = nil")
	}
	if _, err := NewDiffService(&mockGitOps{}, nil).GetIssueDiffStat(ctx, "WS", "ISSUE-1"); err == nil {
		t.Fatal("nil pool error = nil")
	}

	issueData, _ := json.Marshal(map[string]string{"assignee": "falcon"})
	socketPath := startGitDiffMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := gitDiffHealthHandler(req); ok {
			return resp
		}
		if req.Operation == "show" {
			return rpc.Response{Success: true, Data: issueData}
		}
		return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
	})
	pool := newGitDiffMockPool(t, socketPath)
	wt := testWorktree()
	gitOps := &mockGitOps{
		resolveFunc: func(name string) (*ops.AgentWorktree, error) {
			if name != "falcon" {
				t.Fatalf("resolved agent = %q, want falcon", name)
			}
			return wt, nil
		},
		diffStatFunc: func(path, from string) ops.DiffStatResult {
			if path != wt.Path || from != wt.DefaultBranch {
				t.Fatalf("DiffStat args = %q %q, want %q %q", path, from, wt.Path, wt.DefaultBranch)
			}
			return ops.DiffStatResult{LinesAdded: 11, LinesRemoved: 4}
		},
	}
	stat, err := NewDiffService(gitOps, pool).GetIssueDiffStat(ctx, "WS", "ISSUE-1")
	if err != nil {
		t.Fatalf("GetIssueDiffStat success: %v", err)
	}
	if stat.Branch != wt.Branch || stat.Added != 11 || stat.Removed != 4 {
		t.Fatalf("stat = %+v", stat)
	}

	badJSONSocket := startGitDiffMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := gitDiffHealthHandler(req); ok {
			return resp
		}
		return rpc.Response{Success: true, Data: []byte("123")}
	})
	if _, err := NewDiffService(&mockGitOps{}, newGitDiffMockPool(t, badJSONSocket)).GetIssueDiffStat(ctx, "WS", "ISSUE-1"); err == nil || !strings.Contains(err.Error(), "parse issue") {
		t.Fatalf("bad JSON err = %v, want parse issue", err)
	}

	notFoundSocket := startGitDiffMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := gitDiffHealthHandler(req); ok {
			return resp
		}
		return rpc.Response{Success: false, Error: "not found: ISSUE-404"}
	})
	if _, err := NewDiffService(&mockGitOps{}, newGitDiffMockPool(t, notFoundSocket)).GetIssueDiffStat(ctx, "WS", "ISSUE-404"); err == nil || !strings.Contains(err.Error(), "issue not found") {
		t.Fatalf("not found err = %v, want issue not found", err)
	}
}

func newGitDiffMockPool(t *testing.T, socketPath string) daemon.Pool {
	t.Helper()
	pool, err := daemon.NewConnectionPool(socketPath, 1)
	if err != nil {
		t.Fatalf("NewConnectionPool: %v", err)
	}
	pool.SetDialTimeout(2 * time.Second)
	pool.SetPoolTimeout(2 * time.Second)
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func startGitDiffMockServer(t *testing.T, handler func(rpc.Request) rpc.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "git-diff-rpc-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "loom.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
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
				reader := bufio.NewReader(c)
				for {
					line, err := reader.ReadBytes('\n')
					if err != nil {
						return
					}
					var req rpc.Request
					if err := json.Unmarshal(line, &req); err != nil {
						return
					}
					resp, _ := json.Marshal(handler(req))
					resp = append(resp, '\n')
					_, _ = c.Write(resp)
				}
			}(conn)
		}
	}()
	return socketPath
}

func gitDiffHealthHandler(req rpc.Request) (rpc.Response, bool) {
	switch req.Operation {
	case "health":
		data, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
		return rpc.Response{Success: true, Data: data}, true
	case "ping":
		return rpc.Response{Success: true}, true
	}
	return rpc.Response{}, false
}
