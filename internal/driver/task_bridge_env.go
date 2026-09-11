package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
)

func (e HostBridgeTaskExecutor) taskRunnerEnv(req TaskExecRequest, requestJSON string) []string {
	env := []string{
		"LOOM_TASK_RUN_REQUEST_JSON=" + requestJSON,
		"LOOM_WORKTREE_PATH=" + strings.TrimSpace(e.WorktreePath),
		"LOOM_DRIVER_WORKSPACE=" + req.WorkspaceKey,
		"LOOM_DRIVER_RUN_ID=" + req.DriverRunID,
		"LOOM_DRIVER_STEP_ID=" + req.DriverStepID,
		"LOOM_PARENT_SESSION_ID=" + req.ParentSessionID,
		"LOOM_TASK_RUN_ID=" + req.TaskRunID,
		"LOOM_TASK_ID=" + req.TaskID,
		"LOOM_TASK_RUN_PARENT_SESSION_ID=" + req.ParentSessionID,
		"LOOM_TASK_RUN_WORKER_PROFILE_ID=" + req.WorkerProfileID,
		"LOOM_TASK_RUNNER=" + req.Runner,
		"LOOM_TASK_RUNNER_REF=" + req.RunnerRef,
		"LOOM_TASK_RUNNER_KIND=" + req.RunnerKind,
		"LOOM_TASK_RUNNER_ENTRYPOINT=" + req.RunnerEntrypoint,
		"LOOM_TASK_RUNNER_DRIVER_VERSION_ID=" + req.RunnerVersionID,
		"LOOM_TASK_RUNNER_TRUST_LEVEL=" + string(taskRunnerTrustLevel(req.RunnerTrustLevel)),
		"LOOM_TASK_RUN_PROVIDER_PROFILE=" + req.ProviderProfile,
		"LOOM_TASK_RUN_NODE_ID=" + req.NodeID,
		"LOOM_TASK_RUN_LEASE_ID=" + req.LeaseID,
		"LOOM_TASK_RUN_LEASE_TOKEN=" + req.LeaseToken,
		fmt.Sprintf("LOOM_TASK_RUN_FENCING_TOKEN=%d", req.FencingToken),
		"LOOM_TASK_RUN_RUNNER_PLACEMENT_JSON=" + taskRunPlacementJSON(req.RunnerPlacement),
		"LOOM_TASK_RUN_SANDBOX_PLACEMENT_JSON=" + taskRunPlacementJSON(req.SandboxPlacement),
	}
	if apiBaseURL := strings.TrimSpace(e.APIBaseURL); apiBaseURL != "" {
		env = append(env, "LOOM_TASK_RUN_API_URL="+apiBaseURL)
	}
	if manifest := strings.TrimSpace(e.taskRootManifest); manifest != "" {
		env = append(env, "LOOM_TASK_ROOT_MANIFEST="+manifest)
	}
	if e.stackBinding != nil {
		env = append(env,
			"LOOM_TASK_RUN_STACKED=1",
			"LOOM_TASK_RUN_STACK_ID="+e.stackBinding.StackID,
			"LOOM_TASK_RUN_OUTPUT_BRANCH="+e.stackBinding.OutputBranch,
			"LOOM_TASK_RUN_BASE_REF="+e.stackBinding.BaseRef,
		)
	}
	env = append(env, e.taskRunnerBundleEnv(req)...)
	if isLocalTaskRunner(req) {
		env = append(env, TaskRunnerBackendEnv+"="+e.resolveTaskRunnerBackend(req))
		env = append(env, e.localTaskRunnerSettingsEnv()...)
	}
	return env
}

func (e HostBridgeTaskExecutor) localTaskRunnerSettingsEnv() []string {
	dir := strings.TrimSpace(e.LocalSettingsDir)
	if dir == "" {
		return nil
	}
	settings, err := runtimesettings.Load(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, 1)
	if model := strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel); model != "" {
		out = append(out, "LOOM_OPENCODE_MODEL="+model)
	}
	return out
}

// resolveTaskRunnerBackend mirrors service.GetWorkspaceBackend precedence.
func (e HostBridgeTaskExecutor) resolveTaskRunnerBackend(req TaskExecRequest) string {
	if e.Store == nil {
		return defaultTaskRunnerBackend
	}
	ctx := context.Background()
	if worker := strings.TrimSpace(req.WorkerProfileID); worker != "" {
		if agent, err := e.Store.Agents().Get(ctx, req.WorkspaceKey, worker); err == nil && agent != nil {
			if backend := strings.TrimSpace(agent.Backend); backend != "" {
				return backend
			}
		}
	}
	if profile, err := e.Store.Daemon().Get(ctx, req.WorkspaceKey); err == nil && profile != nil {
		if backend := strings.TrimSpace(profile.AgentBackend); backend != "" {
			return backend
		}
	}
	return defaultTaskRunnerBackend
}

func (e HostBridgeTaskExecutor) taskRunnerBundleEnv(req TaskExecRequest) []string {
	if e.Store == nil || strings.TrimSpace(req.RunnerVersionID) == "" {
		return nil
	}
	version, err := e.Store.DriverVersions().Get(context.Background(), req.WorkspaceKey, req.RunnerVersionID)
	if err != nil || version.BundleRef == "" {
		return nil
	}
	for _, base := range []string{strings.TrimSpace(e.WorktreePath), strings.TrimSpace(e.driverBundleBaseDir)} {
		if base == "" {
			continue
		}
		bundleRoot, err := safeBundleRoot(base, version.BundleRef)
		if err != nil {
			continue
		}
		manifest, serverPath, err := verifyBundleManifest(bundleRoot, version.BundleDigest)
		if err != nil {
			continue
		}
		encoded, err := json.Marshal(manifest)
		if err != nil {
			continue
		}
		return []string{
			"LOOM_TASK_RUNNER_BUNDLE_ROOT=" + bundleRoot,
			"LOOM_TASK_RUNNER_SERVER_PATH=" + serverPath,
			"LOOM_TASK_RUNNER_MANIFEST_JSON=" + string(encoded),
		}
	}
	return nil
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
