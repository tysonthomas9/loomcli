package app

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// stubErrorPool implements daemon.Pool, returning errors from Get.
// Used in module tests where handlers must not attempt RPC calls.
type stubErrorPool struct{}

func (s *stubErrorPool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, errors.New("stub: pool unavailable")
}
func (s *stubErrorPool) Put(_ *rpc.Client)           {}
func (s *stubErrorPool) PutAfterError(_ *rpc.Client) {}
func (s *stubErrorPool) Discard(_ *rpc.Client)       {}
func (s *stubErrorPool) Stats() daemon.PoolStats     { return daemon.PoolStats{} }
func (s *stubErrorPool) Close() error                { return nil }

// stubTerminalService implements TerminalService with no-op defaults for module tests.
type stubTerminalService struct{}

func (s *stubTerminalService) GenerateToken(_ context.Context, _, _, _ string) (string, error) {
	return "tok", nil
}
func (s *stubTerminalService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	return nil, nil
}
func (s *stubTerminalService) GetTab(_ context.Context, _, _ string) (*tabmeta.TabMetadata, error) {
	return &tabmeta.TabMetadata{}, nil
}
func (s *stubTerminalService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*service.PatchTabResult, error) {
	return &service.PatchTabResult{Tab: &tabmeta.TabMetadata{}}, nil
}
func (s *stubTerminalService) PutTab(_ context.Context, _ string, _ *tabmeta.TabMetadata) error {
	return nil
}
func (s *stubTerminalService) DeleteTab(_ context.Context, _, _ string) error { return nil }
func (s *stubTerminalService) ListSessionsByIssue(_ context.Context) (map[string][]string, error) {
	return nil, nil
}
func (s *stubTerminalService) GetTerminalState(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubTerminalService) PatchTerminalState(_ context.Context, _, _ string) error { return nil }
func (s *stubTerminalService) StartSetup(_ context.Context, _ string, req service.TerminalSetupRequest) (*service.TerminalSetupResult, error) {
	return &service.TerminalSetupResult{Backend: req.Backend, Action: req.Action}, nil
}

// stubSessionService implements SessionService with no-op defaults for module tests.
type stubSessionService struct{}

func (s *stubSessionService) ListTaskSessions(_ context.Context, _, _ string) ([]service.SessionListItem, error) {
	return nil, nil
}
func (s *stubSessionService) GetSession(_ context.Context, _, _, _ string) (*service.SessionDetailData, error) {
	return &service.SessionDetailData{}, nil
}
func (s *stubSessionService) GetSessionTranscript(_ context.Context, _, _, _ string) ([]transcript.Event, error) {
	return nil, nil
}
func (s *stubSessionService) ListSessionSubagents(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionSubagentTranscript(_ context.Context, _, _, _, _ string) ([]transcript.Event, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionDiff(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}
func (s *stubSessionService) ListSessionHistory(_ context.Context, _, _ string) ([]sessionhistory.SessionRecord, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionScrollback(_ context.Context, _, _, _ string) (*service.SessionScrollbackResult, error) {
	return &service.SessionScrollbackResult{}, nil
}

// stubDiffService implements DiffService with no-op defaults for module tests.
type stubDiffService struct{}

func (s *stubDiffService) DiffCommits(_ context.Context, _, _, _ string, _ int) ([]ops.DiffCommitResult, error) {
	return nil, nil
}
func (s *stubDiffService) DiffFiles(_ context.Context, _, _, _, _ string) ([]ops.DiffFileResult, error) {
	return nil, nil
}
func (s *stubDiffService) DiffFilePatch(_ context.Context, _, _, _, _, _ string) (*ops.DiffFilePatchResult, error) {
	return &ops.DiffFilePatchResult{}, nil
}
func (s *stubDiffService) GetIssueDiffStat(_ context.Context, _, _ string) (*service.IssueDiffStatResult, error) {
	return &service.IssueDiffStatResult{}, nil
}

// stubFileService implements FileService with no-op defaults for module tests.
type stubFileService struct{}

func (s *stubFileService) ListDirectory(_ context.Context, _, _, _ string) (*service.FileTreeResult, error) {
	return &service.FileTreeResult{}, nil
}
func (s *stubFileService) ReadFile(_ context.Context, _, _, _ string) (*service.FileReadResult, error) {
	return &service.FileReadResult{}, nil
}
func (s *stubFileService) WriteFile(_ context.Context, _, _, _, _ string) error { return nil }
