package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestNewTerminalServiceAndTokenValidation(t *testing.T) {
	auth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("NewTerminalAuth: %v", err)
	}
	started := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	svcIface := NewTerminalService(auth, nil, nil, nil, nil, started)
	svc, ok := svcIface.(*terminalServiceImpl)
	if !ok {
		t.Fatalf("service type = %T", svcIface)
	}
	if svc.termAuth != auth || !svc.startedAt.Equal(started) {
		t.Fatalf("service fields not initialized: %+v", svc)
	}

	token, err := svc.GenerateToken(context.Background(), "WS", "session-1", "user-1")
	if err != nil {
		t.Fatalf("GenerateToken valid: %v", err)
	}
	if token == "" {
		t.Fatal("GenerateToken returned empty token")
	}

	if _, err := svc.GenerateToken(context.Background(), "WS", "bad/session", "user-1"); err == nil || !strings.Contains(err.Error(), "invalid session name") {
		t.Fatalf("invalid session err = %v", err)
	}

	noAuth := &terminalServiceImpl{}
	_, err = noAuth.GenerateToken(context.Background(), "WS", "session-1", "user-1")
	var svcErr *service.ServiceError
	if err == nil || !errors.As(err, &svcErr) || svcErr.Kind != service.KindUnavailable {
		t.Fatalf("nil auth err = %v", err)
	}
}

func TestTerminalServicePTYNilAndLiveBranches(t *testing.T) {
	svc := &terminalServiceImpl{}
	if svc.ptyAlive("WS", "sess") {
		t.Fatal("nil pty manager should not report alive")
	}
	if svc.attachedClients("WS", "sess") != 0 {
		t.Fatal("nil pty manager should report zero attached clients")
	}

	fake := newFakePTYSource()
	svc.ptyMgr = fake
	key := SessionKey{Workspace: "WS", Name: "sess"}
	fake.alive[key] = true
	if !svc.ptyAlive("WS", "sess") {
		t.Fatal("live pty not detected")
	}
	if got := svc.attachedClients("WS", "sess"); got != 1 {
		t.Fatalf("attachedClients = %d, want 1", got)
	}
}
