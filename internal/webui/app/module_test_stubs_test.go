package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/filecoord"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/sourcecontrolcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// stubTerminalService implements TerminalService with no-op defaults for module tests.
type stubTerminalService struct {
	tabs []tabmeta.TabMetadata
}

func (s *stubTerminalService) GenerateToken(_ context.Context, _, _, _ string) (string, error) {
	return "tok", nil
}
func (s *stubTerminalService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	return append([]tabmeta.TabMetadata(nil), s.tabs...), nil
}
func (s *stubTerminalService) GetTab(_ context.Context, _, _ string) (*tabmeta.TabMetadata, error) {
	return &tabmeta.TabMetadata{}, nil
}
func (s *stubTerminalService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*terminal.PatchTabResult, error) {
	return &terminal.PatchTabResult{Tab: &tabmeta.TabMetadata{}}, nil
}
func (s *stubTerminalService) PutTab(_ context.Context, _ string, _ *tabmeta.TabMetadata) error {
	return nil
}
func (s *stubTerminalService) PersistInteractionTabIdentity(_ context.Context, _ string, _ *tabmeta.TabMetadata) error {
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
func (s *stubTerminalService) StartSetup(_ context.Context, _ string, req terminal.TerminalSetupRequest) (*terminal.TerminalSetupResult, error) {
	return &terminal.TerminalSetupResult{Backend: req.Backend, Action: req.Action}, nil
}

// stubSessionService implements SessionService with no-op defaults for module tests.
type stubSessionService struct{}

func (s *stubSessionService) ListTaskSessions(_ context.Context, _ string) ([]sessioncoord.SessionListItem, error) {
	return nil, nil
}
func (s *stubSessionService) GetSession(_ context.Context, _, _ string) (*sessioncoord.SessionDetailData, error) {
	return &sessioncoord.SessionDetailData{}, nil
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
func (s *stubSessionService) GetSessionScrollback(_ context.Context, _, _, _ string) (*sessioncoord.SessionScrollbackResult, error) {
	return &sessioncoord.SessionScrollbackResult{}, nil
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
func (s *stubDiffService) GetIssueDiffStat(_ context.Context, _, _ string) (*sourcecontrolcoord.IssueDiffStatResult, error) {
	return &sourcecontrolcoord.IssueDiffStatResult{}, nil
}

// stubFileService implements FileService with no-op defaults for module tests.
type stubFileService struct{}

func (s *stubFileService) ListDirectoryScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _ string) (*filecoord.FileTreeResult, error) {
	return &filecoord.FileTreeResult{}, nil
}
func (s *stubFileService) ReadFileScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _ string) (*filecoord.FileReadResult, error) {
	return &filecoord.FileReadResult{}, nil
}
func (s *stubFileService) StatPathScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _ string) (*filecoord.FileStatResult, error) {
	return &filecoord.FileStatResult{}, nil
}
func (s *stubFileService) ReadFileAtRevScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _, _ string) (*filecoord.FileReadResult, error) {
	return &filecoord.FileReadResult{}, nil
}
func (s *stubFileService) IndexFilesScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _ string) (*filecoord.FileIndexResult, error) {
	return &filecoord.FileIndexResult{}, nil
}
func (s *stubFileService) SearchFilesScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _ string, _ filecoord.FileSearchRequest) (*filecoord.FileSearchResult, error) {
	return &filecoord.FileSearchResult{}, nil
}
func (s *stubFileService) GitStatusScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _ string) (filecoord.FileGitStatusResult, error) {
	return filecoord.FileGitStatusResult{}, nil
}
func (s *stubFileService) ListFileCheckouts(_ context.Context, _ string) (*filecoord.FileCheckoutsResult, error) {
	return &filecoord.FileCheckoutsResult{}, nil
}
func (s *stubFileService) RepairCheckout(_ context.Context, _ string, _ filecoord.FileCheckoutRepairRequest) (*ops.RepairResult, error) {
	return &ops.RepairResult{}, nil
}
func (s *stubFileService) DiffFileScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _, _, _ string) (*filecoord.FileDiffResult, error) {
	return &filecoord.FileDiffResult{}, nil
}
func (s *stubFileService) BlameFileScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _ string) (*filecoord.FileBlameResult, error) {
	return &filecoord.FileBlameResult{}, nil
}
func (s *stubFileService) HistoryFileScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _ string) (*filecoord.FileHistoryResult, error) {
	return &filecoord.FileHistoryResult{}, nil
}
func (s *stubFileService) WriteFileConditionalScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _, _ string, _ filecoord.FileWritePreconditions) (*filecoord.FileMutationResult, error) {
	return &filecoord.FileMutationResult{Success: true}, nil
}
func (s *stubFileService) DeletePathVersionedScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _ string, _ bool, _ string) error {
	return nil
}
func (s *stubFileService) MkdirScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _ string) error {
	return nil
}
func (s *stubFileService) MovePathVersionedScoped(_ context.Context, _ string, _ filecoord.FileScope, _, _, _, _ string, _ bool, _, _ string) (*filecoord.FileMutationResult, error) {
	return &filecoord.FileMutationResult{Success: true}, nil
}
