package sandbox

// SandboxLauncher is the executor-level sandbox seam (§7 step 9, SB1):
// NodeRunner launches workflow bundles through this interface, with today's
// local node-fork behavior as the default implementation, so container/gVisor
// launchers (SB2) slot in without re-touching the executor — the same seam
// strategy as loomExecutablePath.
//
// IPC contract (transport-agnostic JSON-lines frames):
//
//   - The host encodes the invoke into the launch environment
//     (LOOM_FLUE_INVOKE_PAYLOAD et al, built by flueRuntimeEnv and passed
//     verbatim via LaunchSpec.Env); inside the sandbox the embedded runtime
//     launcher relays it to the built Flue server as an invoke frame.
//   - The runtime's log stream rides stderr verbatim.
//   - stdout carries the host-bound frame stream as JSON lines. The last
//     non-empty line is the terminal result frame {status,summary,errorClass}
//     (decoded by flueRuntimeResult). For v0 compatibility the result frame
//     omits the type discriminator; started/event frames are reserved for
//     SB2 streaming, and hosts ignore lines before the terminal frame.
//
// The process transport below preserves the pre-seam cmd.Run behavior; a
// container transport (SB2) carries the same frames over the container's
// stdio (--network=none with a unix-socket relay to serve for egress).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

// SandboxFrameType discriminates host-bound frames on the sandbox stdout
// stream. A line without a type field is the terminal result frame (the
// local runtime launcher emits exactly one such line).
type SandboxFrameType string

const (
	SandboxFrameInvoke  SandboxFrameType = "invoke"
	SandboxFrameStarted SandboxFrameType = "started"
	SandboxFrameEvent   SandboxFrameType = "event"
	SandboxFrameResult  SandboxFrameType = "result"
)

// SandboxFrame is the JSON-lines frame envelope (driver wire camelCase).
// Result frames carry the terminal status triple; invoke/event frames carry
// Payload.
type SandboxFrame struct {
	Version    int              `json:"version,omitempty"`
	Type       SandboxFrameType `json:"type,omitempty"`
	RequestID  string           `json:"requestId,omitempty"`
	Payload    json.RawMessage  `json:"payload,omitempty"`
	Status     string           `json:"status,omitempty"`
	Summary    string           `json:"summary,omitempty"`
	ErrorClass string           `json:"errorClass,omitempty"`
}

// SandboxProviderProcess is the placement provider of the default local
// node-process launcher.
const SandboxProviderProcess = "process"

// SandboxPlacementOutputKey is the run-output key carrying the launcher's
// JSON-encoded execution.TaskRunPlacementRecord descriptor (§9.6 audit).
const SandboxPlacementOutputKey = "sandbox_placement"

// SandboxLauncher launches one workflow-bundle runtime per driver run.
type SandboxLauncher interface {
	Launch(ctx context.Context, spec LaunchSpec) (SandboxProcess, error)
}

// LaunchSpec describes one sandboxed workflow execution. Env is the
// runtime's complete environment, passed verbatim (no host inheritance):
// the runner has already minimized it and embedded the invoke payload, so
// launchers must not add or drop entries.
type LaunchSpec struct {
	BundleRoot string
	ServerPath string
	// WorkDir is the runtime's working directory; empty means BundleRoot.
	WorkDir  string
	Env      []string
	Manifest map[string]string
	// TrustLevel is the run's driver trust level (SB3). The container
	// launcher resolves its default egress mode from it (SB4): trusted →
	// all, anything else — including empty — → serve-only, fail closed.
	TrustLevel workflowcatalog.DriverTrustLevel
}

// SandboxProcess is one launched runtime. Cooperative cancellation stays
// ctx-driven through Launch's context; Kill is the hard stop.
type SandboxProcess interface {
	// Wait blocks until the runtime exits and returns the captured stdio.
	// The error mirrors exec.Cmd.Run: start failures and non-zero exits
	// surface here so the runner maps them onto driver-run results exactly
	// as the pre-seam path did. Wait must be called exactly once.
	Wait() (SandboxExit, error)
	// Kill force-terminates the runtime. Idempotent and safe after exit.
	Kill() error
	// Placement reports where the runtime ran; the runner records it onto
	// the run output (§9.6). Empty when the launch never started.
	Placement() execution.TaskRunPlacementRecord
}

// SandboxExit carries the captured stdio after the runtime exits: Stdout is
// the JSON-lines frame stream (last non-empty line = terminal result frame),
// Stderr is the runtime log stream verbatim.
type SandboxExit struct {
	Stdout string
	Stderr string
}

// RecordSandboxPlacement stamps the launcher's placement descriptor onto the
// run result output: Finish persists Output onto the DriverRun row, so the
// row records where the workflow executed (§9.6 audit).
func RecordSandboxPlacement(output map[string]string, placement execution.TaskRunPlacementRecord) map[string]string {
	if placement.Empty() {
		return output
	}
	encoded, err := json.Marshal(placement)
	if err != nil {
		return output
	}
	if output == nil {
		output = map[string]string{}
	}
	output[SandboxPlacementOutputKey] = string(encoded)
	return output
}

// ProcessLauncher is the default SandboxLauncher: it forks the bundle's
// built Flue server under local node via the embedded runtime launcher —
// the pre-seam runBuiltFlueServer behavior, unchanged.
type ProcessLauncher struct {
	// NodePath overrides the node executable (default "node").
	NodePath string
}

func (l ProcessLauncher) Launch(ctx context.Context, spec LaunchSpec) (SandboxProcess, error) {
	node := strings.TrimSpace(l.NodePath)
	if node == "" {
		node = "node"
	}
	launcherPath, cleanupLauncher, err := writeFlueRuntimeLauncher()
	if err != nil {
		return nil, err
	}
	workDir := spec.WorkDir
	if workDir == "" {
		workDir = spec.BundleRoot
	}
	process := &processSandbox{cleanup: cleanupLauncher}
	cmd := flueRuntimeCommand(ctx, node, launcherPath, workDir, spec.Env)
	cmd.Stdout = &process.stdout
	cmd.Stderr = &process.stderr
	process.cmd = cmd
	if startErr := cmd.Start(); startErr != nil {
		// A start failure surfaces from Wait exactly like exec.Cmd.Run
		// reported it pre-seam: the runner maps it onto a failed
		// driver_runtime result instead of a launch error.
		process.startErr = startErr
		return process, nil
	}
	process.placement = execution.TaskRunPlacementRecord{
		Provider:   SandboxProviderProcess,
		ProcessRef: strconv.Itoa(cmd.Process.Pid),
		CWD:        workDir,
		StartedAt:  time.Now().UTC(),
	}
	return process, nil
}

type processSandbox struct {
	cmd       *exec.Cmd
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	cleanup   func()
	startErr  error
	placement execution.TaskRunPlacementRecord
	waitOnce  sync.Once
	waitErr   error
}

func (p *processSandbox) Wait() (SandboxExit, error) {
	p.waitOnce.Do(func() {
		defer p.cleanup()
		if p.startErr != nil {
			p.waitErr = p.startErr
			return
		}
		p.waitErr = p.cmd.Wait()
	})
	return SandboxExit{Stdout: p.stdout.String(), Stderr: p.stderr.String()}, p.waitErr
}

func (p *processSandbox) Kill() error {
	if p.startErr != nil || p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (p *processSandbox) Placement() execution.TaskRunPlacementRecord {
	return p.placement
}

func writeFlueRuntimeLauncher() (string, func(), error) {
	launcher, err := os.CreateTemp("", "loom-flue-runtime-*.mjs")
	if err != nil {
		return "", nil, fmt.Errorf("create Flue runtime launcher: %w", err)
	}
	cleanup := func() { _ = os.Remove(launcher.Name()) }
	if _, err := launcher.WriteString(flueLocalLauncher); err != nil {
		_ = launcher.Close()
		cleanup()
		return "", nil, fmt.Errorf("write Flue runtime launcher: %w", err)
	}
	if err := launcher.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close Flue runtime launcher: %w", err)
	}
	return launcher.Name(), cleanup, nil
}

func flueRuntimeCommand(ctx context.Context, node, launcherPath, workDir string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, node, launcherPath) //nolint:gosec // node is operator-configured; launcherPath is a temp file created by this process.
	cmd.Dir = workDir
	cmd.Env = env
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

const flueLocalLauncher = `
import { fork } from 'node:child_process';

const serverPath = process.env.LOOM_FLUE_SERVER_PATH;
const bundleRoot = process.env.LOOM_FLUE_BUNDLE_ROOT || process.cwd();
const workflowName = process.env.LOOM_FLUE_WORKFLOW_NAME;
const payload = JSON.parse(process.env.LOOM_FLUE_INVOKE_PAYLOAD || '{}');
const requestId = process.env.LOOM_DRIVER_RUN_ID || 'loom-driver-run';

if (!serverPath || !workflowName) {
  console.log(JSON.stringify({ status: 'failed', summary: 'missing Flue server path or workflow name', errorClass: 'driver_runtime' }));
  process.exit(0);
}

let completed = false;
let invoked = false;
const child = fork(serverPath, [], {
  cwd: bundleRoot,
  env: {
    ...process.env,
    FLUE_MODE: 'local',
    FLUE_CLI_TARGET: 'workflow',
    FLUE_CLI_NAME: workflowName,
    // Flue HEAD gates one-shot IPC mode behind this explicit internal flag (in
    // addition to FLUE_CLI_TARGET + an inherited IPC channel) so user-supplied
    // FLUE_CLI_* can never flip a production HTTP server into IPC mode. Without
    // it the generated entry serves HTTP on :3000 instead of performing the
    // invoke/result handshake the driver workflow executor depends on.
    FLUE_INTERNAL_CLI_IPC: '1',
  },
  stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
});

child.stdout?.on('data', (data) => process.stderr.write(data));
child.stderr?.on('data', (data) => process.stderr.write(data));

function finish(result) {
  if (completed) return;
  completed = true;
  console.log(JSON.stringify(result || {}));
  try { child.disconnect(); } catch {}
}

// Suspension (AW11): an await op suspended the run server-side; the workflow
// signals it by letting the SDK's WorkflowSuspended sentinel propagate
// (recognized by type/name or the 'workflow_suspended:' message prefix) or
// by returning a suspended-status result. Either way the launcher exits
// cleanly with the suspended shape — the executor skips Finish and the
// resumed run re-runs from the top.
const suspendedShape = { status: 'suspended_awaiting_event', summary: 'workflow suspended awaiting event' };

function isSuspendedResult(result) {
  const status = String((result && result.status) || '');
  return status === 'suspended' || status === 'suspended_awaiting_event';
}

function isSuspendedError(error) {
  // Flue HEAD's IPC error envelope rewrites every error to {type:'internal_error', message,
  // details} (build-plugin-node.ts ipcErrorMessage), so the type/name branch below is dead for
  // HEAD bundles — the 'workflow_suspended:' message prefix is the de-facto contract that must be
  // preserved by the SDK (sdk/driver.js WorkflowSuspended) for suspend/resume to work. The
  // type/name check is kept for older bundles + defense in depth.
  const type = String((error && (error.type || error.name)) || '');
  if (type === 'workflow_suspended' || type === 'WorkflowSuspended') return true;
  return String((error && error.message) || '').startsWith('workflow_suspended:');
}

child.on('message', (message) => {
  if (!message || typeof message !== 'object') return;
  if (message.type === 'ready' && !invoked) {
    invoked = true;
    child.send({ version: 1, type: 'invoke', requestId, payload });
    return;
  }
  if (message.type === 'result') {
    const result = message.result || {};
    finish(isSuspendedResult(result) ? suspendedShape : result);
    return;
  }
  if (message.type === 'error') {
    const error = message.error || {};
    if (isSuspendedError(error)) {
      finish(suspendedShape);
      return;
    }
    finish({
      status: 'failed',
      summary: error.message || error.details || 'Flue workflow failed',
      errorClass: error.type || 'driver_runtime',
    });
  }
});

child.on('error', (error) => {
  finish({ status: 'failed', summary: error?.message || String(error), errorClass: 'driver_runtime' });
});

function shutdown(signal) {
  if (completed) return;
  completed = true;
  try { child.kill(signal); } catch {}
  setTimeout(() => {
    try { child.kill('SIGKILL'); } catch {}
  }, 1000).unref?.();
  console.log(JSON.stringify({
    status: 'cancelled',
    summary: 'Flue local runner cancelled',
    errorClass: 'driver_cancelled',
  }));
  process.exit(signal === 'SIGINT' ? 130 : 143);
}

process.once('SIGINT', () => shutdown('SIGINT'));
process.once('SIGTERM', () => shutdown('SIGTERM'));

child.on('exit', (code, signal) => {
  if (completed) return;
  finish({
    status: 'failed',
    summary: 'Flue local runner exited before result' + (signal ? ' signal=' + signal : ' code=' + code),
    errorClass: 'driver_runtime',
  });
});
`
