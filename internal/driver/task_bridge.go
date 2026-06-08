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

	"github.com/tysonthomas9/loomcli/internal/domain"
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

func (e HostBridgeTaskExecutor) ExecuteTask(ctx context.Context, req TaskExecRequest) (TaskExecResult, error) {
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
	runner, err := e.runCommand(ctx, req, command)
	if err != nil {
		return TaskExecResult{}, err
	}
	result := runner.taskExecResult()
	if artifacts := runner.finalizedArtifacts(); len(artifacts) > 0 {
		result, err = e.registerRunnerArtifacts(ctx, req, artifacts, result)
		if err != nil {
			return TaskExecResult{}, err
		}
	}
	patch, err := e.readPatch(ctx, runner)
	if err != nil {
		return TaskExecResult{}, err
	}
	if len(patch) == 0 {
		return result, nil
	}
	return e.finalizeAndApplyPatch(ctx, req, runner, patch, result)
}

func taskProviderIsNoop(provider string) bool {
	switch strings.TrimSpace(provider) {
	case "local-noop", "noop":
		return true
	default:
		return false
	}
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
		"LOOM_TASK_RUN_ID=" + req.TaskRunID,
		"LOOM_TASK_ID=" + req.TaskID,
		"LOOM_TASK_RUN_WORKER_PROFILE_ID=" + req.WorkerProfileID,
		"LOOM_TASK_RUN_PROVIDER_PROFILE=" + req.ProviderProfile,
		"LOOM_TASK_PROVIDER_PROFILE=" + req.ProviderProfile,
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
		finalized, err := e.registerRunnerArtifact(ctx, req, artifact, i)
		if err != nil {
			return TaskExecResult{}, err
		}
		artifactIDs = normalizeArtifactIDs(append(artifactIDs, finalized.ArtifactID))
		result.RuntimeMetadata = recordRunnerArtifactMetadata(result.RuntimeMetadata, finalized)
	}
	result.ArtifactIDs = artifactIDs
	if result.ArtifactsRef == "" && len(artifactIDs) > 0 {
		result.ArtifactsRef = "artifacts://" + req.TaskRunID
	}
	return result, nil
}

func (e HostBridgeTaskExecutor) registerRunnerArtifact(ctx context.Context, req TaskExecRequest, artifact bridgeArtifact, index int) (*domain.Artifact, error) {
	spec := runnerArtifactSpec(req, artifact, index)
	if spec.uri == "" {
		return &domain.Artifact{ArtifactID: spec.artifactID}, nil
	}
	if _, err := e.Store.Artifacts().Create(ctx, spec.create); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return nil, fmt.Errorf("create runner artifact %q: %w", spec.artifactID, err)
	}
	finalized, err := e.Store.Artifacts().Finalize(ctx, req.WorkspaceKey, spec.artifactID, spec.finalize)
	if err != nil {
		return nil, fmt.Errorf("finalize runner artifact %q: %w", spec.artifactID, err)
	}
	return finalized, nil
}

type runnerArtifactRegistration struct {
	artifactID string
	uri        string
	create     store.ArtifactCreate
	finalize   store.ArtifactFinalize
}

func runnerArtifactSpec(req TaskExecRequest, artifact bridgeArtifact, index int) runnerArtifactRegistration {
	artifactID := firstNonEmpty(artifact.ArtifactID, artifact.ArtifactIDCamel, artifact.ID)
	if artifactID == "" {
		artifactID = fmt.Sprintf("artifact-%s-%d", req.TaskRunID, index+1)
	}
	metadata := runnerArtifactMetadata(req, artifact)
	contentHash := firstNonEmpty(artifact.ContentHash, artifact.ContentHashCamel, artifact.Checksum)
	checksum := firstNonEmpty(artifact.Checksum, contentHash)
	mimeType := firstNonEmpty(artifact.MIMEType, artifact.MIMETypeCamel)
	redactionStatus := firstNonEmpty(artifact.RedactionStatus, artifact.RedactionStatusAlt)
	return runnerArtifactRegistration{
		artifactID: artifactID,
		uri:        strings.TrimSpace(artifact.URI),
		create:     runnerArtifactCreate(req, artifactID, artifact, mimeType, redactionStatus, metadata),
		finalize: store.ArtifactFinalize{
			URI:             optionalString(artifact.URI),
			Summary:         optionalString(artifact.Summary),
			MIMEType:        optionalString(mimeType),
			SizeBytes:       optionalInt64(firstNonZeroInt64(artifact.SizeBytes, artifact.SizeBytesCamel)),
			Checksum:        optionalString(checksum),
			ContentHash:     optionalString(contentHash),
			Visibility:      optionalString(artifact.Visibility),
			RedactionStatus: optionalString(redactionStatus),
			Metadata:        &metadata,
		},
	}
}

func runnerArtifactMetadata(req TaskExecRequest, artifact bridgeArtifact) map[string]string {
	metadata := cloneStringMap(artifact.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["driver_run_id"] = req.DriverRunID
	metadata["provider_profile"] = req.ProviderProfile
	return metadata
}

func runnerArtifactCreate(req TaskExecRequest, artifactID string, artifact bridgeArtifact, mimeType, redactionStatus string, metadata map[string]string) store.ArtifactCreate {
	return store.ArtifactCreate{
		WorkspaceKey:    req.WorkspaceKey,
		ArtifactID:      artifactID,
		TaskID:          req.TaskID,
		OwnerType:       "task_run",
		OwnerID:         req.TaskRunID,
		Type:            firstNonEmpty(artifact.Type, "artifact"),
		Summary:         artifact.Summary,
		MIMEType:        mimeType,
		Visibility:      artifact.Visibility,
		RedactionStatus: redactionStatus,
		DurableStatus:   "declared",
		Metadata:        metadata,
	}
}

func recordRunnerArtifactMetadata(metadata map[string]string, artifact *domain.Artifact) map[string]string {
	if artifact == nil || artifact.ContentHash == "" {
		return metadata
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["artifact."+artifact.ArtifactID+".content_hash"] = artifact.ContentHash
	return metadata
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
	patchPath = strings.TrimSpace(patchPath)
	if patchPath == "" {
		return "", fmt.Errorf("patch path required: %w", domain.ErrInvalid)
	}
	if filepath.IsAbs(patchPath) {
		return patchPath, nil
	}
	root := strings.TrimSpace(e.WorktreePath)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	path := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(patchPath)))
	rel, err := filepath.Rel(absRoot, path)
	if err != nil {
		return "", fmt.Errorf("resolve patch path: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("patch path escapes worktree: %w", domain.ErrInvalid)
	}
	return path, nil
}

func (e HostBridgeTaskExecutor) finalizeAndApplyPatch(ctx context.Context, req TaskExecRequest, runner bridgeTaskRunnerResult, patch []byte, result TaskExecResult) (TaskExecResult, error) {
	if e.Store == nil {
		return TaskExecResult{}, fmt.Errorf("store required for patch artifact finalization: %w", domain.ErrInvalid)
	}
	spec := patchArtifactSpec(req, runner)
	finalized, err := e.finalizePatchArtifact(ctx, req, spec, patch)
	if err != nil {
		return TaskExecResult{}, err
	}
	result = recordPatchArtifactResult(req, result, finalized)
	if strings.TrimSpace(e.WorktreePath) == "" || strings.TrimSpace(spec.baseRef) == "" {
		result = patchBackBaseRequiredResult(result)
		return result, nil
	}
	patchBack, err := ApplyPatchBack(ctx, PatchBackOptions{
		WorktreePath: e.WorktreePath,
		BaseRef:      spec.baseRef,
		Patch:        patch,
	})
	if err != nil {
		return TaskExecResult{}, err
	}
	return applyPatchBackResult(result, patchBack), nil
}

type patchArtifactRegistration struct {
	artifactID string
	mimeType   string
	baseRef    string
	create     store.ArtifactCreate
}

func patchArtifactSpec(req TaskExecRequest, runner bridgeTaskRunnerResult) patchArtifactRegistration {
	artifactID := firstNonEmpty(runner.PatchArtifactID, runner.PatchArtifactIDCamel)
	if artifactID == "" {
		artifactID = "patch-" + req.TaskRunID
	}
	baseRef := firstNonEmpty(runner.PatchBaseRef, runner.PatchBaseRefCamel, runner.BaseRef, runner.BaseRefCamel)
	metadata := patchArtifactMetadata(req, baseRef)
	mimeType := firstNonEmpty(runner.PatchMIMEType, runner.PatchMIMETypeCamel, "text/x-diff")
	return patchArtifactRegistration{
		artifactID: artifactID,
		mimeType:   mimeType,
		baseRef:    baseRef,
		create: store.ArtifactCreate{
			WorkspaceKey:    req.WorkspaceKey,
			ArtifactID:      artifactID,
			TaskID:          req.TaskID,
			OwnerType:       "task_run",
			OwnerID:         req.TaskRunID,
			Type:            "patch",
			Summary:         firstNonEmpty(runner.PatchSummary, runner.PatchSummaryCamel, "task patch"),
			MIMEType:        mimeType,
			Visibility:      firstNonEmpty(runner.PatchVisibility, runner.PatchVisibilityCamel),
			RedactionStatus: firstNonEmpty(runner.PatchRedactionStatus, runner.PatchRedactionStatusAlt),
			DurableStatus:   "declared",
			Metadata:        metadata,
		},
	}
}

func patchArtifactMetadata(req TaskExecRequest, baseRef string) map[string]string {
	metadata := map[string]string{
		"driver_run_id":    req.DriverRunID,
		"provider_profile": req.ProviderProfile,
	}
	if baseRef != "" {
		metadata["patch_base_ref"] = baseRef
	}
	return metadata
}

func (e HostBridgeTaskExecutor) finalizePatchArtifact(ctx context.Context, req TaskExecRequest, spec patchArtifactRegistration, patch []byte) (*domain.Artifact, error) {
	if _, err := e.Store.Artifacts().Create(ctx, spec.create); err != nil {
		return nil, fmt.Errorf("create patch artifact: %w", err)
	}
	uploaded, err := e.Store.Artifacts().UploadContent(ctx, req.WorkspaceKey, spec.artifactID, store.ArtifactContentUpload{
		Body:     bytes.NewReader(patch),
		MIMEType: spec.mimeType,
	})
	if err != nil {
		return nil, fmt.Errorf("upload patch artifact: %w", err)
	}
	finalized, err := e.Store.Artifacts().Finalize(ctx, req.WorkspaceKey, spec.artifactID, store.ArtifactFinalize{
		ContentHash: &uploaded.ContentHash,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize patch artifact: %w", err)
	}
	return finalized, nil
}

func recordPatchArtifactResult(req TaskExecRequest, result TaskExecResult, finalized *domain.Artifact) TaskExecResult {
	result.ArtifactIDs = normalizeArtifactIDs(append(result.ArtifactIDs, finalized.ArtifactID))
	if result.ArtifactsRef == "" {
		result.ArtifactsRef = "artifacts://" + req.TaskRunID
	}
	if result.RuntimeMetadata == nil {
		result.RuntimeMetadata = map[string]string{}
	}
	result.RuntimeMetadata["patch_artifact_id"] = finalized.ArtifactID
	result.RuntimeMetadata["patch_content_hash"] = finalized.ContentHash
	return result
}

func patchBackBaseRequiredResult(result TaskExecResult) TaskExecResult {
	result.Status = domain.TaskRunFailed
	if result.ExitCode == 0 {
		result.ExitCode = 1
	}
	result.ErrorClass = "patch_back_base_required"
	result.ErrorMessage = "patch artifact requires worktree path and base ref for local patch-back"
	result.RuntimeMetadata["patch_back_status"] = PatchBackBaseUnreachable
	return result
}

func applyPatchBackResult(result TaskExecResult, patchBack *PatchBackResult) TaskExecResult {
	result.RuntimeMetadata["patch_back_status"] = patchBack.Status
	if patchBack.BaseSHA != "" {
		result.RuntimeMetadata["patch_back_base_sha"] = patchBack.BaseSHA
	}
	if patchBack.CurrentHEAD != "" {
		result.RuntimeMetadata["patch_back_head_sha"] = patchBack.CurrentHEAD
	}
	if patchBack.Applied {
		return result
	}
	result.Status = domain.TaskRunFailed
	if result.ExitCode == 0 {
		result.ExitCode = 1
	}
	result.ErrorClass = firstNonEmpty(patchBack.ErrorClass, patchBack.Status)
	result.ErrorMessage = patchBack.ErrorMessage
	result.RuntimeMetadata["patch_preserved"] = "true"
	return result
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
