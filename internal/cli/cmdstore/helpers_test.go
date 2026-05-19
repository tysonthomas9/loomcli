package cmdstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type contextKey string

func TestRootContextAndSignalContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), contextKey("trace"), "root")
	SetRootContext(ctx)
	t.Cleanup(func() { SetRootContext(context.Background()) })

	if got := RootContext().Value(contextKey("trace")); got != "root" {
		t.Fatalf("RootContext value = %#v, want root", got)
	}

	sigCtx, cancel := SignalContext()
	defer cancel()
	if got := sigCtx.Value(contextKey("trace")); got != "root" {
		t.Fatalf("SignalContext parent value = %#v, want root", got)
	}
	cancel()
	if err := sigCtx.Err(); err == nil {
		t.Fatal("SignalContext was not canceled")
	}
}

func TestActiveWorkspaceAndIsNotFound(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	t.Setenv(bootstrap.EnvWorkspace, "WS")
	got, err := ActiveWorkspace(ctx, st)
	if err != nil || got != "WS" {
		t.Fatalf("ActiveWorkspace = %q, %v; want WS, nil", got, err)
	}

	t.Setenv(bootstrap.EnvWorkspace, "MISSING")
	_, err = ActiveWorkspace(ctx, st)
	if !IsNotFound(err) {
		t.Fatalf("missing workspace err = %v, IsNotFound=false", err)
	}
	if IsNotFound(errors.New("other")) {
		t.Fatal("IsNotFound returned true for unrelated error")
	}
	if !IsNotFound(domain.ErrNotFound) {
		t.Fatal("IsNotFound returned false for domain.ErrNotFound")
	}
}

func TestWriteJSON(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	writeErr := WriteJSON(map[string]any{"name": "loom", "count": 2})
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("close pipe writer: %v", closeErr)
	}
	if writeErr != nil {
		t.Fatalf("WriteJSON: %v", writeErr)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if !strings.Contains(buf.String(), "\n  \"count\": 2") {
		t.Fatalf("WriteJSON output was not indented JSON: %q", buf.String())
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["name"] != "loom" || got["count"] != float64(2) {
		t.Fatalf("decoded output = %#v", got)
	}
}
