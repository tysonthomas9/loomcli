package svcimpl

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

func TestDiffServiceIssueDiffStatRPCBranches(t *testing.T) {
	ctx := context.Background()
	wt := &ops.AgentWorktree{Name: "falcon", Path: "/tmp/falcon", Branch: "feature/falcon", DefaultBranch: "main"}
	issueData, _ := json.Marshal(map[string]string{"assignee": "falcon"})

	socketPath := startSvcImplDiffMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := svcImplDiffHealthHandler(req); ok {
			return resp
		}
		if req.Operation == "show" {
			return rpc.Response{Success: true, Data: issueData}
		}
		return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
	})
	gitOps := &fakeGitOps{
		wt:   wt,
		diff: ops.DiffStatResult{LinesAdded: 8, LinesRemoved: 3},
	}
	stat, err := NewDiffService(gitOps, newSvcImplDiffMockPool(t, socketPath)).GetIssueDiffStat(ctx, "WS", "ISSUE-1")
	if err != nil {
		t.Fatalf("GetIssueDiffStat success: %v", err)
	}
	if stat.Branch != wt.Branch || stat.Added != 8 || stat.Removed != 3 {
		t.Fatalf("stat = %+v", stat)
	}

	noAssignee, _ := json.Marshal(map[string]string{"assignee": ""})
	noAssigneeSocket := startSvcImplDiffMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := svcImplDiffHealthHandler(req); ok {
			return resp
		}
		return rpc.Response{Success: true, Data: noAssignee}
	})
	if _, err := NewDiffService(&fakeGitOps{}, newSvcImplDiffMockPool(t, noAssigneeSocket)).GetIssueDiffStat(ctx, "WS", "ISSUE-1"); err == nil || !strings.Contains(err.Error(), "no assignee") {
		t.Fatalf("no assignee err = %v", err)
	}

	resolveErrSocket := startSvcImplDiffMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := svcImplDiffHealthHandler(req); ok {
			return resp
		}
		return rpc.Response{Success: true, Data: issueData}
	})
	if _, err := NewDiffService(&fakeGitOps{resolveErr: os.ErrNotExist}, newSvcImplDiffMockPool(t, resolveErrSocket)).GetIssueDiffStat(ctx, "WS", "ISSUE-1"); err == nil || !strings.Contains(err.Error(), "agent worktree not found") {
		t.Fatalf("resolve err = %v, want agent worktree not found", err)
	}

	genericErrSocket := startSvcImplDiffMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := svcImplDiffHealthHandler(req); ok {
			return resp
		}
		return rpc.Response{Success: false, Error: "boom"}
	})
	if _, err := NewDiffService(&fakeGitOps{}, newSvcImplDiffMockPool(t, genericErrSocket)).GetIssueDiffStat(ctx, "WS", "ISSUE-1"); err == nil || !strings.Contains(err.Error(), "failed to get issue") {
		t.Fatalf("generic show err = %v, want failed to get issue", err)
	}
}

func newSvcImplDiffMockPool(t *testing.T, socketPath string) daemon.Pool {
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

func startSvcImplDiffMockServer(t *testing.T, handler func(rpc.Request) rpc.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "svcimpl-diff-rpc-*")
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

func svcImplDiffHealthHandler(req rpc.Request) (rpc.Response, bool) {
	switch req.Operation {
	case "health":
		data, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
		return rpc.Response{Success: true, Data: data}, true
	case "ping":
		return rpc.Response{Success: true}, true
	}
	return rpc.Response{}, false
}
