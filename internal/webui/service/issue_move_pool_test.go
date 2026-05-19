package service

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

type movePoolWorkspaceKey struct{}

func movePoolWithWorkspace(ctx context.Context, wsID string) context.Context {
	return context.WithValue(ctx, movePoolWorkspaceKey{}, wsID)
}

func movePoolWorkspaceFromContext(ctx context.Context) string {
	wsID, _ := ctx.Value(movePoolWorkspaceKey{}).(string)
	return wsID
}

func TestMoveIssueViaPoolSuccessUsesSourceAndTargetRPC(t *testing.T) {
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	due := now.Add(24 * time.Hour)
	deferUntil := now.Add(2 * time.Hour)
	estimate := 42
	externalRef := "gh-123"
	sourceIssue := types.Issue{
		ID:                 "SRC-1",
		Title:              "Move me",
		Description:        "details",
		Design:             "design",
		AcceptanceCriteria: "criteria",
		Notes:              "notes",
		Status:             types.StatusOpen,
		Priority:           2,
		IssueType:          types.TypeTask,
		Assignee:           "planner",
		Owner:              "owner@example.test",
		EstimatedMinutes:   &estimate,
		ExternalRef:        &externalRef,
		Labels:             []string{"ui", "go"},
		DueAt:              &due,
		DeferUntil:         &deferUntil,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	var mu sync.Mutex
	var sourceOps []string
	var targetOps []string
	var createArgs rpc.CreateArgs
	var commentArgs rpc.CommentAddArgs
	var closeArgs rpc.CloseArgs

	sourceSocket := startMovePoolRPCServer(t, func(req rpc.Request) rpc.Response {
		mu.Lock()
		sourceOps = append(sourceOps, req.Operation)
		mu.Unlock()
		switch req.Operation {
		case rpc.OpHealth:
			return movePoolJSONResponse(t, rpc.HealthResponse{Status: "healthy", Compatible: true})
		case rpc.OpShow:
			var args rpc.ShowArgs
			if err := json.Unmarshal(req.Args, &args); err != nil || args.ID != "SRC-1" {
				return rpc.Response{Success: false, Error: "bad show args"}
			}
			return movePoolJSONResponse(t, sourceIssue)
		case rpc.OpCommentAdd:
			if err := json.Unmarshal(req.Args, &commentArgs); err != nil {
				return rpc.Response{Success: false, Error: "bad comment args"}
			}
			return rpc.Response{Success: true}
		case rpc.OpClose:
			if err := json.Unmarshal(req.Args, &closeArgs); err != nil {
				return rpc.Response{Success: false, Error: "bad close args"}
			}
			return rpc.Response{Success: true}
		default:
			return rpc.Response{Success: false, Error: "unexpected source op " + req.Operation}
		}
	})
	targetSocket := startMovePoolRPCServer(t, func(req rpc.Request) rpc.Response {
		mu.Lock()
		targetOps = append(targetOps, req.Operation)
		mu.Unlock()
		switch req.Operation {
		case rpc.OpHealth:
			return movePoolJSONResponse(t, rpc.HealthResponse{Status: "healthy", Compatible: true})
		case rpc.OpCreate:
			if err := json.Unmarshal(req.Args, &createArgs); err != nil {
				return rpc.Response{Success: false, Error: "bad create args"}
			}
			return movePoolJSONResponse(t, types.Issue{ID: "DST-9", Title: createArgs.Title, Status: types.StatusOpen})
		default:
			return rpc.Response{Success: false, Error: "unexpected target op " + req.Operation}
		}
	})

	sourcePool, err := daemon.NewConnectionPool(sourceSocket, 1)
	if err != nil {
		t.Fatalf("source pool: %v", err)
	}
	t.Cleanup(func() { _ = sourcePool.Close() })
	sourcePool.SetDialTimeout(time.Second)
	targetPool, err := daemon.NewConnectionPool(targetSocket, 1)
	if err != nil {
		t.Fatalf("target pool: %v", err)
	}
	t.Cleanup(func() { _ = targetPool.Close() })
	targetPool.SetDialTimeout(time.Second)

	multiPool := daemon.NewMultiPool(movePoolWorkspaceFromContext, 1)
	t.Cleanup(func() { _ = multiPool.Close() })
	if err := multiPool.Register("target-ws", targetPool); err != nil {
		t.Fatalf("register target pool: %v", err)
	}

	svc := NewIssueService(sourcePool, multiPool, movePoolWithWorkspace)
	result, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "SRC-1",
		TargetWorkspace: "Target Workspace",
		Validator:       testWorkspaceValidator{targetID: "target-ws"},
	})
	if err != nil {
		t.Fatalf("MoveIssue: %v", err)
	}
	if result.SourceID != "SRC-1" || result.TargetID != "DST-9" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "planner") {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
	if got := strings.Join(sourceOps, ","); got != "health,show,comment_add,close" {
		t.Fatalf("source operations = %s", got)
	}
	if got := strings.Join(targetOps, ","); got != "health,create" {
		t.Fatalf("target operations = %s", got)
	}
	if createArgs.Title != sourceIssue.Title || createArgs.IssueType != string(types.TypeTask) || createArgs.CreatedBy != "web-ui" {
		t.Fatalf("create args = %+v", createArgs)
	}
	if !strings.Contains(createArgs.Description, "(Moved from SRC-1)") || createArgs.ExternalRef != externalRef {
		t.Fatalf("create args missing moved marker/ref: %+v", createArgs)
	}
	if createArgs.EstimatedMinutes == nil || *createArgs.EstimatedMinutes != estimate || createArgs.DueAt == "" || createArgs.DeferUntil == "" {
		t.Fatalf("create scheduling fields = %+v", createArgs)
	}
	if commentArgs.ID != "SRC-1" || commentArgs.Author != "web-ui" || !strings.Contains(commentArgs.Text, "DST-9") {
		t.Fatalf("comment args = %+v", commentArgs)
	}
	if closeArgs.ID != "SRC-1" || !closeArgs.Force || !strings.Contains(closeArgs.Reason, "DST-9") {
		t.Fatalf("close args = %+v", closeArgs)
	}
}

func TestMoveIssueViaPoolValidationAndUnavailableBranches(t *testing.T) {
	svc := NewIssueService(nil, nil, movePoolWithWorkspace)
	if _, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "SRC-1",
		TargetWorkspace: "Target",
		Validator:       testWorkspaceValidator{targetID: "target-ws"},
	}); err == nil {
		t.Fatal("expected nil source pool error")
	}

	sourcePool, err := daemon.NewConnectionPool(startMovePoolRPCServer(t, func(req rpc.Request) rpc.Response {
		return movePoolJSONResponse(t, rpc.HealthResponse{Status: "healthy", Compatible: true})
	}), 1)
	if err != nil {
		t.Fatalf("source pool: %v", err)
	}
	t.Cleanup(func() { _ = sourcePool.Close() })

	svc = NewIssueService(sourcePool, nil, movePoolWithWorkspace)
	if _, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "SRC-1",
		TargetWorkspace: "Target",
		Validator:       testWorkspaceValidator{targetID: "target-ws"},
	}); err == nil {
		t.Fatal("expected nil multiPool error")
	}

	multiPool := daemon.NewMultiPool(movePoolWorkspaceFromContext, 1)
	t.Cleanup(func() { _ = multiPool.Close() })
	svc = NewIssueService(sourcePool, multiPool, movePoolWithWorkspace)
	if _, err := svc.MoveIssue(context.Background(), MoveIssueParams{IssueID: "SRC-1"}); err == nil {
		t.Fatal("expected missing validator error")
	}
	if _, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "SRC-1",
		TargetWorkspace: "Target",
		Validator:       testWorkspaceValidator{err: ErrValidation("bad target")},
	}); err == nil {
		t.Fatal("expected validator error")
	}
}

func startMovePoolRPCServer(t *testing.T, handler func(req rpc.Request) rpc.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "loom-move-rpc-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "loom.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
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
					resp := handler(req)
					respJSON, err := json.Marshal(resp)
					if err != nil {
						return
					}
					respJSON = append(respJSON, '\n')
					if _, err := c.Write(respJSON); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return socketPath
}

func movePoolJSONResponse(t *testing.T, value any) rpc.Response {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return rpc.Response{Success: true, Data: data}
}
