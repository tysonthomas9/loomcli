package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func captureRemoveOutput(t *testing.T, action func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = stdout }()
	actionErr := action()
	_ = writer.Close()
	out, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(out), actionErr
}

func TestWorkspaceRemoveCommandUsesConfirmedDeletion(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["ALPHA"] = bootstrap.WorkspaceLocalState{Path: t.TempDir()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldStore := withWorkspaceRemoveStore
	withWorkspaceRemoveStore = func(fn func(context.Context, *bootstrap.StoreHandle) error) error {
		return fn(ctx, &bootstrap.StoreHandle{Store: st})
	}
	oldFingerprint, oldForce := wsRemoveFingerprint, wsRemoveForce
	t.Cleanup(func() {
		withWorkspaceRemoveStore = oldStore
		wsRemoveFingerprint, wsRemoveForce = oldFingerprint, oldForce
		workspaceCmd.SetArgs(nil)
	})
	wsRemoveFingerprint, wsRemoveForce = "", false
	previewText, err := captureRemoveOutput(t, func() error {
		return removeWorkspace(workspaceRemoveCmd, []string{"ALPHA"})
	})
	if err == nil || !strings.Contains(err.Error(), "unsaved_work") {
		t.Fatalf("unconfirmed removal = %v, want unsaved_work", err)
	}
	if _, err := st.Workspaces().Get(ctx, "ALPHA"); err != nil {
		t.Fatalf("unconfirmed removal changed row: %v", err)
	}
	_, fingerprint, ok := strings.Cut(previewText, "Delete fingerprint: ")
	if !ok {
		t.Fatalf("CLI did not print fingerprint: %q", previewText)
	}
	fingerprint = strings.TrimSpace(fingerprint)
	workspaceCmd.SetArgs([]string{"remove", "ALPHA", "--force", "--confirm-fingerprint", fingerprint})
	output, err := captureRemoveOutput(t, workspaceCmd.Execute)
	if err != nil || !strings.Contains(output, `Workspace "ALPHA" removed.`) {
		t.Fatalf("confirmed CLI removal: output=%q err=%v", output, err)
	}
	if _, err := st.Workspaces().Get(ctx, "ALPHA"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("CLI did not delete workspace row: %v", err)
	}
}
