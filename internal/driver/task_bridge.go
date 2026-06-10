package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	TaskRunnerCommandJSONEnv = "LOOM_DRIVER_TASK_RUNNER_CMD_JSON"
	TaskRunnerCommandEnv     = "LOOM_DRIVER_TASK_RUNNER_CMD"
)

type HostBridgeTaskExecutor struct {
	Store        store.Store
	WorktreePath string
	Command      []string
	Fallback     TaskExecutor
}

type bridgeTaskRunnerResult struct {
	Status                  domain.TaskRunStatus `json:"status"`
	ExitCode                *int                 `json:"exit_code"`
	ExitCodeCamel           *int                 `json:"exitCode"`
	LogsRef                 string               `json:"logs_ref"`
	LogsRefCamel            string               `json:"logsRef"`
	Logs                    string               `json:"logs"`
	LogsPath                string               `json:"logs_path"`
	LogsPathCamel           string               `json:"logsPath"`
	ArtifactsRef            string               `json:"artifacts_ref"`
	ArtifactsRefCamel       string               `json:"artifactsRef"`
	ArtifactIDs             []string             `json:"artifact_ids"`
	ArtifactIDsCamel        []string             `json:"artifactIds"`
	InputTokens             int64                `json:"input_tokens"`
	InputTokensCamel        int64                `json:"inputTokens"`
	OutputTokens            int64                `json:"output_tokens"`
	OutputTokensCamel       int64                `json:"outputTokens"`
	CacheReadTokens         int64                `json:"cache_read_tokens"`
	CacheReadTokensCamel    int64                `json:"cacheReadTokens"`
	CacheWriteTokens        int64                `json:"cache_write_tokens"`
	CacheWriteTokensCamel   int64                `json:"cacheWriteTokens"`
	EstimatedCostUSD        float64              `json:"estimated_cost_usd"`
	EstimatedCostUSDCamel   float64              `json:"estimatedCostUsd"`
	Artifacts               []bridgeArtifact     `json:"artifacts"`
	ArtifactDescriptors     []bridgeArtifact     `json:"artifact_descriptors"`
	ArtifactDescriptorsAlt  []bridgeArtifact     `json:"artifactDescriptors"`
	SessionID               string               `json:"session_id"`
	SessionIDCamel          string               `json:"sessionId"`
	TranscriptRef           string               `json:"transcript_ref"`
	TranscriptRefCamel      string               `json:"transcriptRef"`
	Transcript              string               `json:"transcript"`
	TranscriptPath          string               `json:"transcript_path"`
	TranscriptPathCamel     string               `json:"transcriptPath"`
	TranscriptEntries       []transcript.Event   `json:"transcript_entries"`
	TranscriptEntriesCamel  []transcript.Event   `json:"transcriptEntries"`
	TranscriptEvents        []transcript.Event   `json:"transcript_events"`
	TranscriptEventsCamel   []transcript.Event   `json:"transcriptEvents"`
	RuntimeMetadata         map[string]string    `json:"runtime_metadata"`
	RuntimeMetadataCamel    map[string]string    `json:"runtimeMetadata"`
	ErrorClass              string               `json:"error_class"`
	ErrorClassCamel         string               `json:"errorClass"`
	ErrorMessage            string               `json:"error_message"`
	ErrorMessageCamel       string               `json:"errorMessage"`
	Patch                   string               `json:"patch"`
	PatchPath               string               `json:"patch_path"`
	PatchPathCamel          string               `json:"patchPath"`
	PatchBaseRef            string               `json:"patch_base_ref"`
	PatchBaseRefCamel       string               `json:"patchBaseRef"`
	BaseRef                 string               `json:"base_ref"`
	BaseRefCamel            string               `json:"baseRef"`
	PatchArtifactID         string               `json:"patch_artifact_id"`
	PatchArtifactIDCamel    string               `json:"patchArtifactId"`
	PatchSummary            string               `json:"patch_summary"`
	PatchSummaryCamel       string               `json:"patchSummary"`
	PatchMIMEType           string               `json:"patch_mime_type"`
	PatchMIMETypeCamel      string               `json:"patchMimeType"`
	PatchVisibility         string               `json:"patch_visibility"`
	PatchVisibilityCamel    string               `json:"patchVisibility"`
	PatchRedactionStatus    string               `json:"patch_redaction_status"`
	PatchRedactionStatusAlt string               `json:"patchRedactionStatus"`
}

type bridgeArtifact struct {
	ArtifactID         string            `json:"artifact_id"`
	ArtifactIDCamel    string            `json:"artifactId"`
	ID                 string            `json:"id"`
	Type               string            `json:"type"`
	URI                string            `json:"uri"`
	Summary            string            `json:"summary"`
	MIMEType           string            `json:"mime_type"`
	MIMETypeCamel      string            `json:"mimeType"`
	SizeBytes          int64             `json:"size_bytes"`
	SizeBytesCamel     int64             `json:"sizeBytes"`
	Checksum           string            `json:"checksum"`
	ContentHash        string            `json:"content_hash"`
	ContentHashCamel   string            `json:"contentHash"`
	Visibility         string            `json:"visibility"`
	RedactionStatus    string            `json:"redaction_status"`
	RedactionStatusAlt string            `json:"redactionStatus"`
	Metadata           map[string]string `json:"metadata"`
}

type flueTaskSession struct {
	SessionID string
	Metadata  map[string]string
	cancel    context.CancelFunc
}

func (e HostBridgeTaskExecutor) PreflightTaskProvider(ctx context.Context, opts TaskRunRequestOptions) (TaskRunRequestOptions, error) {
	if taskProviderIsNoop(opts.ProviderProfile) {
		return LocalTaskExecutor{}.PreflightTaskProvider(ctx, opts)
	}
	command, err := e.command()
	if err != nil {
		return opts, err
	}
	if len(command) == 0 {
		if preflighter, ok := e.Fallback.(TaskProviderPreflighter); ok {
			return preflighter.PreflightTaskProvider(ctx, opts)
		}
		return resolveTaskProviderProfile(opts, false)
	}
	return resolveTaskProviderProfile(opts, true)
}

func (e HostBridgeTaskExecutor) ExecuteTask(ctx context.Context, req TaskExecRequest) (result TaskExecResult, err error) {
	if taskProviderIsNoop(req.ProviderProfile) {
		return LocalTaskExecutor{}.ExecuteTask(ctx, req)
	}
	command, err := e.command()
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(command) == 0 {
		fallback := e.Fallback
		if fallback == nil {
			fallback = LocalTaskExecutor{}
		}
		return fallback.ExecuteTask(ctx, req)
	}
	session, err := e.startFlueTaskSession(ctx, req)
	if err != nil {
		return TaskExecResult{}, err
	}
	var runner *bridgeTaskRunnerResult
	defer func() {
		if session != nil {
			if finishErr := e.finishFlueTaskSession(ctx, req, session, result, runner, err); finishErr != nil && err == nil {
				err = finishErr
			}
		}
	}()
	runnerResult, err := e.runCommand(ctx, req, command)
	if err != nil {
		return TaskExecResult{}, err
	}
	runner = &runnerResult
	result = runnerResult.taskExecResult()
	if artifacts := runner.finalizedArtifacts(); len(artifacts) > 0 {
		result, err = e.registerRunnerArtifacts(ctx, req, artifacts, result)
		if err != nil {
			return TaskExecResult{}, err
		}
	}
	result, err = e.persistRunnerOutputArtifacts(ctx, req, session, runnerResult, result)
	if err != nil {
		return TaskExecResult{}, err
	}
	patch, err := e.readPatch(ctx, runnerResult)
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(patch) == 0 {
		return result, nil
	}
	return e.finalizeAndApplyPatch(ctx, req, runnerResult, patch, result)
}

func taskProviderIsNoop(provider string) bool {
	switch strings.TrimSpace(provider) {
	case "local-noop", "noop":
		return true
	default:
		return false
	}
}

func (e HostBridgeTaskExecutor) startFlueTaskSession(ctx context.Context, req TaskExecRequest) (*flueTaskSession, error) {
	if e.Store == nil || !taskExecUsesFlueRuntime(req) {
		return nil, nil
	}
	sessionID := flueTaskSessionID(req)
	metadata := flueTaskSessionMetadata(req, sessionID)
	status := domain.AgentSessionRunning
	if _, err := e.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey:    req.WorkspaceKey,
		SessionID:       sessionID,
		AgentID:         flueTaskAgentID(req),
		NodeID:          req.NodeID,
		Kind:            domain.AgentSessionKindTask,
		TaskID:          req.TaskID,
		ParentSessionID: req.ParentSessionID,
		Status:          status,
		Phase:           "implementation",
		Metadata:        metadata,
	}); err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("create flue task agent session: %w", err)
		}
		existing, getErr := e.Store.AgentSessions().Get(ctx, req.WorkspaceKey, sessionID)
		if getErr != nil {
			return nil, fmt.Errorf("get existing flue task agent session: %w", getErr)
		}
		metadata = mergeStringMaps(existing.Metadata, metadata)
		if _, updateErr := e.Store.AgentSessions().Update(ctx, req.WorkspaceKey, sessionID, store.AgentSessionUpdate{
			NodeID:   optionalString(req.NodeID),
			TaskID:   optionalString(req.TaskID),
			Status:   &status,
			Phase:    optionalString("implementation"),
			Metadata: &metadata,
		}); updateErr != nil {
			return nil, fmt.Errorf("update existing flue task agent session: %w", updateErr)
		}
	}
	hbCtx, cancel := context.WithCancel(ctx)
	go heartbeatFlueTaskSession(hbCtx, e.Store, req.WorkspaceKey, sessionID, 30*time.Second)
	return &flueTaskSession{SessionID: sessionID, Metadata: metadata, cancel: cancel}, nil
}

func heartbeatFlueTaskSession(ctx context.Context, st store.Store, workspaceKey, sessionID string, interval time.Duration) {
	if st == nil || workspaceKey == "" || sessionID == "" || interval <= 0 {
		return
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _ = st.AgentSessions().Heartbeat(ctx, workspaceKey, sessionID)
			timer.Reset(interval)
		}
	}
}

func (e HostBridgeTaskExecutor) finishFlueTaskSession(ctx context.Context, req TaskExecRequest, session *flueTaskSession, result TaskExecResult, runner *bridgeTaskRunnerResult, execErr error) error {
	if e.Store == nil || session == nil {
		return nil
	}
	if session.cancel != nil {
		session.cancel()
	}
	status := flueTaskSessionStatus(result, execErr)
	metadata := mergeStringMaps(session.Metadata, result.RuntimeMetadata)
	if runner != nil {
		if sessionID := firstNonEmpty(runner.SessionID, runner.SessionIDCamel); sessionID != "" {
			metadata["driver_runner_session_id"] = sessionID
		}
	}
	if result.LogsRef != "" {
		metadata["logs_ref"] = result.LogsRef
	}
	if result.ArtifactsRef != "" {
		metadata["artifacts_ref"] = result.ArtifactsRef
	}
	if execErr != nil {
		metadata["task_runner_error"] = execErr.Error()
	}
	exitCode := result.ExitCode
	if status != domain.AgentSessionCompleted && exitCode == 0 {
		exitCode = 1
	}
	exitCodePtr := &exitCode
	finishedAt := time.Now().UTC()
	finishedAtPtr := &finishedAt
	errorClass := result.ErrorClass
	if execErr != nil && errorClass == "" {
		errorClass = "task_runner_error"
	}
	summary := "task run completed"
	if status != domain.AgentSessionCompleted {
		summary = firstNonEmpty(result.ErrorMessage, "task run failed")
	}
	return updateFlueAgentSession(ctx, e.Store, req.WorkspaceKey, session.SessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		Summary:    &summary,
		ErrorClass: optionalString(errorClass),
		ExitCode:   &exitCodePtr,
		Metadata:   &metadata,
	})
}

func updateFlueAgentSession(ctx context.Context, st store.Store, workspaceKey, sessionID string, patch store.AgentSessionUpdate) error {
	if _, err := st.AgentSessions().Update(ctx, workspaceKey, sessionID, patch); err != nil {
		return fmt.Errorf("update flue task agent session: %w", err)
	}
	return nil
}

func flueTaskSessionStatus(result TaskExecResult, execErr error) domain.AgentSessionStatus {
	if execErr != nil {
		return domain.AgentSessionFailed
	}
	switch result.Status {
	case "", domain.TaskRunCompleted:
		if result.ExitCode == 0 {
			return domain.AgentSessionCompleted
		}
		return domain.AgentSessionFailed
	case domain.TaskRunCancelled:
		return domain.AgentSessionCancelled
	default:
		return domain.AgentSessionFailed
	}
}

func taskExecUsesFlueRuntime(req TaskExecRequest) bool {
	profile := strings.TrimSpace(req.ProviderProfile)
	return strings.HasPrefix(profile, "flue") ||
		strings.TrimSpace(req.RunnerPlacement.Provider) == "flue" ||
		strings.HasPrefix(strings.TrimSpace(req.SandboxPlacement.Provider), "flue")
}

func flueTaskSessionID(req TaskExecRequest) string {
	return "flue-" + req.TaskRunID
}

func flueTaskAgentID(req TaskExecRequest) string {
	return firstNonEmpty(req.WorkerProfileID, req.RunnerPlacement.RunnerID, req.RunnerPlacement.Provider, "flue-task-agent")
}

func flueTaskSessionMetadata(req TaskExecRequest, sessionID string) map[string]string {
	metadata := map[string]string{
		"backend":          "flue",
		"runtime":          "flue",
		"task_id":          req.TaskID,
		"task_run_id":      req.TaskRunID,
		"driver_run_id":    req.DriverRunID,
		"provider_profile": req.ProviderProfile,
		"flue_session":     sessionID,
		"flue_harness":     "task-agent",
	}
	if req.DriverStepID != "" {
		metadata["driver_step_id"] = req.DriverStepID
	}
	if req.ParentSessionID != "" {
		metadata["parent_session_id"] = req.ParentSessionID
	}
	return metadata
}

func (e HostBridgeTaskExecutor) command() ([]string, error) {
	if len(e.Command) > 0 {
		return append([]string(nil), e.Command...), nil
	}
	if raw := strings.TrimSpace(os.Getenv(TaskRunnerCommandJSONEnv)); raw != "" {
		var command []string
		if err := json.Unmarshal([]byte(raw), &command); err != nil {
			return nil, fmt.Errorf("decode %s: %w", TaskRunnerCommandJSONEnv, err)
		}
		return normalizeCommand(command)
	}
	if raw := strings.TrimSpace(os.Getenv(TaskRunnerCommandEnv)); raw != "" {
		return []string{raw}, nil
	}
	return nil, nil
}

func normalizeCommand(command []string) ([]string, error) {
	out := make([]string, 0, len(command))
	for _, part := range command {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("task runner command is empty: %w", domain.ErrInvalid)
	}
	return out, nil
}

func (e HostBridgeTaskExecutor) runCommand(ctx context.Context, req TaskExecRequest, command []string) (bridgeTaskRunnerResult, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return bridgeTaskRunnerResult{}, fmt.Errorf("encode task runner request: %w", err)
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // configured argv vector; no shell expansion.
	if worktree := strings.TrimSpace(e.WorktreePath); worktree != "" {
		cmd.Dir = worktree
	}
	cmd.Env = append(taskRunnerBaseEnv(os.Environ()), taskRunnerEnv(req, e.WorktreePath, string(input))...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return bridgeTaskRunnerResult{}, fmt.Errorf("task runner command failed: %s", msg)
	}
	payload, err := lastJSONLine(stdout.Bytes())
	if err != nil {
		return bridgeTaskRunnerResult{}, err
	}
	var result bridgeTaskRunnerResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return bridgeTaskRunnerResult{}, fmt.Errorf("decode task runner result: %w", err)
	}
	return result, nil
}

func taskRunnerEnv(req TaskExecRequest, worktreePath, requestJSON string) []string {
	return []string{
		"LOOM_TASK_RUN_REQUEST_JSON=" + requestJSON,
		"LOOM_WORKTREE_PATH=" + strings.TrimSpace(worktreePath),
		"LOOM_DRIVER_WORKSPACE=" + req.WorkspaceKey,
		"LOOM_DRIVER_RUN_ID=" + req.DriverRunID,
		"LOOM_DRIVER_STEP_ID=" + req.DriverStepID,
		"LOOM_PARENT_SESSION_ID=" + req.ParentSessionID,
		"LOOM_TASK_RUN_ID=" + req.TaskRunID,
		"LOOM_TASK_ID=" + req.TaskID,
		"LOOM_TASK_RUN_PARENT_SESSION_ID=" + req.ParentSessionID,
		"LOOM_TASK_RUN_WORKER_PROFILE_ID=" + req.WorkerProfileID,
		"LOOM_TASK_RUN_PROVIDER_PROFILE=" + req.ProviderProfile,
		"LOOM_TASK_RUN_NODE_ID=" + req.NodeID,
		"LOOM_TASK_RUN_LEASE_ID=" + req.LeaseID,
		"LOOM_TASK_RUN_LEASE_TOKEN=" + req.LeaseToken,
		fmt.Sprintf("LOOM_TASK_RUN_FENCING_TOKEN=%d", req.FencingToken),
		"LOOM_TASK_RUN_RUNNER_PLACEMENT_JSON=" + taskRunPlacementJSON(req.RunnerPlacement),
		"LOOM_TASK_RUN_SANDBOX_PLACEMENT_JSON=" + taskRunPlacementJSON(req.SandboxPlacement),
	}
}

func taskRunPlacementJSON(placement domain.TaskRunPlacement) string {
	if placement.Empty() {
		return "{}"
	}
	b, err := json.Marshal(placement)
	if err != nil {
		return "{}"
	}
	return string(b)
}

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
		metadata := cloneStringMap(artifact.Metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["driver_run_id"] = req.DriverRunID
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
			return TaskExecResult{}, fmt.Errorf("create runner artifact %q: %w", artifactID, err)
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
			return TaskExecResult{}, fmt.Errorf("finalize runner artifact %q: %w", artifactID, err)
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
	metadata := map[string]string{
		"driver_run_id":    req.DriverRunID,
		"provider_profile": req.ProviderProfile,
		"runtime":          "flue",
		"task_run_id":      req.TaskRunID,
	}
	if _, err := e.Store.Artifacts().Create(ctx, store.ArtifactCreate{
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
		Metadata:      metadata,
	}); err != nil {
		return nil, fmt.Errorf("create %s artifact: %w", artifactType, err)
	}
	uploaded, err := e.Store.Artifacts().UploadContent(ctx, req.WorkspaceKey, artifactID, store.ArtifactContentUpload{
		Body:     bytes.NewReader(content),
		MIMEType: mimeType,
	})
	if err != nil {
		return nil, fmt.Errorf("upload %s artifact: %w", artifactType, err)
	}
	finalized, err := e.Store.Artifacts().Finalize(ctx, req.WorkspaceKey, artifactID, store.ArtifactFinalize{
		ContentHash: &uploaded.ContentHash,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize %s artifact: %w", artifactType, err)
	}
	return finalized, nil
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

func (e HostBridgeTaskExecutor) finalizeAndApplyPatch(ctx context.Context, req TaskExecRequest, runner bridgeTaskRunnerResult, patch []byte, result TaskExecResult) (TaskExecResult, error) {
	if e.Store == nil {
		return TaskExecResult{}, fmt.Errorf("store required for patch artifact finalization: %w", domain.ErrInvalid)
	}
	artifactID := firstNonEmpty(runner.PatchArtifactID, runner.PatchArtifactIDCamel)
	if artifactID == "" {
		artifactID = "patch-" + req.TaskRunID
	}
	summary := firstNonEmpty(runner.PatchSummary, runner.PatchSummaryCamel, "task patch")
	mimeType := firstNonEmpty(runner.PatchMIMEType, runner.PatchMIMETypeCamel, "text/x-diff")
	baseRef := firstNonEmpty(runner.PatchBaseRef, runner.PatchBaseRefCamel, runner.BaseRef, runner.BaseRefCamel)
	metadata := map[string]string{
		"driver_run_id":    req.DriverRunID,
		"provider_profile": req.ProviderProfile,
	}
	if baseRef != "" {
		metadata["patch_base_ref"] = baseRef
	}
	if _, err := e.Store.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:    req.WorkspaceKey,
		ArtifactID:      artifactID,
		TaskID:          req.TaskID,
		OwnerType:       "task_run",
		OwnerID:         req.TaskRunID,
		Type:            "patch",
		Summary:         summary,
		MIMEType:        mimeType,
		Visibility:      firstNonEmpty(runner.PatchVisibility, runner.PatchVisibilityCamel),
		RedactionStatus: firstNonEmpty(runner.PatchRedactionStatus, runner.PatchRedactionStatusAlt),
		DurableStatus:   "declared",
		Metadata:        metadata,
	}); err != nil {
		return TaskExecResult{}, fmt.Errorf("create patch artifact: %w", err)
	}
	uploaded, err := e.Store.Artifacts().UploadContent(ctx, req.WorkspaceKey, artifactID, store.ArtifactContentUpload{
		Body:     bytes.NewReader(patch),
		MIMEType: mimeType,
	})
	if err != nil {
		return TaskExecResult{}, fmt.Errorf("upload patch artifact: %w", err)
	}
	finalized, err := e.Store.Artifacts().Finalize(ctx, req.WorkspaceKey, artifactID, store.ArtifactFinalize{
		ContentHash: &uploaded.ContentHash,
	})
	if err != nil {
		return TaskExecResult{}, fmt.Errorf("finalize patch artifact: %w", err)
	}
	result.ArtifactIDs = normalizeArtifactIDs(append(result.ArtifactIDs, finalized.ArtifactID))
	if result.ArtifactsRef == "" {
		result.ArtifactsRef = "artifacts://" + req.TaskRunID
	}
	if result.RuntimeMetadata == nil {
		result.RuntimeMetadata = map[string]string{}
	}
	result.RuntimeMetadata["patch_artifact_id"] = finalized.ArtifactID
	result.RuntimeMetadata["patch_content_hash"] = finalized.ContentHash
	if strings.TrimSpace(e.WorktreePath) == "" || strings.TrimSpace(baseRef) == "" {
		result.Status = domain.TaskRunFailed
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
		result.ErrorClass = "patch_back_base_required"
		result.ErrorMessage = "patch artifact requires worktree path and base ref for local patch-back"
		result.RuntimeMetadata["patch_back_status"] = PatchBackBaseUnreachable
		return result, nil
	}
	patchBack, err := ApplyPatchBack(ctx, PatchBackOptions{
		WorktreePath: e.WorktreePath,
		BaseRef:      baseRef,
		Patch:        patch,
	})
	if err != nil {
		return TaskExecResult{}, err
	}
	result.RuntimeMetadata["patch_back_status"] = patchBack.Status
	if patchBack.BaseSHA != "" {
		result.RuntimeMetadata["patch_back_base_sha"] = patchBack.BaseSHA
	}
	if patchBack.CurrentHEAD != "" {
		result.RuntimeMetadata["patch_back_head_sha"] = patchBack.CurrentHEAD
	}
	if patchBack.Applied {
		return result, nil
	}
	result.Status = domain.TaskRunFailed
	if result.ExitCode == 0 {
		result.ExitCode = 1
	}
	result.ErrorClass = firstNonEmpty(patchBack.ErrorClass, patchBack.Status)
	result.ErrorMessage = patchBack.ErrorMessage
	result.RuntimeMetadata["patch_preserved"] = "true"
	return result, nil
}

func firstNonNilStrings(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonNilMap(values ...map[string]string) map[string]string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func mergeStringMaps(values ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		for key, val := range value {
			if strings.TrimSpace(key) != "" {
				out[key] = val
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroFloat64(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
