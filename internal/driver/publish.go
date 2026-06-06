package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	RuntimeFlueNode = "flue-node"
	EntrypointRun   = "run"
)

type PackOptions struct {
	WorkDir     string
	SourcePath  string
	DriverName  string
	FlueCommand []string
}

type PreparedWorkflow struct {
	WorkDir      string
	SourcePath   string
	SourceRef    string
	Source       []byte
	DriverName   string
	DriverID     string
	WorkflowName string
	VersionID    string
	SourceDigest string
}

type Bundle struct {
	Root         string            `json:"root"`
	BundleRef    string            `json:"bundle_ref"`
	SourceRef    string            `json:"source_ref"`
	SourceDigest string            `json:"source_digest"`
	BundleDigest string            `json:"bundle_digest"`
	Manifest     map[string]string `json:"manifest"`
	Diagnostics  string            `json:"diagnostics,omitempty"`
}

type PublishOptions struct {
	WorkspaceKey string
	WorkDir      string
	SourcePath   string
	DriverName   string
	CreatedBy    string
	FlueCommand  []string
}

type PublishResult struct {
	Driver         *domain.Driver        `json:"driver"`
	Version        *domain.DriverVersion `json:"version"`
	Bundle         *Bundle               `json:"bundle,omitempty"`
	CreatedDriver  bool                  `json:"created_driver"`
	CreatedVersion bool                  `json:"created_version"`
	ReusedVersion  bool                  `json:"reused_version"`
}

type ValidationError struct {
	Diagnostics string
}

func (e *ValidationError) Error() string {
	if e == nil || e.Diagnostics == "" {
		return "workflow validation failed"
	}
	return "workflow validation failed: " + e.Diagnostics
}

var runExportPattern = regexp.MustCompile(`(?m)^\s*export\s+(?:async\s+)?function\s+run\s*\(|^\s*export\s+const\s+run\s*=|^\s*export\s*\{[^}]*\brun\b[^}]*\}`)

func PrepareWorkflow(opts PackOptions) (*PreparedWorkflow, error) {
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve work dir: %w", err)
	}
	if opts.SourcePath == "" {
		return nil, fmt.Errorf("source path required: %w", domain.ErrInvalid)
	}
	sourcePath := opts.SourcePath
	if !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(absWorkDir, sourcePath)
	}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	sourceRef, err := filepath.Rel(absWorkDir, absSource)
	if err != nil {
		return nil, fmt.Errorf("resolve source ref: %w", err)
	}
	sourceRef = filepath.ToSlash(sourceRef)
	if strings.HasPrefix(sourceRef, "../") || sourceRef == ".." {
		return nil, fmt.Errorf("workflow source must be inside work dir: %w", domain.ErrInvalid)
	}
	if filepath.Ext(absSource) != ".ts" {
		return nil, fmt.Errorf("workflow source must be a .ts file: %w", domain.ErrInvalid)
	}
	if !strings.HasPrefix(sourceRef, ".loom/workflows/") {
		return nil, fmt.Errorf("workflow source must live under .loom/workflows: %w", domain.ErrInvalid)
	}
	data, err := os.ReadFile(absSource) //nolint:gosec // source path is user-provided command input.
	if err != nil {
		return nil, fmt.Errorf("read workflow source: %w", err)
	}
	name := strings.TrimSpace(opts.DriverName)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(absSource), filepath.Ext(absSource))
	}
	driverID := slug(name)
	if driverID == "" {
		return nil, fmt.Errorf("driver name %q does not contain a usable id: %w", name, domain.ErrInvalid)
	}
	sourceDigest := digestBytes(data)
	return &PreparedWorkflow{
		WorkDir:      absWorkDir,
		SourcePath:   absSource,
		SourceRef:    sourceRef,
		Source:       append([]byte(nil), data...),
		DriverName:   name,
		DriverID:     driverID,
		WorkflowName: strings.TrimSuffix(filepath.Base(absSource), filepath.Ext(absSource)),
		VersionID:    driverID + "-v-" + digestShort(sourceDigest),
		SourceDigest: sourceDigest,
	}, nil
}

func PackWorkflow(opts PackOptions) (*Bundle, error) {
	prepared, err := PrepareWorkflow(opts)
	if err != nil {
		return nil, err
	}
	if err := ValidateWorkflowSource(prepared.Source); err != nil {
		return nil, err
	}
	return writeBundle(context.Background(), prepared, opts.FlueCommand)
}

func ValidateWorkflowSource(source []byte) error {
	text := string(source)
	if strings.TrimSpace(text) == "" {
		return &ValidationError{Diagnostics: "workflow source is empty"}
	}
	if !runExportPattern.MatchString(text) {
		return &ValidationError{Diagnostics: "workflow must export a named run entrypoint"}
	}
	if err := validateBalancedSyntax(text); err != nil {
		return &ValidationError{Diagnostics: err.Error()}
	}
	return nil
}

func WriteBundle(prepared *PreparedWorkflow) (*Bundle, error) {
	return writeBundle(context.Background(), prepared, nil)
}

func writeBundle(ctx context.Context, prepared *PreparedWorkflow, flueCommand []string) (*Bundle, error) {
	if prepared == nil {
		return nil, fmt.Errorf("prepared workflow required: %w", domain.ErrInvalid)
	}
	bundleRef := filepath.ToSlash(filepath.Join(".loom", "drivers", prepared.DriverID, prepared.VersionID))
	bundleRoot := filepath.Join(prepared.WorkDir, filepath.FromSlash(bundleRef))
	if err := os.RemoveAll(bundleRoot); err != nil {
		return nil, fmt.Errorf("reset bundle root: %w", err)
	}

	sourceRel := filepath.ToSlash(filepath.Join(".flue", "loom-sources", prepared.WorkflowName+".ts"))
	sourcePath := filepath.Join(bundleRoot, filepath.FromSlash(sourceRel))
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		return nil, fmt.Errorf("create bundle source dir: %w", err)
	}
	if err := os.WriteFile(sourcePath, prepared.Source, 0o644); err != nil {
		return nil, fmt.Errorf("write workflow source bundle: %w", err)
	}

	contextRel := filepath.ToSlash(filepath.Join(".flue", "loom-runtime", "context.ts"))
	contextPath := filepath.Join(bundleRoot, filepath.FromSlash(contextRel))
	if err := os.MkdirAll(filepath.Dir(contextPath), 0o755); err != nil {
		return nil, fmt.Errorf("create Loom runtime dir: %w", err)
	}
	if err := os.WriteFile(contextPath, []byte(loomDriverContextModule), 0o644); err != nil {
		return nil, fmt.Errorf("write Loom runtime module: %w", err)
	}

	workflowRel := filepath.ToSlash(filepath.Join(".flue", "workflows", prepared.WorkflowName+".ts"))
	workflowPath := filepath.Join(bundleRoot, filepath.FromSlash(workflowRel))
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		return nil, fmt.Errorf("create bundle workflow dir: %w", err)
	}
	if err := os.WriteFile(workflowPath, []byte(loomWorkflowAdapterSource(sourceRel, contextRel)), 0o644); err != nil {
		return nil, fmt.Errorf("write workflow adapter bundle: %w", err)
	}

	serverRel := filepath.ToSlash(filepath.Join("dist", "server.mjs"))
	serverPath := filepath.Join(bundleRoot, filepath.FromSlash(serverRel))
	if err := runFlueBuild(ctx, bundleRoot, filepath.Dir(serverPath), flueCommand); err != nil {
		return nil, err
	}
	if info, err := os.Stat(serverPath); err != nil {
		return nil, fmt.Errorf("verify Flue build server artifact: %w", err)
	} else if info.IsDir() {
		return nil, fmt.Errorf("verify Flue build server artifact: %s is a directory: %w", serverRel, domain.ErrInvalid)
	}
	manifest := map[string]string{
		"schema_version":    "2",
		"runtime":           RuntimeFlueNode,
		"driver_id":         prepared.DriverID,
		"driver_name":       prepared.DriverName,
		"workflow_name":     prepared.WorkflowName,
		"entrypoint":        EntrypointRun,
		"source_ref":        prepared.SourceRef,
		"source_digest":     prepared.SourceDigest,
		"source_bundle_ref": sourceRel,
		"workflow_ref":      workflowRel,
		"server_ref":        serverRel,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode bundle manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	manifestPath := filepath.Join(bundleRoot, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write bundle manifest: %w", err)
	}
	bundleDigest, err := digestBundleTree(bundleRoot, manifestBytes)
	if err != nil {
		return nil, err
	}
	return &Bundle{
		Root:         bundleRoot,
		BundleRef:    bundleRef,
		SourceRef:    prepared.SourceRef,
		SourceDigest: prepared.SourceDigest,
		BundleDigest: bundleDigest,
		Manifest:     manifest,
	}, nil
}

func PublishWorkflow(ctx context.Context, s store.Store, opts PublishOptions) (*PublishResult, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(opts.WorkspaceKey) == "" {
		return nil, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	prepared, err := PrepareWorkflow(PackOptions{WorkDir: opts.WorkDir, SourcePath: opts.SourcePath, DriverName: opts.DriverName})
	if err != nil {
		return nil, err
	}
	result := &PublishResult{}
	driver, createdDriver, err := ensureDriver(ctx, s, opts.WorkspaceKey, prepared)
	if err != nil {
		return nil, err
	}
	result.Driver = driver
	result.CreatedDriver = createdDriver

	if existing, err := s.DriverVersions().Get(ctx, opts.WorkspaceKey, prepared.VersionID); err == nil {
		result.Version = existing
		result.ReusedVersion = true
		if existing.ValidationStatus == domain.DriverVersionValidationPassed {
			result.Bundle = &Bundle{
				BundleRef:    existing.BundleRef,
				SourceRef:    existing.SourceRef,
				SourceDigest: existing.SourceDigest,
				BundleDigest: existing.BundleDigest,
				Manifest:     existing.Manifest,
			}
			return result, nil
		}
		if existing.ValidationStatus == domain.DriverVersionValidationFailed {
			return result, &ValidationError{Diagnostics: strings.TrimPrefix(existing.BuildDiagnostics, "workflow validation failed: ")}
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return result, fmt.Errorf("get driver version: %w", err)
	}

	nextVersion, err := nextDriverVersion(ctx, s, opts.WorkspaceKey, prepared.DriverID)
	if err != nil {
		return result, err
	}
	validationErr := ValidateWorkflowSource(prepared.Source)
	if validationErr != nil {
		diagnostics := validationErr.Error()
		version, createErr := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
			WorkspaceKey:     opts.WorkspaceKey,
			VersionID:        prepared.VersionID,
			DriverID:         prepared.DriverID,
			Version:          nextVersion,
			SourceRef:        prepared.SourceRef,
			SourceDigest:     prepared.SourceDigest,
			BundleDigest:     digestBytes([]byte("validation-failed\n" + prepared.SourceDigest + "\n" + diagnostics)),
			Runtime:          RuntimeFlueNode,
			Manifest:         failedManifest(prepared),
			BuildDiagnostics: diagnostics,
			ValidationStatus: domain.DriverVersionValidationFailed,
			CreatedBy:        opts.CreatedBy,
		})
		if createErr != nil {
			return result, fmt.Errorf("record failed driver version: %w", createErr)
		}
		result.Version = version
		result.CreatedVersion = true
		return result, validationErr
	}

	bundle, err := writeBundle(ctx, prepared, opts.FlueCommand)
	if err != nil {
		return result, err
	}
	version, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     opts.WorkspaceKey,
		VersionID:        prepared.VersionID,
		DriverID:         prepared.DriverID,
		Version:          nextVersion,
		SourceRef:        prepared.SourceRef,
		SourceDigest:     prepared.SourceDigest,
		BundleRef:        bundle.BundleRef,
		BundleDigest:     bundle.BundleDigest,
		Runtime:          RuntimeFlueNode,
		Manifest:         bundle.Manifest,
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        opts.CreatedBy,
	})
	if err != nil {
		return result, fmt.Errorf("create driver version: %w", err)
	}
	result.Bundle = bundle
	result.Version = version
	result.CreatedVersion = true
	active := version.VersionID
	status := domain.DriverStatusActive
	driver, err = s.Drivers().Update(ctx, opts.WorkspaceKey, prepared.DriverID, store.DriverUpdate{
		ActiveVersionID: &active,
		Status:          &status,
		Metadata:        &bundle.Manifest,
	})
	if err != nil {
		return result, fmt.Errorf("activate driver version: %w", err)
	}
	result.Driver = driver
	return result, nil
}

func ensureDriver(ctx context.Context, s store.Store, ws string, prepared *PreparedWorkflow) (*domain.Driver, bool, error) {
	driver, err := s.Drivers().Get(ctx, ws, prepared.DriverID)
	if err == nil {
		return driver, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, fmt.Errorf("get driver: %w", err)
	}
	driver, err = s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws,
		DriverID:     prepared.DriverID,
		Name:         prepared.DriverName,
		OwnerType:    domain.DriverOwnerUser,
		Description:  "Dynamic driver published from " + prepared.SourceRef,
		Status:       domain.DriverStatusDraft,
		Metadata: map[string]string{
			"source_ref": prepared.SourceRef,
			"runtime":    RuntimeFlueNode,
			"entrypoint": EntrypointRun,
		},
	})
	if err != nil {
		return nil, false, fmt.Errorf("create driver: %w", err)
	}
	return driver, true, nil
}

func nextDriverVersion(ctx context.Context, s store.Store, ws, driverID string) (int, error) {
	versions, err := s.DriverVersions().List(ctx, ws, store.DriverVersionFilter{DriverID: driverID})
	if err != nil {
		return 0, fmt.Errorf("list driver versions: %w", err)
	}
	maxVersion := 0
	for _, version := range versions {
		if version != nil && version.DriverID == driverID && version.Version > maxVersion {
			maxVersion = version.Version
		}
	}
	return maxVersion + 1, nil
}

func failedManifest(prepared *PreparedWorkflow) map[string]string {
	return map[string]string{
		"schema_version": "1",
		"runtime":        RuntimeFlueNode,
		"driver_id":      prepared.DriverID,
		"driver_name":    prepared.DriverName,
		"workflow_name":  prepared.WorkflowName,
		"entrypoint":     EntrypointRun,
		"source_ref":     prepared.SourceRef,
		"source_digest":  prepared.SourceDigest,
		"validation":     "failed",
	}
}

func runFlueBuild(ctx context.Context, bundleRoot, outputDir string, flueCommand []string) error {
	command, err := resolveFlueCommand(flueCommand)
	if err != nil {
		return err
	}
	args := append(command[1:], "build", "--target", "node", "--root", bundleRoot, "--output", outputDir)
	cmd := exec.CommandContext(ctx, command[0], args...) //nolint:gosec // command is configured by the local operator.
	cmd.Dir = bundleRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		diagnostics := strings.TrimSpace(string(output))
		if diagnostics == "" {
			diagnostics = err.Error()
		}
		return fmt.Errorf("Flue build failed: %s", diagnostics)
	}
	return nil
}

func resolveFlueCommand(command []string) ([]string, error) {
	if len(command) > 0 {
		return append([]string(nil), command...), nil
	}
	if encoded := strings.TrimSpace(os.Getenv("LOOM_FLUE_BUILD_CMD_JSON")); encoded != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(encoded), &parsed); err != nil {
			return nil, fmt.Errorf("decode LOOM_FLUE_BUILD_CMD_JSON: %w", err)
		}
		if len(parsed) == 0 {
			return nil, fmt.Errorf("LOOM_FLUE_BUILD_CMD_JSON must contain at least one command element: %w", domain.ErrInvalid)
		}
		return parsed, nil
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_FLUE_BUILD_CMD")); raw != "" {
		return []string{raw}, nil
	}
	return []string{"flue"}, nil
}

func loomWorkflowAdapterSource(sourceRel, contextRel string) string {
	workflowDir := filepath.FromSlash(filepath.ToSlash(filepath.Join(".flue", "workflows")))
	sourceImport := relativeJSImport(workflowDir, filepath.FromSlash(sourceRel))
	contextImport := relativeJSImport(workflowDir, filepath.FromSlash(contextRel))
	return fmt.Sprintf(`import { createLoomDriverContext } from %s;

globalThis.defineDriver = globalThis.defineDriver || ((definition) => definition);

const userModulePromise = import(%s);
const entrypoint = %s;

export async function run(flueCtx) {
  const mod = await userModulePromise;
  const entry = mod[entrypoint] || mod.default;
  const ctx = createLoomDriverContext(flueCtx?.payload || {});
  if (typeof entry === 'function') {
    return await entry(ctx);
  }
  if (entry && typeof entry.run === 'function') {
    return await entry.run(ctx);
  }
  throw new Error('driver entrypoint ' + entrypoint + ' is not a function or defineDriver object');
}
`, jsString(contextImport), jsString(sourceImport), jsString(EntrypointRun))
}

func relativeJSImport(fromDir, target string) string {
	rel, err := filepath.Rel(fromDir, target)
	if err != nil {
		rel = target
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel
}

func jsString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func digestBundleTree(bundleRoot string, manifest []byte) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("loom-driver-bundle-v2\nmanifest\n"))
	_, _ = h.Write(manifest)
	_, _ = h.Write([]byte("\nfiles\n"))
	paths := []string{}
	for _, root := range []string{".flue", "dist"} {
		err := filepath.WalkDir(filepath.Join(bundleRoot, filepath.FromSlash(root)), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(bundleRoot, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("digest bundle tree: %w", err)
		}
	}
	sort.Strings(paths)
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(bundleRoot, filepath.FromSlash(rel))) //nolint:gosec // rel is generated by WalkDir under bundleRoot.
		if err != nil {
			return "", fmt.Errorf("digest bundle file %s: %w", rel, err)
		}
		_, _ = h.Write([]byte("file\n"))
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte("\n"))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte("\n"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func digestBundleLegacy(manifest, source []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte("loom-driver-bundle-v1\nmanifest\n"))
	_, _ = h.Write(manifest)
	_, _ = h.Write([]byte("\nsource\n"))
	_, _ = h.Write(source)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

const loomDriverContextModule = `
const taskRunResultsByTaskId = new Map();
const taskRunResultsByRunId = new Map();

function complete(payload = {}) {
  return { status: 'completed', summary: payload.summary || 'completed' };
}

function failed(payload = {}) {
  return { status: 'failed', summary: payload.summary || 'failed', errorClass: payload.errorClass || 'driver_failed' };
}

function needsHuman(payload = {}) {
  return { status: 'needs_human', summary: payload.summary || 'needs human', errorClass: payload.errorClass || 'needs_human' };
}

function execTaskCommand() {
  const jsonCommand = process.env.LOOM_DRIVER_EXEC_TASK_CMD_JSON || '';
  if (jsonCommand) {
    const parsed = JSON.parse(jsonCommand);
    if (!Array.isArray(parsed) || parsed.length === 0) {
      throw new Error('LOOM_DRIVER_EXEC_TASK_CMD_JSON must be a non-empty string array');
    }
    return parsed.map(String);
  }
  const command = process.env.LOOM_DRIVER_EXEC_TASK_CMD || 'loom';
  return [command];
}

async function requestTaskRun(payload = {}) {
  const taskId = payload.taskId || payload.task_id;
  if (!taskId) throw new Error('ctx.taskRuns.request requires taskId');
  const driverRunId = process.env.LOOM_DRIVER_RUN_ID || '';
  if (!driverRunId) throw new Error('ctx.taskRuns.request requires LOOM_DRIVER_RUN_ID');
  const providerProfile = payload.providerProfile || payload.provider_profile || '';
  const taskRunId = payload.taskRunId || payload.task_run_id || '';
  const workerProfileId = payload.workerProfileId || payload.worker_profile_id || '';
  const runnerId = payload.runnerId || payload.runner_id || '';
  const supportedProviders = payload.supportedProviders || payload.supported_providers || [];
  const capabilities = payload.capabilities || [];
  const sandboxPlacement = payload.sandboxPlacement || payload.sandbox_placement || {};
  const command = execTaskCommand();
  const args = command.slice(1).concat([
    'driver', 'exec-task',
    '--driver-run-id', driverRunId,
    '--task-id', String(taskId),
    '--provider-profile', String(providerProfile),
    '--json',
  ]);
  const workspace = process.env.LOOM_DRIVER_WORKSPACE || '';
  if (workspace) args.push('--workspace-key', workspace);
  if (taskRunId) args.push('--task-run-id', String(taskRunId));
  if (workerProfileId) args.push('--worker-profile-id', String(workerProfileId));
  if (runnerId) args.push('--runner-id', String(runnerId));
  appendRepeatedFlag(args, '--supported-provider', supportedProviders);
  appendRepeatedFlag(args, '--capability', capabilities);
  appendStringFlag(args, '--sandbox-provider', sandboxPlacement.provider || payload.sandboxProvider || payload.sandbox_provider || '');
  appendStringFlag(args, '--sandbox-id', sandboxPlacement.sandbox_id || sandboxPlacement.sandboxId || payload.sandboxId || payload.sandbox_id || '');
  appendStringFlag(args, '--sandbox-cwd', sandboxPlacement.cwd || payload.sandboxCwd || payload.sandbox_cwd || '');
  appendStringFlag(args, '--sandbox-repo-ref', sandboxPlacement.repo_ref || sandboxPlacement.repoRef || payload.sandboxRepoRef || payload.sandbox_repo_ref || '');
  args.push('--defer-completion');
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) throw proc.error;
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver exec-task failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  if (!stdout) throw new Error('loom driver exec-task returned empty output');
  const result = JSON.parse(stdout);
  rememberTaskRunResult(result);
  return result;
}

function appendStringFlag(args, flag, value) {
  if (value !== undefined && value !== null && String(value).trim() !== '') {
    args.push(flag, String(value));
  }
}

function appendRepeatedFlag(args, flag, values) {
  const list = Array.isArray(values) ? values : (values ? [values] : []);
  for (const value of list) appendStringFlag(args, flag, value);
}

function rememberTaskRunResult(result = {}) {
  const runId = result.taskRunId || result.task_run_id || result.id || '';
  const taskId = result.taskId || result.task_id || '';
  if (runId) taskRunResultsByRunId.set(String(runId), result);
  if (taskId) taskRunResultsByTaskId.set(String(taskId), result);
}

async function claimReadyTask(payload = {}) {
  const driverRunId = process.env.LOOM_DRIVER_RUN_ID || '';
  if (!driverRunId) throw new Error('ctx.tasks.claimReady requires LOOM_DRIVER_RUN_ID');
  const epicId = payload.epicId || payload.epic_id || '';
  const command = execTaskCommand();
  const args = command.slice(1).concat(['driver', 'claim-ready', '--driver-run-id', driverRunId, '--json']);
  const workspace = process.env.LOOM_DRIVER_WORKSPACE || '';
  if (workspace) args.push('--workspace-key', workspace);
  if (epicId) args.push('--epic-id', String(epicId));
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) throw proc.error;
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver claim-ready failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  return stdout ? JSON.parse(stdout) : null;
}

function taskPayloadID(payload) {
  if (typeof payload === 'string') return payload;
  return payload.taskId || payload.task_id || payload.id || '';
}

async function completeTask(payload = {}) {
  const taskId = taskPayloadID(payload);
  const requestedTaskRunId = payload.taskRunId || payload.task_run_id || '';
  const remembered = requestedTaskRunId ? taskRunResultsByRunId.get(String(requestedTaskRunId)) : taskRunResultsByTaskId.get(String(taskId));
  const taskRunId = requestedTaskRunId || remembered?.taskRunId || remembered?.task_run_id || remembered?.id || '';
  if (!taskId && !taskRunId) throw new Error('ctx.tasks.complete requires taskId or taskRunId');
  const driverRunId = process.env.LOOM_DRIVER_RUN_ID || '';
  if (!driverRunId) throw new Error('ctx.tasks.complete requires LOOM_DRIVER_RUN_ID');
  const command = execTaskCommand();
  const args = command.slice(1).concat(['driver', 'complete-task', '--driver-run-id', driverRunId, '--json']);
  const workspace = process.env.LOOM_DRIVER_WORKSPACE || '';
  if (taskId) args.push('--task-id', String(taskId));
  if (workspace) args.push('--workspace-key', workspace);
  if (payload.reason) args.push('--reason', String(payload.reason));
  if (taskRunId) args.push('--task-run-id', String(taskRunId));
  const completionId = payload.completionId || payload.completion_id || '';
  if (completionId) args.push('--completion-id', String(completionId));
  const leaseToken = payload.leaseToken || payload.lease_token || process.env.LOOM_TASK_RUN_LEASE_TOKEN || process.env.LOOM_RUNNER_LEASE_TOKEN || '';
  if (leaseToken) args.push('--lease-token', String(leaseToken));
  const logsRef = payload.logsRef || payload.logs_ref || remembered?.logsRef || remembered?.logs_ref || '';
  if (logsRef) args.push('--logs-ref', String(logsRef));
  const artifactsRef = payload.artifactsRef || payload.artifacts_ref || remembered?.artifactsRef || remembered?.artifacts_ref || '';
  if (artifactsRef) args.push('--artifacts-ref', String(artifactsRef));
  const artifactIds = payload.artifactIds || payload.artifact_ids || remembered?.artifactIds || remembered?.artifact_ids || [];
  if (Array.isArray(artifactIds)) {
    for (const artifactId of artifactIds) {
      if (artifactId) args.push('--artifact-id', String(artifactId));
    }
  }
  if (payload.session) args.push('--session', String(payload.session));
  if (payload.force) args.push('--force');
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) throw proc.error;
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver complete-task failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  return stdout ? JSON.parse(stdout) : { id: String(taskId) };
}

async function releaseTask(payload = {}) {
  const taskId = taskPayloadID(payload);
  if (!taskId) throw new Error('ctx.tasks.release requires taskId');
  const driverRunId = process.env.LOOM_DRIVER_RUN_ID || '';
  if (!driverRunId) throw new Error('ctx.tasks.release requires LOOM_DRIVER_RUN_ID');
  const command = execTaskCommand();
  const args = command.slice(1).concat(['driver', 'release-task', '--driver-run-id', driverRunId, '--task-id', String(taskId), '--json']);
  const workspace = process.env.LOOM_DRIVER_WORKSPACE || '';
  if (workspace) args.push('--workspace-key', workspace);
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) throw proc.error;
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver release-task failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  return stdout ? JSON.parse(stdout) : { id: String(taskId), released: true };
}

export function createLoomDriverContext(input = {}) {
  return {
    input,
    run: { complete, failed, needsHuman },
    tasks: { claimReady: claimReadyTask, complete: completeTask, release: releaseTask },
    taskRuns: { request: requestTaskRun },
  };
}
`

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestShort(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func slug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func validateBalancedSyntax(src string) error {
	var stack []rune
	inLineComment := false
	inBlockComment := false
	var quote rune
	escaped := false
	for i, r := range src {
		var next rune
		if i+1 < len(src) {
			next = rune(src[i+1])
		}
		if inLineComment {
			if r == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if r == '*' && next == '/' {
				inBlockComment = false
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '/' && next == '/' {
			inLineComment = true
			continue
		}
		if r == '/' && next == '*' {
			inBlockComment = true
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		switch r {
		case '(', '{', '[':
			stack = append(stack, r)
		case ')', '}', ']':
			if len(stack) == 0 || matchingOpen(r) != stack[len(stack)-1] {
				return fmt.Errorf("unmatched %q", string(r))
			}
			stack = stack[:len(stack)-1]
		}
	}
	if quote != 0 {
		return fmt.Errorf("unterminated string literal")
	}
	if inBlockComment {
		return fmt.Errorf("unterminated block comment")
	}
	if len(stack) > 0 {
		return fmt.Errorf("unclosed %q", string(stack[len(stack)-1]))
	}
	return nil
}

func matchingOpen(close rune) rune {
	switch close {
	case ')':
		return '('
	case '}':
		return '{'
	case ']':
		return '['
	default:
		return 0
	}
}
