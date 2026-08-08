package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

const maxHostBridgeArtifactFileBytes int64 = 64 << 20

func (r bridgeTaskRunnerResult) finalizedArtifacts() []bridgeArtifact {
	switch {
	case len(r.Artifacts) > 0:
		return r.Artifacts
	case len(r.ArtifactDescriptors) > 0:
		return r.ArtifactDescriptors
	case len(r.ArtifactDescriptorsAlt) > 0:
		return r.ArtifactDescriptorsAlt
	default:
		return nil
	}
}

func (e HostBridgeTaskExecutor) registerRunnerArtifacts(ctx context.Context, req TaskExecRequest, artifacts []bridgeArtifact, result TaskExecResult) (TaskExecResult, error) {
	if len(artifacts) == 0 {
		return result, nil
	}
	if e.Artifacts == nil {
		return TaskExecResult{}, fmt.Errorf("artifacts capability required for runner artifact registration: %w", artifactsmodule.ErrUnavailable)
	}
	artifactIDs := normalizeArtifactIDs(result.ArtifactIDs)
	for i, artifact := range artifacts {
		artifactID := firstNonEmpty(artifact.ArtifactID, artifact.ArtifactIDCamel, artifact.ID)
		if artifactID == "" {
			artifactID = taskRunAttemptArtifactID(req, fmt.Sprintf("artifact-%s-%d", req.TaskRunID, i+1))
		}
		uri := strings.TrimSpace(artifact.URI)
		if uri == "" {
			artifactIDs = normalizeArtifactIDs(append(artifactIDs, artifactID))
			continue
		}
		finalized, err := e.registerRunnerArtifact(ctx, req, artifact, artifactID, uri)
		if err != nil {
			return TaskExecResult{}, err
		}
		artifactIDs = normalizeArtifactIDs(append(artifactIDs, finalized.ArtifactID))
		if result.RuntimeMetadata == nil {
			result.RuntimeMetadata = map[string]string{}
		}
		result.RuntimeMetadata["artifact."+finalized.ArtifactID+".content_hash"] = finalized.ContentHash
	}
	result.ArtifactIDs = artifactIDs
	if result.ArtifactsRef == "" && len(artifactIDs) > 0 {
		result.ArtifactsRef = "artifacts://" + req.TaskRunID
	}
	return result, nil
}

func (e HostBridgeTaskExecutor) registerRunnerArtifact(ctx context.Context, req TaskExecRequest, artifact bridgeArtifact, artifactID, uri string) (*domain.Artifact, error) {
	metadata := runnerArtifactMetadataWithSource(req, artifact.Metadata)
	contentHash := firstNonEmpty(artifact.ContentHash, artifact.ContentHashCamel, artifact.Checksum)
	checksum := firstNonEmpty(artifact.Checksum, contentHash)
	mimeType := firstNonEmpty(artifact.MIMEType, artifact.MIMETypeCamel)
	sizeBytes := firstNonZeroInt64(artifact.SizeBytes, artifact.SizeBytesCamel)
	summary := artifact.Summary
	visibility := artifact.Visibility
	redactionStatus := firstNonEmpty(artifact.RedactionStatus, artifact.RedactionStatusAlt)
	service := e.Artifacts
	owner := artifactExecutionOwnerForTask(req)
	declareAuth, err := e.artifactAuthority(ctx, req, artifactsmodule.ActionDeclare)
	if err != nil {
		return nil, fmt.Errorf("authorize runner artifact %q declaration: %w", artifactID, err)
	}
	if _, err := service.Create(ctx, declareAuth, owner, artifactsmodule.CreateCommand{
		ArtifactID: artifactID, TaskID: req.TaskID, Type: firstNonEmpty(artifact.Type, "artifact"),
		Summary: summary, MIMEType: mimeType, Visibility: visibility,
		RedactionStatus: redactionStatus, Metadata: metadata,
	}); err != nil && !errors.Is(err, artifactsmodule.ErrAlreadyExists) {
		return nil, fmt.Errorf("create runner artifact %q: %w", artifactID, err)
	}
	finalizeAuth, err := e.artifactAuthority(ctx, req, artifactsmodule.ActionFinalize)
	if err != nil {
		return nil, fmt.Errorf("authorize runner artifact %q finalization: %w", artifactID, err)
	}
	_, err = service.Finalize(ctx, finalizeAuth, owner, artifactsmodule.FinalizeCommand{
		ArtifactID:      artifactID,
		URI:             &uri,
		Summary:         optionalString(summary),
		MIMEType:        optionalString(mimeType),
		SizeBytes:       optionalInt64(sizeBytes),
		Checksum:        optionalString(checksum),
		ContentHash:     optionalString(contentHash),
		Visibility:      optionalString(visibility),
		RedactionStatus: optionalString(redactionStatus),
		Metadata:        &metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize runner artifact %q: %w", artifactID, err)
	}
	referenceAuth, err := e.artifactAuthority(ctx, req, artifactsmodule.ActionReference)
	if err != nil {
		return nil, fmt.Errorf("authorize runner artifact %q reference: %w", artifactID, err)
	}
	referenced, err := service.Reference(ctx, referenceAuth, owner, taskOutputReference(req, artifactID))
	if err != nil {
		return nil, fmt.Errorf("reference runner artifact %q: %w", artifactID, err)
	}
	return artifactDomainFromModule(referenced.Artifact), nil
}

func runnerArtifactMetadataWithSource(req TaskExecRequest, source map[string]string) map[string]string {
	metadata := cloneStringMap(source)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["driver_run_id"] = req.DriverRunID
	metadata["runner"] = req.Runner
	metadata["runner_ref"] = req.RunnerRef
	metadata["runner_kind"] = req.RunnerKind
	metadata["runner_entrypoint"] = req.RunnerEntrypoint
	metadata["runner_driver_version_id"] = req.RunnerVersionID
	metadata["provider_profile"] = req.ProviderProfile
	metadata["task_run_attempt"] = strconv.Itoa(taskExecAttempt(req))
	return metadata
}

func (e HostBridgeTaskExecutor) persistRunnerOutputArtifacts(ctx context.Context, req TaskExecRequest, runner bridgeTaskRunnerResult, result TaskExecResult) (TaskExecResult, error) {
	if result.RuntimeMetadata == nil {
		result.RuntimeMetadata = map[string]string{}
	}
	if transcriptRef := firstNonEmpty(runner.TranscriptRef, runner.TranscriptRefCamel); transcriptRef != "" {
		result.RuntimeMetadata["transcript_ref"] = transcriptRef
	}
	transcriptContent, err := e.runnerFileOrInlineBytes(runner.transcriptInline(), firstNonEmpty(runner.TranscriptPath, runner.TranscriptPathCamel), "transcript")
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(transcriptContent) > 0 && result.RuntimeMetadata["transcript_ref"] == "" {
		artifactID := taskRunAttemptArtifactID(req, "transcript-"+req.TaskRunID)
		finalized, err := e.createContentArtifact(ctx, req, artifactID, "transcript", "task run transcript", "application/x-ndjson", transcriptContent)
		if err != nil {
			return TaskExecResult{}, err
		}
		result.ArtifactIDs = normalizeArtifactIDs(append(result.ArtifactIDs, finalized.ArtifactID))
		result.RuntimeMetadata["transcript_ref"] = "artifact://" + finalized.ArtifactID
		result.RuntimeMetadata["transcript_artifact_id"] = finalized.ArtifactID
	}
	logContent, err := e.runnerFileOrInlineBytes(runner.logsInline(), firstNonEmpty(runner.LogsPath, runner.LogsPathCamel), "logs")
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(logContent) > 0 && result.LogsRef == "" {
		artifactID := taskRunAttemptArtifactID(req, "logs-"+req.TaskRunID)
		finalized, err := e.createContentArtifact(ctx, req, artifactID, "logs", "task run logs", "text/plain; charset=utf-8", logContent)
		if err != nil {
			return TaskExecResult{}, err
		}
		result.ArtifactIDs = normalizeArtifactIDs(append(result.ArtifactIDs, finalized.ArtifactID))
		result.LogsRef = "artifact://" + finalized.ArtifactID
		result.RuntimeMetadata["logs_artifact_id"] = finalized.ArtifactID
	}
	if result.LogsRef != "" {
		result.RuntimeMetadata["logs_ref"] = result.LogsRef
	}
	if result.ArtifactsRef == "" && len(result.ArtifactIDs) > 0 {
		result.ArtifactsRef = "artifacts://" + req.TaskRunID
	}
	return result, nil
}

func (r bridgeTaskRunnerResult) transcriptInline() []byte {
	switch {
	case len(r.TranscriptEntries) > 0:
		return marshalTranscriptJSONL(r.TranscriptEntries)
	case len(r.TranscriptEntriesCamel) > 0:
		return marshalTranscriptJSONL(r.TranscriptEntriesCamel)
	case len(r.TranscriptEvents) > 0:
		return marshalTranscriptJSONL(r.TranscriptEvents)
	case len(r.TranscriptEventsCamel) > 0:
		return marshalTranscriptJSONL(r.TranscriptEventsCamel)
	case strings.TrimSpace(r.Transcript) != "":
		return []byte(r.Transcript)
	default:
		return nil
	}
}

func (r bridgeTaskRunnerResult) logsInline() []byte {
	if strings.TrimSpace(r.Logs) == "" {
		return nil
	}
	return []byte(r.Logs)
}

func marshalTranscriptJSONL(events []transcript.Event) []byte {
	var out bytes.Buffer
	for _, event := range events {
		if err := json.NewEncoder(&out).Encode(event); err != nil {
			return nil
		}
	}
	return out.Bytes()
}

func (e HostBridgeTaskExecutor) runnerFileOrInlineBytes(inline []byte, filePath, label string) ([]byte, error) {
	if len(inline) > 0 {
		return inline, nil
	}
	if strings.TrimSpace(filePath) == "" {
		return nil, nil
	}
	path, err := e.safeRunnerPath(filePath, label)
	if err != nil {
		return nil, err
	}
	content, err := readHostBridgeArtifactFile(path, label)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func readHostBridgeArtifactFile(path, label string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // caller constrains the path to the task worktree.
	if err != nil {
		return nil, fmt.Errorf("read %s file: %w", label, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s file: %w", label, err)
	}
	if info.Size() > maxHostBridgeArtifactFileBytes {
		return nil, fmt.Errorf("%s file exceeds %d-byte artifact limit: %w", label, maxHostBridgeArtifactFileBytes, domain.ErrInvalid)
	}

	content, err := io.ReadAll(io.LimitReader(file, maxHostBridgeArtifactFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s file: %w", label, err)
	}
	if int64(len(content)) > maxHostBridgeArtifactFileBytes {
		return nil, fmt.Errorf("%s file exceeds %d-byte artifact limit: %w", label, maxHostBridgeArtifactFileBytes, domain.ErrInvalid)
	}
	return content, nil
}

func (e HostBridgeTaskExecutor) createContentArtifact(ctx context.Context, req TaskExecRequest, artifactID, artifactType, summary, mimeType string, content []byte) (*domain.Artifact, error) {
	if e.Artifacts == nil {
		return nil, fmt.Errorf("artifacts capability required for %s artifact finalization: %w", artifactType, artifactsmodule.ErrUnavailable)
	}
	authorities, err := e.artifactContentAuthorities(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("authorize %s artifact finalization: %w", artifactType, err)
	}
	result, err := e.Artifacts.CreateContent(ctx, authorities, artifactExecutionOwnerForTask(req), artifactsmodule.CreateCommand{
		ArtifactID: artifactID, TaskID: req.TaskID, Type: artifactType,
		Summary: summary, MIMEType: mimeType, Metadata: runnerArtifactMetadata(req),
	}, content, taskOutputReference(req, artifactID))
	if err != nil {
		return nil, artifactDomainErrorForBridge(err)
	}
	return artifactDomainFromModule(result.Artifact), nil
}

// createPatchArtifact routes patch persistence through the Artifacts-owned
// retry-safe content lifecycle. The execution owner tuple is validated before
// the compatibility adapter can touch the legacy store.
func (e HostBridgeTaskExecutor) createPatchArtifact(ctx context.Context, req TaskExecRequest, runner bridgeTaskRunnerResult, patch []byte) (*domain.Artifact, string, error) {
	if e.Artifacts == nil {
		return nil, "", fmt.Errorf("artifacts capability required for patch artifact finalization: %w", artifactsmodule.ErrUnavailable)
	}
	artifactID := firstNonEmpty(runner.PatchArtifactID, runner.PatchArtifactIDCamel)
	if artifactID == "" {
		artifactID = taskRunAttemptArtifactID(req, "patch-"+req.TaskRunID)
	}
	summary := firstNonEmpty(runner.PatchSummary, runner.PatchSummaryCamel, "task patch")
	mimeType := firstNonEmpty(runner.PatchMIMEType, runner.PatchMIMETypeCamel, "text/x-diff")
	baseRef := firstNonEmpty(runner.PatchBaseRef, runner.PatchBaseRefCamel, runner.BaseRef, runner.BaseRefCamel)
	metadata := runnerArtifactMetadata(req)
	if baseRef != "" {
		metadata["patch_base_ref"] = baseRef
	}
	authorities, err := e.artifactContentAuthorities(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("authorize patch artifact finalization: %w", err)
	}
	result, err := e.Artifacts.CreateContent(ctx, authorities, artifactExecutionOwnerForTask(req), artifactsmodule.CreateCommand{
		ArtifactID:      artifactID,
		TaskID:          req.TaskID,
		Type:            "patch",
		Summary:         summary,
		MIMEType:        mimeType,
		Visibility:      firstNonEmpty(runner.PatchVisibility, runner.PatchVisibilityCamel),
		RedactionStatus: firstNonEmpty(runner.PatchRedactionStatus, runner.PatchRedactionStatusAlt),
		Metadata:        metadata,
	}, patch, taskOutputReference(req, artifactID))
	if err != nil {
		return nil, "", fmt.Errorf("create patch artifact: %w", artifactDomainErrorForBridge(err))
	}
	return artifactDomainFromModule(result.Artifact), baseRef, nil
}

func (e HostBridgeTaskExecutor) artifactAuthority(
	ctx context.Context,
	req TaskExecRequest,
	action authority.Action,
) (authority.ExecutionAuthority, error) {
	if e.ArtifactAuthorities == nil {
		return authority.ExecutionAuthority{}, fmt.Errorf("TaskRun authority resolver required: %w", artifactsmodule.ErrUnavailable)
	}
	return e.ArtifactAuthorities.ResolveTaskRunAuthority(ctx, req.WorkspaceKey, action, execution.Owner{
		ResourceKind: execution.ResourceTaskRun,
		ResourceID:   req.TaskRunID,
		NodeID:       req.NodeID,
		LeaseID:      req.LeaseID,
		LeaseToken:   req.LeaseToken,
		FencingToken: req.FencingToken,
	})
}

func (e HostBridgeTaskExecutor) artifactContentAuthorities(ctx context.Context, req TaskExecRequest) (artifactsmodule.ContentAuthorities, error) {
	var result artifactsmodule.ContentAuthorities
	actions := []struct {
		action authority.Action
		assign func(authority.ExecutionAuthority)
	}{
		{artifactsmodule.ActionDeclare, func(value authority.ExecutionAuthority) { result.Declare = value }},
		{artifactsmodule.ActionGet, func(value authority.ExecutionAuthority) { result.Get = value }},
		{artifactsmodule.ActionUpload, func(value authority.ExecutionAuthority) { result.Upload = value }},
		{artifactsmodule.ActionFinalize, func(value authority.ExecutionAuthority) { result.Finalize = value }},
		{artifactsmodule.ActionReference, func(value authority.ExecutionAuthority) { result.Reference = value }},
	}
	for _, operation := range actions {
		value, err := e.artifactAuthority(ctx, req, operation.action)
		if err != nil {
			return artifactsmodule.ContentAuthorities{}, err
		}
		operation.assign(value)
	}
	return result, nil
}

func taskOutputReference(req TaskExecRequest, artifactID string) artifactsmodule.ReferenceCommand {
	return artifactsmodule.ReferenceCommand{
		ArtifactID: artifactID,
		Kind:       "task-output",
		TargetRef:  "task-run://" + req.TaskRunID + "/output",
	}
}

func artifactExecutionOwnerForTask(req TaskExecRequest) artifactsmodule.ExecutionOwner {
	return artifactsmodule.ExecutionOwner{
		WorkspaceKey: req.WorkspaceKey,
		TaskRunID:    req.TaskRunID,
		NodeID:       req.NodeID,
		LeaseID:      req.LeaseID,
		LeaseToken:   req.LeaseToken,
		FencingToken: req.FencingToken,
	}
}

func artifactDomainFromModule(artifact *artifactsmodule.Artifact) *domain.Artifact {
	if artifact == nil {
		return nil
	}
	return &domain.Artifact{
		WorkspaceKey: artifact.WorkspaceKey, ArtifactID: artifact.ArtifactID, SessionID: artifact.SessionID,
		TaskID: artifact.TaskID, OwnerType: string(artifact.OwnerType), OwnerID: artifact.OwnerID,
		Type: artifact.Type, URI: artifact.URI, Summary: artifact.Summary, MIMEType: artifact.MIMEType,
		SizeBytes: artifact.SizeBytes, Checksum: artifact.Checksum, ContentHash: artifact.ContentHash,
		Visibility: artifact.Visibility, RedactionStatus: artifact.RedactionStatus,
		DurableStatus: string(artifact.DurableStatus), Metadata: artifact.Metadata,
		FinalizedAt: artifact.FinalizedAt, CreatedAt: artifact.CreatedAt, UpdatedAt: artifact.UpdatedAt,
	}
}

func artifactDomainErrorForBridge(err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, artifactsmodule.ErrNotFound):
		mapped = domain.ErrNotFound
	case errors.Is(err, artifactsmodule.ErrAlreadyExists):
		mapped = domain.ErrAlreadyExists
	case errors.Is(err, artifactsmodule.ErrNotOwner):
		mapped = domain.ErrNotOwner
	case errors.Is(err, artifactsmodule.ErrInvalidTransition):
		mapped = domain.ErrInvalidTransition
	case errors.Is(err, artifactsmodule.ErrInvalid):
		mapped = domain.ErrInvalid
	default:
		return err
	}
	return errors.Join(mapped, err)
}

func runnerArtifactMetadata(req TaskExecRequest) map[string]string {
	return map[string]string{
		"driver_run_id":            req.DriverRunID,
		"runner":                   req.Runner,
		"runner_ref":               req.RunnerRef,
		"runner_kind":              req.RunnerKind,
		"runner_entrypoint":        req.RunnerEntrypoint,
		"runner_driver_version_id": req.RunnerVersionID,
		"provider_profile":         req.ProviderProfile,
		"runtime":                  "flue",
		"task_run_id":              req.TaskRunID,
		"task_run_attempt":         strconv.Itoa(taskExecAttempt(req)),
	}
}

func taskExecAttempt(req TaskExecRequest) int {
	if req.TaskRunAttempt < 1 {
		return 1
	}
	return req.TaskRunAttempt
}

// taskRunAttemptArtifactID preserves the long-standing first-attempt identity
// used by transcript links while assigning each retry its own immutable
// artifact. Replaying the same attempt remains idempotent; a later attempt can
// persist different evidence without colliding with the failed attempt.
func taskRunAttemptArtifactID(req TaskExecRequest, baseID string) string {
	attempt := taskExecAttempt(req)
	if attempt == 1 {
		return baseID
	}
	return fmt.Sprintf("%s-attempt-%d", baseID, attempt)
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

// taskRunnerBaseEnvForRequest selects the subprocess base env for a task runner
// by entrypoint: the local task runner gets the trusted-local provider-cred
// superset (§4.3); every other runner (Daytona/remote/node-module) keeps the
// strict filter so credentials never leak into a remote sandbox.
func taskRunnerBaseEnvForRequest(req TaskExecRequest, env []string) []string {
	if isLocalTaskRunner(req) {
		return platformruntime.FilterSubprocessEnv(platformruntime.SubprocessEnvDriverLocalTaskRunner, env)
	}
	return platformruntime.FilterSubprocessEnv(platformruntime.SubprocessEnvDriverRemote, env)
}

func lastJSONLine(stdout []byte) ([]byte, error) {
	lines := bytes.Split(bytes.TrimSpace(stdout), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		return line, nil
	}
	return nil, fmt.Errorf("task runner command returned empty output: %w", domain.ErrInvalid)
}

func (r bridgeTaskRunnerResult) taskExecResult() TaskExecResult {
	exitCode := 0
	if r.ExitCode != nil {
		exitCode = *r.ExitCode
	} else if r.ExitCodeCamel != nil {
		exitCode = *r.ExitCodeCamel
	}
	return TaskExecResult{
		Status:           r.Status,
		ExitCode:         exitCode,
		LogsRef:          firstNonEmpty(r.LogsRef, r.LogsRefCamel),
		ArtifactsRef:     firstNonEmpty(r.ArtifactsRef, r.ArtifactsRefCamel),
		ArtifactIDs:      normalizeArtifactIDs(firstNonNilStrings(r.ArtifactIDs, r.ArtifactIDsCamel)),
		InputTokens:      firstNonZeroInt64(r.InputTokens, r.InputTokensCamel),
		OutputTokens:     firstNonZeroInt64(r.OutputTokens, r.OutputTokensCamel),
		CacheReadTokens:  firstNonZeroInt64(r.CacheReadTokens, r.CacheReadTokensCamel),
		CacheWriteTokens: firstNonZeroInt64(r.CacheWriteTokens, r.CacheWriteTokensCamel),
		EstimatedCostUSD: firstNonZeroFloat64(r.EstimatedCostUSD, r.EstimatedCostUSDCamel),
		RuntimeMetadata:  cloneStringMap(firstNonNilMap(r.RuntimeMetadata, r.RuntimeMetadataCamel)),
		ErrorClass:       firstNonEmpty(r.ErrorClass, r.ErrorClassCamel),
		ErrorMessage:     firstNonEmpty(r.ErrorMessage, r.ErrorMessageCamel),
	}
}

func (e HostBridgeTaskExecutor) readPatch(ctx context.Context, r bridgeTaskRunnerResult) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Patch != "" {
		return []byte(r.Patch), nil
	}
	patchPath := firstNonEmpty(r.PatchPath, r.PatchPathCamel)
	if patchPath == "" {
		return nil, nil
	}
	path, err := e.safePatchPath(patchPath)
	if err != nil {
		return nil, err
	}
	patch, err := readHostBridgeArtifactFile(path, "patch")
	if err != nil {
		return nil, err
	}
	return patch, ctx.Err()
}

func (e HostBridgeTaskExecutor) safePatchPath(patchPath string) (string, error) {
	return e.safeRunnerPath(patchPath, "patch")
}

func (e HostBridgeTaskExecutor) safeRunnerPath(rawPath, label string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("%s path required: %w", label, domain.ErrInvalid)
	}
	if filepath.IsAbs(rawPath) {
		return rawPath, nil
	}
	root := strings.TrimSpace(e.WorktreePath)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	path := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(rawPath)))
	rel, err := filepath.Rel(absRoot, path)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", label, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path escapes worktree: %w", label, domain.ErrInvalid)
	}
	return path, nil
}
