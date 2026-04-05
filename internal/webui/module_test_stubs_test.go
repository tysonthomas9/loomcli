package webui

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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

func (s *stubTerminalService) GenerateToken(_ context.Context, _, _ string) (string, error) {
	return "tok", nil
}
func (s *stubTerminalService) RestartSession(_ context.Context, _, _ string) (*TerminalRestartResult, error) {
	return &TerminalRestartResult{}, nil
}
func (s *stubTerminalService) KillSession(_ context.Context, _ string) error { return nil }
func (s *stubTerminalService) GetSessionStatus(_ context.Context, _ string) (*TerminalStatusResult, error) {
	return &TerminalStatusResult{}, nil
}
func (s *stubTerminalService) ListSessions(_ context.Context, _ string) ([]TerminalSessionInfo, error) {
	return nil, nil
}
func (s *stubTerminalService) SpawnSession(_ context.Context, _ string, _ *SpawnParams) (*SpawnResult, error) {
	return &SpawnResult{}, nil
}
func (s *stubTerminalService) SeedSession(_ context.Context, _ string, _ *SeedParams) error {
	return nil
}
func (s *stubTerminalService) ScheduleKill(_ context.Context, _ string) error { return nil }
func (s *stubTerminalService) CloseAllSessions(_ context.Context) (*CloseAllResult, error) {
	return &CloseAllResult{}, nil
}
func (s *stubTerminalService) ExportSession(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubTerminalService) GetScrollbackInfo(_ context.Context, _ string) (*ScrollbackInfoResult, error) {
	return &ScrollbackInfoResult{}, nil
}
func (s *stubTerminalService) GetScrollback(_ context.Context, _ string) (*ScrollbackResult, error) {
	return &ScrollbackResult{}, nil
}
func (s *stubTerminalService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	return nil, nil
}
func (s *stubTerminalService) GetTab(_ context.Context, _, _ string) (*tabmeta.TabMetadata, error) {
	return &tabmeta.TabMetadata{}, nil
}
func (s *stubTerminalService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*PatchTabResult, error) {
	return &PatchTabResult{Tab: &tabmeta.TabMetadata{}}, nil
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

// stubSessionService implements SessionService with no-op defaults for module tests.
type stubSessionService struct{}

func (s *stubSessionService) ListTaskSessions(_ context.Context, _ string) ([]SessionListItem, error) {
	return nil, nil
}
func (s *stubSessionService) GetSession(_ context.Context, _, _ string) (*SessionDetailData, error) {
	return &SessionDetailData{}, nil
}
func (s *stubSessionService) GetSessionTranscript(_ context.Context, _, _ string) ([]sessions.TranscriptEntry, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionDiff(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (s *stubSessionService) ListSessionHistory(_ context.Context, _, _ string) ([]sessionhistory.SessionRecord, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionScrollback(_ context.Context, _, _, _ string) (*SessionScrollbackResult, error) {
	return &SessionScrollbackResult{}, nil
}

// stubDiffService implements DiffService with no-op defaults for module tests.
type stubDiffService struct{}

func (s *stubDiffService) DiffCommits(_ context.Context, _, _, _ string, _ int) ([]DiffCommitResult, error) {
	return nil, nil
}
func (s *stubDiffService) DiffFiles(_ context.Context, _, _, _, _ string) ([]DiffFileResult, error) {
	return nil, nil
}
func (s *stubDiffService) DiffFilePatch(_ context.Context, _, _, _, _, _ string) (*DiffFilePatchResult, error) {
	return &DiffFilePatchResult{}, nil
}
func (s *stubDiffService) GetIssueDiffStat(_ context.Context, _, _ string) (*IssueDiffStatResult, error) {
	return &IssueDiffStatResult{}, nil
}

// stubFileService implements FileService with no-op defaults for module tests.
type stubFileService struct{}

func (s *stubFileService) ListDirectory(_ context.Context, _, _, _ string) (*FileTreeResult, error) {
	return &FileTreeResult{}, nil
}
func (s *stubFileService) ReadFile(_ context.Context, _, _, _ string) (*FileReadResult, error) {
	return &FileReadResult{}, nil
}
func (s *stubFileService) WriteFile(_ context.Context, _, _, _, _ string) error { return nil }
