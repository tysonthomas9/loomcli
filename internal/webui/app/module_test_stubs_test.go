package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	transcript "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
)

// stubTerminalService implements TerminalService with no-op defaults for module tests.
type stubTerminalService struct {
	tabs []interaction.TabMetadata
}

func (s *stubTerminalService) GenerateToken(_ context.Context, _, _, _ string) (string, error) {
	return "tok", nil
}
func (s *stubTerminalService) ListTabs(_ context.Context, _ string) ([]interaction.TabMetadata, error) {
	return append([]interaction.TabMetadata(nil), s.tabs...), nil
}
func (s *stubTerminalService) GetTab(_ context.Context, _, _ string) (*interaction.TabMetadata, error) {
	return &interaction.TabMetadata{}, nil
}
func (s *stubTerminalService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*interaction.PatchTabResult, error) {
	return &interaction.PatchTabResult{Tab: &interaction.TabMetadata{}}, nil
}
func (s *stubTerminalService) PutTab(_ context.Context, command interaction.PutTerminalTabCommand) (*interaction.TabMetadata, error) {
	return &interaction.TabMetadata{SessionName: command.TerminalID}, nil
}
func (s *stubTerminalService) DeleteTab(_ context.Context, _, _ string) error { return nil }
func (s *stubTerminalService) ListSessionsByIssue(_ context.Context) (map[string][]string, error) {
	return nil, nil
}
func (s *stubTerminalService) EnsureAgentTerminal(_ context.Context, _ interaction.EnsureAgentTerminalCommand) (*interaction.TabMetadata, error) {
	return &interaction.TabMetadata{}, nil
}
func (s *stubTerminalService) PlanTerminalAttach(context.Context, interaction.TerminalAttachCommand) (interaction.TerminalAttachPlan, error) {
	return interaction.TerminalAttachPlan{}, nil
}
func (s *stubTerminalService) AttachTerminal(context.Context, interaction.TerminalAttachCommand) (*interaction.TerminalAttachResult, error) {
	return nil, interaction.ErrUnavailable
}
func (s *stubTerminalService) DetachTerminal(context.Context, string, string, string) {}
func (s *stubTerminalService) AgentTerminalInfo(context.Context, string, string) (*interaction.AgentTerminalInfo, error) {
	return &interaction.AgentTerminalInfo{}, nil
}
func (s *stubTerminalService) AttachAgentTerminal(context.Context, interaction.AttachAgentTerminalCommand) (*interaction.AgentTerminalAttachResult, error) {
	return nil, interaction.ErrUnavailable
}
func (s *stubTerminalService) DetachAgentTerminal(context.Context, string) error { return nil }
func (s *stubTerminalService) GetTerminalState(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubTerminalService) PatchTerminalState(_ context.Context, _, _ string) error { return nil }
func (s *stubTerminalService) StartSetup(_ context.Context, _ string, req interaction.TerminalSetupRequest) (*interaction.TerminalSetupResult, error) {
	return &interaction.TerminalSetupResult{Backend: req.Backend, Action: req.Action}, nil
}

// stubSessionService implements SessionService with no-op defaults for module tests.
type stubSessionService struct{}

func (s *stubSessionService) ListTaskSessions(_ context.Context, _, _ string) ([]sessionarchive.SessionListItem, error) {
	return nil, nil
}
func (s *stubSessionService) GetSession(_ context.Context, _, _, _ string) (*sessionarchive.SessionDetailData, error) {
	return &sessionarchive.SessionDetailData{}, nil
}
func (s *stubSessionService) GetSessionTranscript(_ context.Context, _, _, _ string) ([]transcript.Event, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionDiff(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}
func (s *stubSessionService) ListSessionHistory(_ context.Context, _, _ string) ([]sessionarchive.SessionHistoryItem, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionScrollback(_ context.Context, _, _, _ string) (*sessionarchive.SessionScrollbackResult, error) {
	return &sessionarchive.SessionScrollbackResult{}, nil
}

type stubIssueDiff struct{}

func (s *stubIssueDiff) GetIssueDiff(
	_ context.Context,
	_ readprojection.IssueDiffQuery,
) (readprojection.IssueDiffResult, error) {
	return readprojection.IssueDiffResult{}, nil
}

type stubSourceBrowse struct{}

func (s *stubSourceBrowse) DiffStat(_ context.Context, _ sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error) {
	return sourcecontrol.AgentDiffStat{}, nil
}

func (s *stubSourceBrowse) DiffCommits(_ context.Context, _ sourcecontrol.DiffCommitsQuery) ([]sourcecontrol.DiffCommit, error) {
	return []sourcecontrol.DiffCommit{}, nil
}
func (s *stubSourceBrowse) DiffFiles(_ context.Context, _ sourcecontrol.DiffFilesQuery) ([]sourcecontrol.DiffFile, error) {
	return []sourcecontrol.DiffFile{}, nil
}
func (s *stubSourceBrowse) DiffFilePatch(_ context.Context, _ sourcecontrol.DiffFilePatchQuery) (*sourcecontrol.DiffFilePatch, error) {
	return &sourcecontrol.DiffFilePatch{}, nil
}

// stubFileService implements the three Source Control workspace ports with
// no-op defaults for module tests.
type stubFileService struct{ stubSourceBrowse }

func (s *stubFileService) ListDirectory(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileTreeResult, error) {
	return &sourcecontrol.FileTreeResult{}, nil
}
func (s *stubFileService) ReadFile(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileReadResult, error) {
	return &sourcecontrol.FileReadResult{}, nil
}
func (s *stubFileService) StatPath(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileStatResult, error) {
	return &sourcecontrol.FileStatResult{}, nil
}
func (s *stubFileService) ReadFileAtRevision(_ context.Context, _ sourcecontrol.RevisionQuery) (*sourcecontrol.FileReadResult, error) {
	return &sourcecontrol.FileReadResult{}, nil
}
func (s *stubFileService) IndexFiles(_ context.Context, _ sourcecontrol.LocationQuery) (*sourcecontrol.FileIndexResult, error) {
	return &sourcecontrol.FileIndexResult{}, nil
}
func (s *stubFileService) SearchFiles(_ context.Context, _ sourcecontrol.SearchQuery) (*sourcecontrol.FileSearchResult, error) {
	return &sourcecontrol.FileSearchResult{}, nil
}
func (s *stubFileService) Status(_ context.Context, _ sourcecontrol.LocationQuery) (sourcecontrol.FileGitStatusResult, error) {
	return sourcecontrol.FileGitStatusResult{}, nil
}
func (s *stubFileService) ListCheckouts(_ context.Context, _ sourcecontrol.WorkspaceQuery) (*sourcecontrol.FileCheckoutsResult, error) {
	return &sourcecontrol.FileCheckoutsResult{}, nil
}
func (s *stubFileService) Repair(_ context.Context, _ sourcecontrol.RepairCommand) (*sourcecontrol.RepairResult, error) {
	return &sourcecontrol.RepairResult{}, nil
}
func (s *stubFileService) DiffPath(_ context.Context, _ sourcecontrol.PathDiffQuery) (*sourcecontrol.FileDiffResult, error) {
	return &sourcecontrol.FileDiffResult{}, nil
}
func (s *stubFileService) BlamePath(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileBlameResult, error) {
	return &sourcecontrol.FileBlameResult{}, nil
}
func (s *stubFileService) PathHistory(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileHistoryResult, error) {
	return &sourcecontrol.FileHistoryResult{}, nil
}
func (s *stubFileService) WriteFile(_ context.Context, _ sourcecontrol.WriteCommand) (*sourcecontrol.FileMutationResult, error) {
	return &sourcecontrol.FileMutationResult{Success: true}, nil
}
func (s *stubFileService) DeletePath(_ context.Context, _ sourcecontrol.DeleteCommand) error {
	return nil
}
func (s *stubFileService) CreateDirectory(_ context.Context, _ sourcecontrol.CreateDirectoryCommand) error {
	return nil
}
func (s *stubFileService) MovePath(_ context.Context, _ sourcecontrol.MoveCommand) (*sourcecontrol.FileMutationResult, error) {
	return &sourcecontrol.FileMutationResult{Success: true}, nil
}
func (s *stubFileService) Push(context.Context, sourcecontrol.PushCommand) (*sourcecontrol.PushResult, error) {
	return &sourcecontrol.PushResult{Success: true}, nil
}
func (s *stubFileService) PushAll(context.Context, sourcecontrol.PushAllCommand) (*sourcecontrol.PushAllResult, error) {
	return &sourcecontrol.PushAllResult{}, nil
}
func (s *stubFileService) Pull(context.Context, sourcecontrol.PullCommand) (*sourcecontrol.PullResult, error) {
	return &sourcecontrol.PullResult{Success: true}, nil
}
func (s *stubFileService) Sync(context.Context, sourcecontrol.SyncCommand) (*sourcecontrol.SyncResult, error) {
	return &sourcecontrol.SyncResult{}, nil
}
func (s *stubFileService) CreatePullRequest(context.Context, sourcecontrol.CreatePullRequestCommand) (*sourcecontrol.PullRequestCreation, error) {
	return &sourcecontrol.PullRequestCreation{}, nil
}
func (s *stubFileService) ListPullRequests(context.Context, sourcecontrol.ListPullRequestsQuery) (*sourcecontrol.PullRequestList, error) {
	return &sourcecontrol.PullRequestList{}, nil
}
func (s *stubFileService) Reset(context.Context, sourcecontrol.ResetCommand) (*sourcecontrol.ResetResult, error) {
	return &sourcecontrol.ResetResult{}, nil
}
func (s *stubFileService) AgentStatus(context.Context, sourcecontrol.AgentStatusQuery) (*sourcecontrol.AgentStatusResult, error) {
	return &sourcecontrol.AgentStatusResult{}, nil
}
func (s *stubFileService) SetTargetBranch(context.Context, sourcecontrol.SetTargetBranchCommand) error {
	return nil
}
