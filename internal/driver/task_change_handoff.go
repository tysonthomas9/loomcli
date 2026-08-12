package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type changeHandoffOutcome string

const (
	changeHandoffNoChange             changeHandoffOutcome = "no_change"
	changeHandoffContinuationRequired changeHandoffOutcome = "continuation_required"
	changeHandoffReadyToReview        changeHandoffOutcome = "ready_to_review"
)

type changedRepository struct {
	Name       string
	Path       string
	BranchName string
	BaseSHA    string
	HeadSHA    string
}

type compositeCommitInspection struct {
	Outcome           changeHandoffOutcome
	Repositories      []changedRepository
	DirtyRepositories []string
}

type CompletionClaim struct {
	Request      TaskExecRequest
	Inspection   compositeCommitInspection
	ArtifactRefs []string
}

type CompletionOutcome struct {
	Outcome   changeHandoffOutcome
	ChangeSet *domain.TaskChangeSet
}

type TaskCompletionFinalizer interface {
	Finalize(ctx context.Context, claim CompletionClaim) (CompletionOutcome, error)
}

type GitPushProxy struct {
	Recorder         store.TaskChangeHandoffStore
	LocalSettingsDir string
}

func (p GitPushProxy) Finalize(ctx context.Context, claim CompletionClaim) (CompletionOutcome, error) {
	req, inspection, artifactRefs := claim.Request, claim.Inspection, claim.ArtifactRefs
	if inspection.Outcome == changeHandoffNoChange {
		return CompletionOutcome{Outcome: changeHandoffNoChange}, nil
	}
	if inspection.Outcome != changeHandoffReadyToReview || len(inspection.Repositories) == 0 {
		return CompletionOutcome{}, fmt.Errorf("change handoff is not publishable: %s", inspection.Outcome)
	}
	if p.Recorder == nil {
		return CompletionOutcome{}, fmt.Errorf("Task Change Set recorder is required")
	}
	token := p.githubToken()
	entries := make([]domain.TaskChangeSetEntry, 0, len(inspection.Repositories))
	for _, repository := range inspection.Repositories {
		existing, err := p.Recorder.GetTaskBranch(ctx, req.WorkspaceKey, req.TaskID, repository.Name)
		if err == nil && existing.ConfirmedRemoteHeadSHA == repository.HeadSHA {
			entries = append(entries, changeSetEntry(repository, artifactRefs))
			continue
		}
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return CompletionOutcome{}, fmt.Errorf("read Task Branch %s: %w", repository.Name, err)
		}
		if _, err := p.Recorder.PutTaskBranch(ctx, domain.TaskBranch{
			WorkspaceKey: req.WorkspaceKey, TaskID: req.TaskID, RepoName: repository.Name,
			BranchName: repository.BranchName, AdmittedBaseSHA: repository.BaseSHA,
			ExpectedRemoteHeadSHA: repository.HeadSHA,
		}); err != nil {
			return CompletionOutcome{}, fmt.Errorf("record expected Task Branch %s: %w", repository.Name, err)
		}
		if err := pushAndVerifyRepository(ctx, repository, "origin", token); err != nil {
			return CompletionOutcome{}, fmt.Errorf("publish repository %s: %w", repository.Name, err)
		}
		if _, err := p.Recorder.PutTaskBranch(ctx, domain.TaskBranch{
			WorkspaceKey: req.WorkspaceKey, TaskID: req.TaskID, RepoName: repository.Name,
			BranchName: repository.BranchName, AdmittedBaseSHA: repository.BaseSHA,
			ExpectedRemoteHeadSHA: repository.HeadSHA, ConfirmedRemoteHeadSHA: repository.HeadSHA,
		}); err != nil {
			return CompletionOutcome{}, fmt.Errorf("confirm Task Branch %s: %w", repository.Name, err)
		}
		entries = append(entries, changeSetEntry(repository, artifactRefs))
	}
	changeSet := domain.TaskChangeSet{WorkspaceKey: req.WorkspaceKey, TaskID: req.TaskID, Version: 1, Entries: entries}
	if existing, err := p.Recorder.GetTaskChangeSet(ctx, req.WorkspaceKey, req.TaskID, 1); err == nil {
		if taskChangeSetEntriesEqual(existing.Entries, entries) {
			return CompletionOutcome{Outcome: changeHandoffReadyToReview, ChangeSet: existing}, nil
		}
		return CompletionOutcome{}, fmt.Errorf("Task Change Set version 1 already exists with different entries")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return CompletionOutcome{}, fmt.Errorf("read Task Change Set version 1: %w", err)
	}
	created, err := p.Recorder.CreateTaskChangeSet(ctx, changeSet)
	if err != nil {
		return CompletionOutcome{}, fmt.Errorf("create Task Change Set version 1: %w", err)
	}
	return CompletionOutcome{Outcome: changeHandoffReadyToReview, ChangeSet: created}, nil
}

func taskChangeSetEntriesEqual(left, right []domain.TaskChangeSetEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a.RepoName != b.RepoName || a.BaseSHA != b.BaseSHA || a.HeadSHA != b.HeadSHA || a.BranchName != b.BranchName || a.RemoteName != b.RemoteName || a.PublicationStatus != b.PublicationStatus {
			return false
		}
		if strings.Join(a.ArtifactRefs, "\x00") != strings.Join(b.ArtifactRefs, "\x00") {
			return false
		}
	}
	return true
}

func changeSetEntry(repository changedRepository, artifactRefs []string) domain.TaskChangeSetEntry {
	return domain.TaskChangeSetEntry{
		RepoName: repository.Name, BaseSHA: repository.BaseSHA, HeadSHA: repository.HeadSHA,
		BranchName: repository.BranchName, RemoteName: "origin",
		PublicationStatus: domain.TaskChangePublicationConfirmed,
		ArtifactRefs:      append([]string(nil), artifactRefs...),
	}
}

func (p GitPushProxy) githubToken() string {
	if token := firstNonEmpty(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	dir := strings.TrimSpace(p.LocalSettingsDir)
	if dir == "" {
		return ""
	}
	settings, err := runtimesettings.Load(dir)
	if err != nil {
		return ""
	}
	token, err := runtimesettings.UnsealRuntimeCredential(dir, settings, runtimesettings.RuntimeCredentialProviderGitHub)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

func pushAndVerifyRepository(ctx context.Context, repository changedRepository, remote, token string) error {
	if _, err := proxyGit(ctx, repository.Path, token, "push", "--porcelain", remote, repository.HeadSHA+":refs/heads/"+repository.BranchName); err != nil {
		return err
	}
	output, err := proxyGit(ctx, repository.Path, token, "ls-remote", "--heads", remote, "refs/heads/"+repository.BranchName)
	if err != nil {
		return err
	}
	fields := strings.Fields(output)
	if len(fields) < 1 || fields[0] != repository.HeadSHA {
		return fmt.Errorf("remote verification returned %q, expected %s", output, repository.HeadSHA)
	}
	return nil
}

func proxyGit(ctx context.Context, directory, token string, args ...string) (string, error) {
	commandArgs := append([]string(nil), args...)
	var askpassPath string
	if token != "" {
		askpass, err := os.CreateTemp("", "loom-git-askpass-*.sh")
		if err != nil {
			return "", err
		}
		askpassPath = askpass.Name()
		if _, err := askpass.WriteString("#!/bin/sh\ncase \"$1\" in *Username*) printf '%s\\n' x-access-token ;; *) printf '%s\\n' \"$LOOM_GIT_PUSH_TOKEN\" ;; esac\n"); err != nil {
			askpass.Close()
			_ = os.Remove(askpassPath)
			return "", err
		}
		if err := askpass.Chmod(0o700); err != nil {
			askpass.Close()
			_ = os.Remove(askpassPath)
			return "", err
		}
		if err := askpass.Close(); err != nil {
			_ = os.Remove(askpassPath)
			return "", err
		}
		defer os.Remove(askpassPath)
		commandArgs = append([]string{"-c", "credential.helper="}, commandArgs...)
	}
	command := exec.CommandContext(ctx, "git", commandArgs...) //nolint:gosec // fixed binary and argv-only invocation.
	command.Dir = directory
	command.Env = os.Environ()
	if askpassPath != "" {
		command.Env = append(command.Env, "GIT_ASKPASS="+askpassPath, "GIT_TERMINAL_PROMPT=0", "LOOM_GIT_PUSH_TOKEN="+token)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func inspectCompositeCommits(ctx context.Context, root TaskRoot) (compositeCommitInspection, error) {
	inspection := compositeCommitInspection{Outcome: changeHandoffNoChange}
	repositories := append([]TaskRootRepository(nil), root.Repositories...)
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].Name < repositories[j].Name })
	for _, repository := range repositories {
		status, err := taskChangeGit(ctx, repository.Path, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			return compositeCommitInspection{}, fmt.Errorf("inspect repository %q status: %w", repository.Name, err)
		}
		if status != "" {
			inspection.DirtyRepositories = append(inspection.DirtyRepositories, repository.Name)
			continue
		}
		branch, err := taskChangeGit(ctx, repository.Path, "branch", "--show-current")
		if err != nil || branch != repository.BranchName {
			return compositeCommitInspection{}, fmt.Errorf("repository %q is on branch %q, expected %q", repository.Name, branch, repository.BranchName)
		}
		head, err := taskChangeGit(ctx, repository.Path, "rev-parse", "HEAD^{commit}")
		if err != nil {
			return compositeCommitInspection{}, fmt.Errorf("inspect repository %q head: %w", repository.Name, err)
		}
		if head == repository.BaseSHA {
			continue
		}
		if _, err := taskChangeGit(ctx, repository.Path, "merge-base", "--is-ancestor", repository.BaseSHA, head); err != nil {
			return compositeCommitInspection{}, fmt.Errorf("repository %q head %s does not descend from admitted base %s", repository.Name, head, repository.BaseSHA)
		}
		inspection.Repositories = append(inspection.Repositories, changedRepository{
			Name: repository.Name, Path: repository.Path, BranchName: repository.BranchName,
			BaseSHA: repository.BaseSHA, HeadSHA: head,
		})
	}
	if len(inspection.DirtyRepositories) > 0 {
		inspection.Outcome = changeHandoffContinuationRequired
	} else if len(inspection.Repositories) > 0 {
		inspection.Outcome = changeHandoffReadyToReview
	}
	return inspection, nil
}

func validateImmutableReviewRoot(ctx context.Context, root TaskRoot) (map[string]string, error) {
	metadata := map[string]string{}
	for _, repository := range root.Repositories {
		status, err := taskChangeGit(ctx, repository.Path, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			return nil, err
		}
		if status != "" {
			return nil, fmt.Errorf("review repository %q was modified", repository.Name)
		}
		head, err := taskChangeGit(ctx, repository.Path, "rev-parse", "HEAD^{commit}")
		if err != nil {
			return nil, err
		}
		if head != repository.BaseSHA {
			return nil, fmt.Errorf("review repository %q observed head %s, expected immutable head %s", repository.Name, head, repository.BaseSHA)
		}
		metadata["review.repository."+repository.Name+".observed_head_sha"] = head
	}
	metadata["review.repository_count"] = fmt.Sprintf("%d", len(root.Repositories))
	return metadata, nil
}

func taskChangeGit(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed binary and argv-only invocation.
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func compositeCommitContinuationPrompt(dirtyRepositories []string) string {
	return "Your completion claim was rejected because these repositories still contain uncommitted changes: " +
		strings.Join(dirtyRepositories, ", ") +
		". Continue this same task session in the same TaskRun Root. Review the changes, run appropriate validation, and author commits on each existing Task Branch. Do not push and do not create pull requests; Loom owns publication."
}

func compositeCommitMetadata(inspection compositeCommitInspection, runner bridgeTaskRunnerResult, continuationCount int) map[string]string {
	metadata := map[string]string{
		"change_handoff_outcome":     string(inspection.Outcome),
		"backend_continuation_count": fmt.Sprintf("%d", continuationCount),
	}
	if sessionRef := firstNonEmpty(runner.SessionID, runner.SessionIDCamel); sessionRef != "" {
		metadata["backend_session_ref"] = sessionRef
	}
	for _, repository := range inspection.Repositories {
		prefix := "repository." + repository.Name + "."
		metadata[prefix+"base_sha"] = repository.BaseSHA
		metadata[prefix+"head_sha"] = repository.HeadSHA
		metadata[prefix+"branch_name"] = repository.BranchName
	}
	return metadata
}

func mergeBridgeContinuation(previous, continued bridgeTaskRunnerResult) bridgeTaskRunnerResult {
	if continued.SessionID == "" && continued.SessionIDCamel == "" {
		continued.SessionID = firstNonEmpty(previous.SessionID, previous.SessionIDCamel)
	}
	continued.Logs = strings.TrimSpace(previous.Logs) + "\n" + strings.TrimSpace(continued.Logs)
	continued.RuntimeMetadata = mergeStringMaps(
		mergeStringMaps(previous.RuntimeMetadata, previous.RuntimeMetadataCamel),
		mergeStringMaps(continued.RuntimeMetadata, continued.RuntimeMetadataCamel),
	)
	events := append([]transcript.Event(nil), firstBridgeTranscriptEvents(previous)...)
	next := firstBridgeTranscriptEvents(continued)
	for _, event := range next {
		event.Seq = len(events) + 1
		events = append(events, event)
	}
	continued.TranscriptEntries = events
	continued.TranscriptEntriesCamel = nil
	continued.TranscriptEvents = nil
	continued.TranscriptEventsCamel = nil
	continued.InputTokens += previous.InputTokens + previous.InputTokensCamel
	continued.OutputTokens += previous.OutputTokens + previous.OutputTokensCamel
	continued.CacheReadTokens += previous.CacheReadTokens + previous.CacheReadTokensCamel
	continued.CacheWriteTokens += previous.CacheWriteTokens + previous.CacheWriteTokensCamel
	continued.EstimatedCostUSD += previous.EstimatedCostUSD + previous.EstimatedCostUSDCamel
	return continued
}

func firstBridgeTranscriptEvents(result bridgeTaskRunnerResult) []transcript.Event {
	for _, events := range [][]transcript.Event{
		result.TranscriptEntries, result.TranscriptEntriesCamel,
		result.TranscriptEvents, result.TranscriptEventsCamel,
	} {
		if len(events) > 0 {
			return events
		}
	}
	return nil
}
