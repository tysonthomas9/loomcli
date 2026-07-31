// Package daytonahost owns the host-side Daytona provider process and its
// credential-contained stdin/IPC protocol.
package daytonahost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

const (
	daytonaProviderHostEntrypoint = "daytona-provider-host"
	maxDaytonaProviderInputBytes  = 2 << 20
	maxDaytonaProviderOutputBytes = 16 << 20
)

// Options is host-only process configuration. Credentials are written to the
// launcher over stdin and are never placed in argv or env.
type Options struct {
	ServerPath        string
	DaytonaSDKImport  string
	Command           execution.DaytonaProviderCommand
	DaytonaCredential []byte
	GitHubCredential  []byte
}

// Runtime contains host decisions that remain owned by the parent driver
// adapter: the packaged Node path and the already-sanitized subprocess base
// environment. InheritedEnv is consulted only for the explicit non-secret
// Daytona configuration allowlist.
type Runtime struct {
	NodePath     string
	BaseEnv      []string
	InheritedEnv []string
}

type daytonaProviderHostEnvelope struct {
	SchemaVersion string                           `json:"schemaVersion"`
	Execution     execution.DaytonaProviderCommand `json:"execution"`
	Credentials   daytonaProviderHostCredentials   `json:"credentials"`
}

type daytonaProviderHostCredentials struct {
	Daytona []byte `json:"daytona"`
	GitHub  []byte `json:"github,omitempty"`
}

// Run invokes the internal provider workflow through a dedicated stdin/IPC launcher
// and strictly decodes its credential-free receipt.
//
//nolint:funlen // The provider host owns one decode, validation, execution, and encoded-response protocol transaction.
func Run(
	ctx context.Context,
	opts Options,
	runtime Runtime,
) (execution.DaytonaProviderResult, error) {
	if strings.TrimSpace(opts.ServerPath) == "" {
		return execution.DaytonaProviderResult{}, errors.New("daytona provider host server path is required")
	}
	if strings.TrimSpace(opts.DaytonaSDKImport) == "" {
		return execution.DaytonaProviderResult{}, errors.New("daytona SDK import path is required")
	}
	if len(bytes.TrimSpace(opts.DaytonaCredential)) == 0 {
		return execution.DaytonaProviderResult{}, errors.New("daytona credential is required")
	}
	envelope := daytonaProviderHostEnvelope{
		SchemaVersion: execution.DaytonaProviderSchemaV1,
		Execution:     opts.Command,
		Credentials: daytonaProviderHostCredentials{
			Daytona: append([]byte(nil), opts.DaytonaCredential...),
			GitHub:  append([]byte(nil), opts.GitHubCredential...),
		},
	}
	input, err := json.Marshal(envelope)
	zeroBytes(envelope.Credentials.Daytona)
	zeroBytes(envelope.Credentials.GitHub)
	if err != nil {
		return execution.DaytonaProviderResult{}, fmt.Errorf("encode Daytona provider host input: %w", err)
	}
	defer zeroBytes(input)
	if len(input) > maxDaytonaProviderInputBytes {
		return execution.DaytonaProviderResult{}, errors.New("daytona provider host input exceeds size limit")
	}

	launcherPath, cleanup, err := writeDaytonaProviderHostLauncher()
	if err != nil {
		return execution.DaytonaProviderResult{}, err
	}
	defer cleanup()

	nodePath := strings.TrimSpace(runtime.NodePath)
	if nodePath == "" {
		nodePath = "node"
	}
	cmd := exec.CommandContext(ctx, nodePath, launcherPath) //nolint:gosec // parent resolves the packaged/operator Node; launcherPath is generated.
	cmd.Dir = filepath.Dir(opts.ServerPath)
	cmd.Env = daytonaProviderHostEnv(runtime.BaseEnv, runtime.InheritedEnv, opts)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{writer: &stdout, remaining: maxDaytonaProviderOutputBytes}
	cmd.Stderr = &limitedWriter{writer: &stderr, remaining: 64 << 10}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		detail := redactProviderSecrets(stderr.String(), opts.DaytonaCredential, opts.GitHubCredential)
		if strings.TrimSpace(detail) == "" {
			detail = "provider host process failed"
		}
		return execution.DaytonaProviderResult{}, fmt.Errorf("%s: %w", detail, err)
	}
	raw, err := lastJSONLine(stdout.Bytes())
	if err != nil {
		return execution.DaytonaProviderResult{}, fmt.Errorf("decode Daytona provider host output: %w", err)
	}
	if containsProviderSecret(raw, opts.DaytonaCredential, opts.GitHubCredential) {
		return execution.DaytonaProviderResult{}, errors.New("daytona provider host output contained credential material")
	}
	result, err := decodeDaytonaProviderResult(raw)
	if err != nil {
		return execution.DaytonaProviderResult{}, err
	}
	if err := validateDaytonaProviderHostResult(result); err != nil {
		return execution.DaytonaProviderResult{}, err
	}
	return result, nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return len(p), nil
	}
	write := p
	if len(write) > w.remaining {
		write = write[:w.remaining]
	}
	_, err := w.writer.Write(write)
	w.remaining -= len(write)
	return len(p), err
}

func daytonaProviderHostEnv(base, inherited []string, opts Options) []string {
	env := append([]string(nil), base...)
	env = append(env,
		"LOOM_TASK_RUNNER_SERVER_PATH="+strings.TrimSpace(opts.ServerPath),
		"LOOM_TASK_RUNNER_BUNDLE_ROOT="+filepath.Dir(strings.TrimSpace(opts.ServerPath)),
		"DAYTONA_SDK_IMPORT="+strings.TrimSpace(opts.DaytonaSDKImport),
	)
	allowed := map[string]struct{}{
		"CODEX_HOME":                     {},
		"LOOM_CODEX_AUTH_FILE":           {},
		"CODEX_AUTH_FILE":                {},
		"DAYTONA_API_URL":                {},
		"DAYTONA_TARGET":                 {},
		"DAYTONA_AUTO_STOP_MINUTES":      {},
		"DAYTONA_AUTO_DELETE_MINUTES":    {},
		"DAYTONA_REPO_DIR":               {},
		"DAYTONA_CLONE_TIMEOUT_SECONDS":  {},
		"DAYTONA_DIFF_TIMEOUT_SECONDS":   {},
		"DAYTONA_COMMIT_TIMEOUT_SECONDS": {},
		"DAYTONA_PUSH_TIMEOUT_SECONDS":   {},
		"DAYTONA_GIT_AUTHOR_NAME":        {},
		"DAYTONA_GIT_AUTHOR_EMAIL":       {},
		"KEEP_DAYTONA_SANDBOX":           {},
	}
	for _, entry := range inherited {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, keep := allowed[strings.TrimSpace(name)]; keep {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func lastJSONLine(stdout []byte) ([]byte, error) {
	lines := bytes.Split(bytes.TrimSpace(stdout), []byte("\n"))
	for index := len(lines) - 1; index >= 0; index-- {
		line := bytes.TrimSpace(lines[index])
		if len(line) > 0 {
			return line, nil
		}
	}
	return nil, fmt.Errorf("task runner command returned empty output: %w", domain.ErrInvalid)
}

func decodeDaytonaProviderResult(raw []byte) (execution.DaytonaProviderResult, error) {
	var result execution.DaytonaProviderResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("decode strict Daytona provider result: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return result, fmt.Errorf("decode strict Daytona provider result: %w", err)
	}
	return result, nil
}

func validateDaytonaProviderHostResult(result execution.DaytonaProviderResult) error {
	if result.SchemaVersion != execution.DaytonaProviderSchemaV1 {
		return errors.New("daytona provider host returned an unsupported schema")
	}
	switch result.Status {
	case "completed":
		if result.ExitCode != 0 {
			return errors.New("daytona provider host returned completed with non-zero exit")
		}
		if strings.TrimSpace(result.Sandbox.ID) == "" ||
			strings.TrimSpace(result.Sandbox.CWD) == "" ||
			strings.TrimSpace(result.Sandbox.RepoRef) == "" {
			return errors.New("daytona provider host returned incomplete materialization evidence")
		}
	case "failed", "cancelled":
	default:
		return fmt.Errorf("daytona provider host returned non-terminal status %q", result.Status)
	}
	if result.Sandbox.Provider != "daytona" {
		return errors.New("daytona provider host returned an invalid sandbox receipt")
	}
	if len(result.Logs) > 4<<20 || len(result.Transcript) > 4<<20 {
		return errors.New("daytona provider host returned oversized evidence")
	}
	if result.Patch != nil && len(result.Patch.Content) > 8<<20 {
		return errors.New("daytona provider host returned an oversized patch")
	}
	return nil
}

func containsProviderSecret(value []byte, secrets ...[]byte) bool {
	for _, secret := range secrets {
		secret = bytes.TrimSpace(secret)
		if len(secret) >= 4 {
			if bytes.Contains(value, secret) ||
				bytes.Contains(value, []byte(base64.StdEncoding.EncodeToString(secret))) {
				return true
			}
		}
	}
	return false
}

func redactProviderSecrets(value string, secrets ...[]byte) string {
	for _, secret := range secrets {
		secret = bytes.TrimSpace(secret)
		if len(secret) >= 4 {
			value = strings.ReplaceAll(value, string(secret), "[redacted]")
			value = strings.ReplaceAll(value, base64.StdEncoding.EncodeToString(secret), "[redacted]")
		}
	}
	return strings.TrimSpace(value)
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func writeDaytonaProviderHostLauncher() (string, func(), error) {
	launcher, err := os.CreateTemp("", "loom-daytona-provider-host-*.mjs")
	if err != nil {
		return "", nil, fmt.Errorf("create Daytona provider host launcher: %w", err)
	}
	cleanup := func() { _ = os.Remove(launcher.Name()) }
	if _, err := launcher.WriteString(daytonaProviderHostLauncher); err != nil {
		_ = launcher.Close()
		cleanup()
		return "", nil, fmt.Errorf("write Daytona provider host launcher: %w", err)
	}
	if err := launcher.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close Daytona provider host launcher: %w", err)
	}
	return launcher.Name(), cleanup, nil
}

const daytonaProviderHostLauncher = `
import { fork } from 'node:child_process';

const MAX_INPUT = 2 * 1024 * 1024;
let raw = '';
for await (const chunk of process.stdin) {
  raw += chunk.toString('utf8');
  if (Buffer.byteLength(raw, 'utf8') > MAX_INPUT) {
    throw new Error('provider host input exceeds size limit');
  }
}
const input = JSON.parse(raw);
const serverPath = String(process.env.LOOM_TASK_RUNNER_SERVER_PATH || '').trim();
const bundleRoot = String(process.env.LOOM_TASK_RUNNER_BUNDLE_ROOT || '').trim() || process.cwd();
if (!serverPath) throw new Error('provider host server path is required');

let settled = false;
let invoked = false;
const child = fork(serverPath, [], {
  cwd: bundleRoot,
  env: {
    ...process.env,
    FLUE_MODE: 'local',
    FLUE_CLI_TARGET: 'workflow',
    FLUE_CLI_NAME: 'daytona-provider-host',
    FLUE_INTERNAL_CLI_IPC: '1',
  },
  stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
});
child.stdout?.resume();
child.stderr?.resume();

function stop() {
  try { child.disconnect(); } catch {}
  if (!child.killed) {
    try { child.kill(); } catch {}
  }
}
function finish(result) {
  if (settled) return;
  settled = true;
  stop();
  process.stdout.write(JSON.stringify(result || {}) + '\n');
}
function fail() {
  if (settled) return;
  settled = true;
  stop();
  process.stderr.write('Daytona provider host invocation failed\n');
  process.exitCode = 1;
}
child.on('message', (message) => {
  if (!message || typeof message !== 'object') return;
  if (message.type === 'ready' && !invoked) {
    invoked = true;
    child.send({
      version: 1,
      type: 'invoke',
      requestId: input.execution?.taskRunId || 'daytona-provider-host',
      input,
    });
    return;
  }
  if (message.type === 'result') finish(message.result);
  if (message.type === 'error') fail();
});
child.on('error', fail);
child.on('exit', () => {
  if (!settled) fail();
});
process.once('SIGINT', fail);
process.once('SIGTERM', fail);
`
