package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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
	if e.Store == nil {
		return TaskExecResult{}, fmt.Errorf("store required for runner artifact registration: %w", domain.ErrInvalid)
	}
	artifactIDs := normalizeArtifactIDs(result.ArtifactIDs)
	for i, artifact := range artifacts {
		artifactID := firstNonEmpty(artifact.ArtifactID, artifact.ArtifactIDCamel, artifact.ID)
		if artifactID == "" {
			artifactID = fmt.Sprintf("artifact-%s-%d", req.TaskRunID, i+1)
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
	metadata := cloneStringMap(artifact.Metadata)
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
	contentHash := firstNonEmpty(artifact.ContentHash, artifact.ContentHashCamel, artifact.Checksum)
	checksum := firstNonEmpty(artifact.Checksum, contentHash)
	mimeType := firstNonEmpty(artifact.MIMEType, artifact.MIMETypeCamel)
	sizeBytes := firstNonZeroInt64(artifact.SizeBytes, artifact.SizeBytesCamel)
	summary := artifact.Summary
	visibility := artifact.Visibility
	redactionStatus := firstNonEmpty(artifact.RedactionStatus, artifact.RedactionStatusAlt)
	if _, err := e.Store.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:    req.WorkspaceKey,
		ArtifactID:      artifactID,
		TaskID:          req.TaskID,
		OwnerType:       "task_run",
		OwnerID:         req.TaskRunID,
		Type:            firstNonEmpty(artifact.Type, "artifact"),
		Summary:         summary,
		MIMEType:        mimeType,
		Visibility:      visibility,
		RedactionStatus: redactionStatus,
		DurableStatus:   "declared",
		Metadata:        metadata,
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return nil, fmt.Errorf("create runner artifact %q: %w", artifactID, err)
	}
	finalized, err := e.Store.Artifacts().Finalize(ctx, req.WorkspaceKey, artifactID, store.ArtifactFinalize{
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
	return finalized, nil
}

func (e HostBridgeTaskExecutor) persistRunnerOutputArtifacts(ctx context.Context, req TaskExecRequest, session *flueTaskSession, runner bridgeTaskRunnerResult, result TaskExecResult) (TaskExecResult, error) {
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
		artifactID := "transcript-" + req.TaskRunID
		finalized, err := e.createContentArtifact(ctx, req, sessionIDFromFlueTaskSession(session), artifactID, "transcript", "task run transcript", "application/x-ndjson", transcriptContent)
		if err != nil {
			return TaskExecResult{}, err
		}
		result.ArtifactIDs = normalizeArtifactIDs(append(result.ArtifactIDs, finalized.ArtifactID))
		result.RuntimeMetadata["transcript_ref"] = "artifact://" + finalized.ArtifactID
		result.RuntimeMetadata["transcript_artifact_id"] = finalized.ArtifactID
	}
	if result.RuntimeMetadata["transcript_ref"] != "" && session != nil {
		session.Metadata["transcript_ref"] = result.RuntimeMetadata["transcript_ref"]
	}

	logContent, err := e.runnerFileOrInlineBytes(runner.logsInline(), firstNonEmpty(runner.LogsPath, runner.LogsPathCamel), "logs")
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(logContent) > 0 && result.LogsRef == "" {
		artifactID := "logs-" + req.TaskRunID
		finalized, err := e.createContentArtifact(ctx, req, sessionIDFromFlueTaskSession(session), artifactID, "logs", "task run logs", "text/plain; charset=utf-8", logContent)
		if err != nil {
			return TaskExecResult{}, err
		}
		result.ArtifactIDs = normalizeArtifactIDs(append(result.ArtifactIDs, finalized.ArtifactID))
		result.LogsRef = "artifact://" + finalized.ArtifactID
		result.RuntimeMetadata["logs_artifact_id"] = finalized.ArtifactID
	}
	if result.LogsRef != "" {
		result.RuntimeMetadata["logs_ref"] = result.LogsRef
		if session != nil {
			session.Metadata["logs_ref"] = result.LogsRef
		}
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
	content, err := os.ReadFile(path) //nolint:gosec // constrained by safeRunnerPath.
	if err != nil {
		return nil, fmt.Errorf("read %s file: %w", label, err)
	}
	return content, nil
}

func (e HostBridgeTaskExecutor) createContentArtifact(ctx context.Context, req TaskExecRequest, sessionID, artifactID, artifactType, summary, mimeType string, content []byte) (*domain.Artifact, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("store required for %s artifact finalization: %w", artifactType, domain.ErrInvalid)
	}
	return store.UploadContentArtifact(ctx, e.Store.Artifacts(), store.ArtifactCreate{
		WorkspaceKey:  req.WorkspaceKey,
		ArtifactID:    artifactID,
		SessionID:     sessionID,
		TaskID:        req.TaskID,
		OwnerType:     "task_run",
		OwnerID:       req.TaskRunID,
		Type:          artifactType,
		Summary:       summary,
		MIMEType:      mimeType,
		DurableStatus: "declared",
		Metadata:      runnerArtifactMetadata(req),
	}, content)
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
	}
}

func sessionIDFromFlueTaskSession(session *flueTaskSession) string {
	if session == nil {
		return ""
	}
	return session.SessionID
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

func taskRunnerBaseEnv(env []string) []string {
	return scopedSubprocessBaseEnv(env)
}

// taskRunnerBaseEnvForRequest selects the subprocess base env for a task runner
// by entrypoint: the local task runner gets the trusted-local provider-cred
// superset (§4.3); every other runner (Daytona/remote/node-module) keeps the
// strict filter so credentials never leak into a remote sandbox.
func taskRunnerBaseEnvForRequest(req TaskExecRequest, env []string) []string {
	if isLocalTaskRunner(req) {
		return localTaskRunnerBaseEnv(env)
	}
	return taskRunnerBaseEnv(env)
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
	patch, err := os.ReadFile(path) //nolint:gosec // constrained by safePatchPath.
	if err != nil {
		return nil, fmt.Errorf("read patch file: %w", err)
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
