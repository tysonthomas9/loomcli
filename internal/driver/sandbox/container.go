package sandbox

// Container sandbox launcher (§7 step 9, SB2): runs workflow bundles in a
// rootless container (podman first, docker fallback) behind the SB1
// SandboxLauncher seam, so flue-local user code gets L2 isolation — host FS
// unreachable, runtime env exactly LaunchSpec.Env, bundle bind-mounted
// read-only, mandatory resource caps (--memory/--cpus/--pids-limit), and an
// optional hardened OCI runtime (--runtime runsc when gVisor is installed).
//
// IPC is the SB1 stdio JSON-lines contract unchanged: the embedded runtime
// launcher (flueLocalLauncher) is bind-mounted into the container and run
// under the image's node; container stdout carries the frame stream (last
// non-empty line = terminal result frame), stderr is the log stream verbatim.
//
// Env hygiene: values never ride argv (ps-visible). Newline-free entries go
// through an ephemeral 0600 --env-file (verified verbatim on podman 5.8 and
// docker: split at the first '=', no quote/comment/$ processing; the gated
// integration test pins this by round-tripping JSON payloads). Entries whose
// values contain newlines cannot ride the line-based env-file, so they pass
// as name-only --env KEY flags with the value carried on the engine client's
// process env instead — still never argv. The env-file is deleted after the
// runtime exits (deleting right after Start would race the client parsing
// its flags); 0600 in the executor's private tmpdir bounds the exposure to
// the launcher temp .mjs file's.
//
// Bundle + launcher mounts are identity mounts (dst == host src): the SB1
// contract passes the env verbatim, so LOOM_FLUE_SERVER_PATH/_BUNDLE_ROOT
// are host paths and must resolve unchanged inside the container. Nothing
// else of the host FS is mounted. The image's node must be reachable via the
// PATH the runner allowlisted from the host (official node images install
// /usr/local/bin/node, on standard PATHs).
//
// Selection is serve-side wiring (ResolveSandboxLauncher): the default stays
// the local process launcher until SB3's trust policy makes the container
// mandatory for untrusted code. Egress is bounded per run by the SB4 modes
// (sandbox_egress.go): untrusted runs default to serve-only — --network=none
// plus the unix-socket relay to serve, with LOOM_DRIVER_API_URL rewritten to
// the relayed loopback address (that rewrite plus the relay plumbing vars are
// the launcher's only deliberate env deviations).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// SandboxProviderContainer is the placement provider recorded by the
// container launcher (§9.6 audit).
const SandboxProviderContainer = "container"

// Sandbox launcher selection + configuration env knobs (resolved in serve's
// driver-executor wiring).
const (
	// SandboxModeEnvVar selects the launcher: "process" (default) or
	// "container".
	SandboxModeEnvVar = "LOOM_DRIVER_SANDBOX"
	// SandboxBinaryEnvVar overrides the container engine binary (default:
	// podman if installed, else docker).
	SandboxBinaryEnvVar = "LOOM_DRIVER_SANDBOX_BINARY"
	// SandboxImageEnvVar overrides the runtime image.
	SandboxImageEnvVar = "LOOM_DRIVER_SANDBOX_IMAGE"
	// SandboxRuntimeEnvVar selects an alternate OCI runtime ("" engine
	// default, "runsc" for gVisor).
	SandboxRuntimeEnvVar = "LOOM_DRIVER_SANDBOX_RUNTIME"
	// SandboxMemoryEnvVar / SandboxCPUsEnvVar / SandboxPidsLimitEnvVar tune
	// the mandatory resource caps.
	SandboxMemoryEnvVar    = "LOOM_DRIVER_SANDBOX_MEMORY"
	SandboxCPUsEnvVar      = "LOOM_DRIVER_SANDBOX_CPUS"
	SandboxPidsLimitEnvVar = "LOOM_DRIVER_SANDBOX_PIDS_LIMIT"
	// SandboxEgressEnvVar (sandbox_egress.go) declares the egress mode:
	// all | serve-only | none | delegated; empty resolves per trust level.
)

// SandboxModeProcess / SandboxModeContainer are the SandboxModeEnvVar values.
const (
	SandboxModeProcess   = "process"
	SandboxModeContainer = "container"
)

// Container launcher defaults. The resource caps are mandatory — a workflow
// runtime must never be able to overstrain its host — so the zero config is
// capped, not uncapped.
const (
	DefaultSandboxImage     = "docker.io/library/node:22-slim"
	DefaultSandboxMemory    = "1g"
	DefaultSandboxCPUs      = "1.0"
	DefaultSandboxPidsLimit = 256
)

// ResolveSandboxLauncher resolves the SandboxModeEnvVar selection for serve's
// executor wiring. A nil launcher means the default local process launcher
// (today's flue-local behavior); container mode returns the rootless
// container launcher configured from its env knobs.
func ResolveSandboxLauncher() (SandboxLauncher, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(SandboxModeEnvVar)))
	switch mode {
	case "", SandboxModeProcess:
		return nil, nil
	case SandboxModeContainer:
		launcher := containerLauncherFromEnv()
		// Fail closed at wiring time on an invalid egress declaration rather
		// than per-run at Launch (trust level only picks the empty-config
		// default, not validity).
		if _, err := resolveSandboxEgress(launcher.Egress, domain.DriverTrustTrusted); err != nil {
			return nil, err
		}
		return launcher, nil
	default:
		return nil, fmt.Errorf("%s=%q: want %q or %q: %w",
			SandboxModeEnvVar, mode, SandboxModeProcess, SandboxModeContainer, domain.ErrInvalid)
	}
}

func containerLauncherFromEnv() *containerLauncher {
	launcher := &containerLauncher{
		Binary:  strings.TrimSpace(os.Getenv(SandboxBinaryEnvVar)),
		Image:   strings.TrimSpace(os.Getenv(SandboxImageEnvVar)),
		Runtime: strings.TrimSpace(os.Getenv(SandboxRuntimeEnvVar)),
		Memory:  strings.TrimSpace(os.Getenv(SandboxMemoryEnvVar)),
		CPUs:    strings.TrimSpace(os.Getenv(SandboxCPUsEnvVar)),
		Egress:  strings.TrimSpace(os.Getenv(SandboxEgressEnvVar)),
	}
	if raw := strings.TrimSpace(os.Getenv(SandboxPidsLimitEnvVar)); raw != "" {
		if pids, err := strconv.Atoi(raw); err == nil && pids > 0 {
			launcher.PidsLimit = pids
		}
	}
	return launcher
}

// containerLauncher is the SB2 SandboxLauncher: one rootless container per
// driver run. Zero values mean defaults; see the package-level doc above for
// the isolation contract.
type containerLauncher struct {
	Binary    string // engine binary; "" = podman, docker fallback
	Image     string // runtime image; "" = DefaultSandboxImage
	Runtime   string // OCI runtime; "" = engine default, "runsc" = gVisor
	Memory    string // --memory cap; "" = DefaultSandboxMemory
	CPUs      string // --cpus cap; "" = DefaultSandboxCPUs
	PidsLimit int    // --pids-limit cap; 0 = DefaultSandboxPidsLimit
	Egress    string // egress mode (SB4); "" = per trust level (trusted all, else serve-only)
}

// Isolates marks the container launcher as an isolating sandbox (SB3 trust
// placement policy): untrusted bundles may launch through it.
func (l *containerLauncher) Isolates() bool { return true }

func (l *containerLauncher) binary() string {
	if l.Binary != "" {
		return l.Binary
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman"
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker"
	}
	return "podman"
}

func (l *containerLauncher) image() string {
	if l.Image != "" {
		return l.Image
	}
	return DefaultSandboxImage
}

func (l *containerLauncher) memory() string {
	if l.Memory != "" {
		return l.Memory
	}
	return DefaultSandboxMemory
}

func (l *containerLauncher) cpus() string {
	if l.CPUs != "" {
		return l.CPUs
	}
	return DefaultSandboxCPUs
}

func (l *containerLauncher) pidsLimit() int {
	if l.PidsLimit > 0 {
		return l.PidsLimit
	}
	return DefaultSandboxPidsLimit
}

func (l *containerLauncher) Launch(ctx context.Context, spec LaunchSpec) (SandboxProcess, error) {
	if strings.TrimSpace(spec.BundleRoot) == "" {
		return nil, fmt.Errorf("container sandbox: bundle root required: %w", domain.ErrInvalid)
	}
	egress, err := l.prepareEgress(spec)
	if err != nil {
		return nil, err
	}
	files, err := prepareContainerRunFiles(egress.env)
	if err != nil {
		egress.close()
		return nil, err
	}
	sandbox := &containerSandbox{
		binary: l.binary(),
		name:   containerSandboxName(),
		cleanup: func() {
			files.cleanup()
			egress.close()
		},
	}
	args, err := l.runArgs(sandbox.name, spec, files.launcherPath, files.envFile, containerEnvKeys(files.passEnv), egress)
	if err != nil {
		sandbox.cleanup()
		return nil, err
	}
	cmd := containerRunCommand(ctx, sandbox.binary, args, mergeEnvOverride(os.Environ(), files.passEnv))
	cmd.Stdout = &sandbox.stdout
	cmd.Stderr = &sandbox.stderr
	sandbox.cmd = cmd
	if startErr := cmd.Start(); startErr != nil {
		// Start-failure parity with the process launcher: a missing engine
		// binary surfaces from Wait as a failed driver_runtime result, not a
		// launch error.
		sandbox.startErr = startErr
		return sandbox, nil
	}
	sandbox.placement = domain.TaskRunPlacement{
		Provider:        SandboxProviderContainer,
		ProcessRef:      strconv.Itoa(cmd.Process.Pid),
		SandboxID:       sandbox.name,
		ImageOrSnapshot: l.image(),
		CWD:             containerWorkDir(spec),
		EgressMode:      string(egress.mode),
		EgressMechanism: egress.mechanism,
		StartedAt:       time.Now().UTC(),
	}
	return sandbox, nil
}

// prepareEgress resolves this run's egress mode (SB4): the launcher's
// configured mode wins; empty defaults per the run's trust level.
func (l *containerLauncher) prepareEgress(spec LaunchSpec) (*containerEgress, error) {
	mode, err := resolveSandboxEgress(l.Egress, spec.TrustLevel)
	if err != nil {
		return nil, err
	}
	return prepareContainerEgress(mode, spec.Env)
}

// containerRunFiles bundles the per-run temp files: the embedded runtime
// launcher and the 0600 env-file, with their joint cleanup.
type containerRunFiles struct {
	launcherPath string
	envFile      string
	passEnv      []string
	cleanup      func()
}

func prepareContainerRunFiles(env []string) (*containerRunFiles, error) {
	launcherPath, cleanupLauncher, err := writeFlueRuntimeLauncher()
	if err != nil {
		return nil, err
	}
	fileEnv, passEnv, err := splitContainerEnv(env)
	if err != nil {
		cleanupLauncher()
		return nil, err
	}
	envFile, err := writeContainerEnvFile(fileEnv)
	if err != nil {
		cleanupLauncher()
		return nil, err
	}
	return &containerRunFiles{
		launcherPath: launcherPath,
		envFile:      envFile,
		passEnv:      passEnv,
		cleanup: func() {
			cleanupLauncher()
			_ = os.Remove(envFile)
		},
	}, nil
}

// runArgs builds the engine argv. No env value ever appears here: the
// env-file path and name-only --env keys are the only env surface on argv.
// A nil egress means engine-default network with no relay (mode all).
func (l *containerLauncher) runArgs(name string, spec LaunchSpec, launcherPath, envFile string, passKeys []string, egress *containerEgress) ([]string, error) {
	mounts, err := containerMounts(spec.BundleRoot, launcherPath)
	if err != nil {
		return nil, err
	}
	args := []string{
		"run", "--rm", "-i",
		"--name", name,
		"--read-only",
		"--security-opt", "no-new-privileges",
		"--memory", l.memory(),
		"--cpus", l.cpus(),
		"--pids-limit", strconv.Itoa(l.pidsLimit()),
	}
	command := []string{l.image(), "node"}
	if egress != nil {
		args = append(args, egress.networkArgs...)
		mounts = append(mounts, egress.mounts...)
		if egress.forwarderPath != "" {
			// serve-only: the forwarder wraps the runtime launcher so the
			// loopback relay port is listening before workflow code runs.
			command = append(command, egress.forwarderPath)
		}
	}
	args = append(args, mounts...)
	args = append(args, "--workdir", containerWorkDir(spec), "--env-file", envFile)
	if runtime := strings.TrimSpace(l.Runtime); runtime != "" {
		args = append(args, "--runtime", runtime)
	}
	for _, key := range passKeys {
		args = append(args, "--env", key)
	}
	return append(args, append(command, launcherPath)...), nil
}

// containerMounts builds the two read-only identity bind mounts: the
// digest-verified bundle tree and the embedded runtime launcher temp file.
// dst == src keeps the verbatim LaunchSpec.Env host paths resolvable inside
// the container.
func containerMounts(bundleRoot, launcherPath string) ([]string, error) {
	mounts := make([]string, 0, 4)
	for _, src := range []string{bundleRoot, launcherPath} {
		mount, err := bindMountArg(src, true)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, "--mount", mount)
	}
	return mounts, nil
}

// bindMountArg renders one identity bind mount (dst == src). CSV --mount
// syntax cannot carry commas in paths.
func bindMountArg(src string, readOnly bool) (string, error) {
	if strings.ContainsAny(src, ",\n") {
		return "", fmt.Errorf("container sandbox: mount source %q must not contain commas or newlines: %w", src, domain.ErrInvalid)
	}
	mount := "type=bind,src=" + src + ",dst=" + src
	if readOnly {
		mount += ",ro"
	}
	return mount, nil
}

func containerWorkDir(spec LaunchSpec) string {
	if spec.WorkDir != "" {
		return spec.WorkDir
	}
	return spec.BundleRoot
}

// containerRunCommand mirrors flueRuntimeCommand's cancellation contract:
// ctx cancel sends SIGINT to the engine client (proxied to the containerized
// runtime launcher, which emits the cancelled result frame), with a 5s
// WaitDelay before the client is hard-killed.
func containerRunCommand(ctx context.Context, binary string, args, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // binary is operator-configured (podman/docker); args are built by runArgs.
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

// splitContainerEnv validates LaunchSpec.Env and partitions it: newline-free
// entries ride the env-file; entries whose values contain newlines cannot
// (line-based format) and pass as name-only --env keys with the value on the
// engine client's process env.
func splitContainerEnv(env []string) (fileEnv, passEnv []string, err error) {
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, nil, fmt.Errorf("container sandbox: env entry %q is not KEY=VALUE: %w", entry, domain.ErrInvalid)
		}
		if strings.ContainsAny(key, " \t\r\n#") {
			return nil, nil, fmt.Errorf("container sandbox: env name %q contains unsupported characters: %w", key, domain.ErrInvalid)
		}
		if strings.ContainsAny(value, "\r\n") {
			passEnv = append(passEnv, entry)
			continue
		}
		fileEnv = append(fileEnv, entry)
	}
	return fileEnv, passEnv, nil
}

func containerEnvKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		keys = append(keys, key)
	}
	return keys
}

// writeContainerEnvFile writes the runtime env (and nothing else — never the
// parent process env) to an ephemeral 0600 env-file.
func writeContainerEnvFile(env []string) (string, error) {
	file, err := os.CreateTemp("", "loom-sandbox-env-*.env")
	if err != nil {
		return "", fmt.Errorf("create container sandbox env file: %w", err)
	}
	var content strings.Builder
	for _, entry := range env {
		content.WriteString(entry)
		content.WriteString("\n")
	}
	if _, err := file.WriteString(content.String()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("write container sandbox env file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("close container sandbox env file: %w", err)
	}
	return file.Name(), nil
}

// mergeEnvOverride overlays overrides onto base with later-wins-once
// semantics: an override replaces every base entry sharing its name, so the
// engine client's env lookup (first match) sees exactly the override value.
func mergeEnvOverride(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	overridden := make(map[string]struct{}, len(overrides))
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		overridden[key] = struct{}{}
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := overridden[key]; ok {
			continue
		}
		out = append(out, entry)
	}
	return append(out, overrides...)
}

func containerSandboxName() string {
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Sprintf("loom-sandbox-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("loom-sandbox-%d-%s", os.Getpid(), hex.EncodeToString(suffix))
}

// containerSandbox is one launched container run. The engine client process
// is the local handle; the named container is force-removed on Kill and on
// abnormal client exit so --rm cleanup gaps never leak a runtime.
type containerSandbox struct {
	binary    string
	name      string
	cmd       *exec.Cmd
	stdout    strings.Builder
	stderr    strings.Builder
	cleanup   func()
	startErr  error
	placement domain.TaskRunPlacement
	waitOnce  sync.Once
	waitErr   error
}

func (c *containerSandbox) Wait() (SandboxExit, error) {
	c.waitOnce.Do(func() {
		defer c.cleanup()
		if c.startErr != nil {
			c.waitErr = c.startErr
			return
		}
		c.waitErr = c.cmd.Wait()
		if c.waitErr != nil {
			// Abnormal client exit (cancel hard-kill, engine crash): the
			// container may have outlived its attached client; reap it.
			c.removeContainer()
		}
	})
	return SandboxExit{Stdout: c.stdout.String(), Stderr: c.stderr.String()}, c.waitErr
}

func (c *containerSandbox) Kill() error {
	if c.startErr != nil || c.cmd.Process == nil {
		return nil
	}
	c.removeContainer()
	if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (c *containerSandbox) Placement() domain.TaskRunPlacement {
	return c.placement
}

// removeContainer best-effort force-removes the named container.
func (c *containerSandbox) removeContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, c.binary, removeContainerArgs(c.binary, c.name)...).Run() //nolint:gosec // binary is operator-configured (podman/docker).
}

func removeContainerArgs(binary, name string) []string {
	args := []string{"rm", "--force"}
	if filepath.Base(binary) != "docker" {
		args = append(args, "--ignore")
	}
	return append(args, name)
}
