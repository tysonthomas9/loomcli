//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/noderuntime"
)

// These tests pin the host-side Node exec sites in internal/driver onto
// noderuntime.Resolve: LOOM_NODE_BIN is honored verbatim, and an unusable
// override fails closed BEFORE any launcher temp file is written.

const missingNodeOverride = "/nonexistent/loom-test/node"

// nodeResolutionSandbox resets the resolver cache around the test and points
// TMPDIR at a fresh, empty directory so launcher temp files (os.CreateTemp)
// can be observed by listing it. It returns that directory.
func nodeResolutionSandbox(t *testing.T) string {
	t.Helper()
	noderuntime.ResetForTest()
	t.Cleanup(noderuntime.ResetForTest)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

// writeFakeNodeWithMarker writes a shell stand-in for node that records
// "$0 $*" into marker and then prints resultLine on stdout. The marker path
// is embedded because the driver sites filter the inherited env, so a test
// env var would not reach the child.
func writeFakeNodeWithMarker(t *testing.T, marker, resultLine string) string {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s %%s\\n' \"$0\" \"$*\" >> '%s'\nprintf '%%s\\n' '%s'\n", marker, resultLine)
	return writeExecutable(t, t.TempDir(), "fake-node", script)
}

func readNodeMarker(t *testing.T, marker string) string {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("fake node was not exec'd (marker %s): %v", marker, err)
	}
	return string(data)
}

func requireNoNodeMarker(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker %s stat err = %v, want not-exist (no node exec)", marker, err)
	}
}

func requireNoTempFiles(t *testing.T, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", tmp, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("TMPDIR %s not empty: %v (launcher temp file written before node resolution failed)", tmp, names)
	}
}

func requireMissingNodeError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, noderuntime.ErrNodeRuntimeMissing) {
		t.Fatalf("err = %v, want node_runtime_missing", err)
	}
	if !strings.Contains(err.Error(), noderuntime.EnvNodeBin) {
		t.Fatalf("err = %q, want mention of %s", err, noderuntime.EnvNodeBin)
	}
}

func requireMarkerRecords(t *testing.T, marker, fakeNode, launcherPrefix string) {
	t.Helper()
	contents := readNodeMarker(t, marker)
	if !strings.Contains(contents, fakeNode) {
		t.Fatalf("marker = %q, want exec of %s", contents, fakeNode)
	}
	if !strings.Contains(contents, launcherPrefix) {
		t.Fatalf("marker = %q, want launcher arg matching %s*", contents, launcherPrefix)
	}
}

// --- RunBundledTaskRunner ---

func TestRunBundledTaskRunnerNodeResolutionExecsOverride(t *testing.T) {
	nodeResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	fakeNode := writeFakeNodeWithMarker(t, marker, `{"status":"completed"}`)
	t.Setenv(noderuntime.EnvNodeBin, fakeNode)

	raw, err := RunBundledTaskRunner(context.Background(), BundledRunnerOptions{
		ServerPath: filepath.Join(t.TempDir(), "server.mjs"),
		Stderr:     os.Stderr,
	})
	if err != nil {
		t.Fatalf("RunBundledTaskRunner: %v", err)
	}
	var result bundledResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v\nraw: %s", err, raw)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed; raw: %s", result.Status, raw)
	}
	requireMarkerRecords(t, marker, fakeNode, "loom-flue-task-runner-")
}

func TestRunBundledTaskRunnerNodeResolutionMissingOverrideFailsClosed(t *testing.T) {
	tmp := nodeResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv(noderuntime.EnvNodeBin, missingNodeOverride)

	raw, err := RunBundledTaskRunner(context.Background(), BundledRunnerOptions{
		ServerPath: filepath.Join(t.TempDir(), "server.mjs"),
		Stderr:     os.Stderr,
	})
	requireMissingNodeError(t, err)
	if raw != nil {
		t.Fatalf("raw = %s, want nil on resolution failure", raw)
	}
	requireNoNodeMarker(t, marker)
	requireNoTempFiles(t, tmp)
}

// --- HostBridgeTaskExecutor.runBuiltInFlueWorkflow ---

func flueWorkflowBridgeRequest() TaskExecRequest {
	req := hostBridgeTaskExecRequest()
	req.Runner = LocalTaskRunnerEntrypoint
	req.RunnerKind = RunnerKindFlueWorkflow
	req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
	req.RunnerTrustLevel = domain.DriverTrustTrusted
	return req
}

func TestRunBuiltInFlueWorkflowNodeResolutionExecsOverride(t *testing.T) {
	nodeResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	fakeNode := writeFakeNodeWithMarker(t, marker, `{"status":"completed","exitCode":0,"logsRef":"logs://task-run-1"}`)
	t.Setenv(noderuntime.EnvNodeBin, fakeNode)

	executor := HostBridgeTaskExecutor{WorktreePath: t.TempDir()}
	result, err := executor.runBuiltInFlueWorkflow(context.Background(), flueWorkflowBridgeRequest())
	if err != nil {
		t.Fatalf("runBuiltInFlueWorkflow: %v", err)
	}
	if result.Status != domain.TaskRunCompleted || result.LogsRefCamel != "logs://task-run-1" {
		t.Fatalf("result = %+v, want completed result from fake node", result)
	}
	requireMarkerRecords(t, marker, fakeNode, "loom-flue-task-runner-")
}

func TestRunBuiltInFlueWorkflowNodeResolutionMissingOverrideFailsClosed(t *testing.T) {
	tmp := nodeResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv(noderuntime.EnvNodeBin, missingNodeOverride)

	executor := HostBridgeTaskExecutor{WorktreePath: t.TempDir()}
	result, err := executor.runBuiltInFlueWorkflow(context.Background(), flueWorkflowBridgeRequest())
	requireMissingNodeError(t, err)
	if result.Status != "" {
		t.Fatalf("result = %+v, want zero result on resolution failure", result)
	}
	requireNoNodeMarker(t, marker)
	requireNoTempFiles(t, tmp)
}

// --- NodeRunner.Run ---

func nodeRunnerRunRequest(t *testing.T) RunRequest {
	t.Helper()
	root := t.TempDir()
	return RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey:    "TEST",
			RunID:           "run-node-resolution",
			Payload:         json.RawMessage(`{}`),
			DriverID:        "driver-1",
			DriverVersionID: "version-1",
		},
		BundleRoot: root,
		ServerPath: filepath.Join(root, "dist", "server.mjs"),
		Manifest:   map[string]string{"workflow_name": "epic-runner"},
		TrustLevel: domain.DriverTrustTrusted,
	}
}

func TestNodeRunnerNodeResolutionExecsOverride(t *testing.T) {
	nodeResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	fakeNode := writeFakeNodeWithMarker(t, marker, `{"status":"completed","summary":"done"}`)
	t.Setenv(noderuntime.EnvNodeBin, fakeNode)

	result, err := (NodeRunner{}).Run(context.Background(), nodeRunnerRunRequest(t))
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted || result.Summary != "done" {
		t.Fatalf("result = %+v, want completed done", result)
	}
	requireMarkerRecords(t, marker, fakeNode, "loom-flue-runtime-")
}

func TestNodeRunnerNodeResolutionMissingOverrideFailsClosed(t *testing.T) {
	tmp := nodeResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv(noderuntime.EnvNodeBin, missingNodeOverride)

	result, err := (NodeRunner{}).Run(context.Background(), nodeRunnerRunRequest(t))
	if err != nil {
		t.Fatalf("NodeRunner.Run err = %v, want nil (failure is reported on the result)", err)
	}
	if result.Status != domain.DriverRunFailed || result.ErrorClass != "node_runtime_missing" {
		t.Fatalf("result = %+v, want failed node_runtime_missing", result)
	}
	if !strings.Contains(result.Summary, "node_runtime_missing") || !strings.Contains(result.Summary, noderuntime.EnvNodeBin) {
		t.Fatalf("summary = %q, want node_runtime_missing mentioning %s", result.Summary, noderuntime.EnvNodeBin)
	}
	requireNoNodeMarker(t, marker)
	requireNoTempFiles(t, tmp)
}

func TestNodeRunnerNodeResolutionExplicitNodePathIgnoresOverride(t *testing.T) {
	nodeResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	fakeNode := writeFakeNodeWithMarker(t, marker, `{"status":"completed","summary":"explicit"}`)
	t.Setenv(noderuntime.EnvNodeBin, missingNodeOverride)

	result, err := (NodeRunner{NodePath: fakeNode}).Run(context.Background(), nodeRunnerRunRequest(t))
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted || result.Summary != "explicit" {
		t.Fatalf("result = %+v, want completed via explicit NodePath", result)
	}
	requireMarkerRecords(t, marker, fakeNode, "loom-flue-runtime-")
}
