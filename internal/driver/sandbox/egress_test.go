package sandbox

// SB4 egress-mode tests. Unit tests cover mode resolution per trust level,
// arg/relay construction goldens, and the LOOM_DRIVER_API_URL rewrite. The
// relay mechanism is exercised for REAL twice without a container engine:
// the Go unix→TCP relay alone, and the full chain (node forwarder TCP→unix →
// Go relay unix→TCP → HTTP backend) under host node — the same bytes that
// flow inside a serve-only container.
//
// The rootless-podman integration test is gated like SB2's:
//
//	LOOM_SANDBOX_PODMAN_TEST=1 go test ./internal/driver -run TestContainerEgressPodmanIntegration -v
//
// Note: the relayed-serve reachability leg asserts only under native Linux
// podman — host unix sockets do not cross the podman-machine VM boundary on
// macOS (virtiofs shares the inode, not the listener), so on darwin that leg
// is reported but not required. The --network=none blocking legs assert
// everywhere. SELinux-enforcing hosts (Fedora/RHEL) additionally need the
// deploy-notes policy module (container_t → unconfined_t unix_stream_socket
// connectto) and a container_file_t TMPDIR, or the relay leg fails with
// ECONNRESET and even plain container runs fail EACCES on the temp-file
// mounts — see AGENTS.md "Egress modes (SB4)".

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func TestResolveSandboxEgress(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		trust      workflowcatalog.DriverTrustLevel
		want       SandboxEgressMode
		wantErr    bool
	}{
		{name: "trusted default is all", trust: workflowcatalog.DriverTrustTrusted, want: SandboxEgressAll},
		{name: "untrusted default is serve-only", trust: workflowcatalog.DriverTrustUntrusted, want: SandboxEgressServeOnly},
		{name: "empty trust fails closed to serve-only", trust: "", want: SandboxEgressServeOnly},
		{name: "unknown trust fails closed to serve-only", trust: "verified", want: SandboxEgressServeOnly},
		{name: "explicit all wins over untrusted", configured: "all", trust: workflowcatalog.DriverTrustUntrusted, want: SandboxEgressAll},
		{name: "explicit serve-only wins over trusted", configured: "serve-only", trust: workflowcatalog.DriverTrustTrusted, want: SandboxEgressServeOnly},
		{name: "explicit none", configured: "none", trust: workflowcatalog.DriverTrustTrusted, want: SandboxEgressNone},
		{name: "explicit delegated", configured: "delegated", trust: workflowcatalog.DriverTrustUntrusted, want: SandboxEgressDelegated},
		{name: "case and space insensitive", configured: " Serve-Only ", trust: workflowcatalog.DriverTrustTrusted, want: SandboxEgressServeOnly},
		{name: "unknown mode rejected", configured: "firewall", trust: workflowcatalog.DriverTrustTrusted, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSandboxEgress(tc.configured, tc.trust)
			if tc.wantErr {
				if !errors.Is(err, persistence.ErrInvalid) {
					t.Fatalf("err = %v, want persistence.ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSandboxEgress: %v", err)
			}
			if got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveSandboxLauncherRejectsInvalidEgressConfig(t *testing.T) {
	t.Setenv(SandboxModeEnvVar, "container")
	t.Setenv(SandboxEgressEnvVar, "firewall")
	if _, err := ResolveSandboxLauncher(); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("err = %v, want persistence.ErrInvalid (fail closed at wiring time)", err)
	}
	t.Setenv(SandboxEgressEnvVar, "serve-only")
	resolved, err := ResolveSandboxLauncher()
	if err != nil {
		t.Fatalf("ResolveSandboxLauncher: %v", err)
	}
	launcher, ok := resolved.(*containerLauncher)
	if !ok || launcher.Egress != "serve-only" {
		t.Fatalf("launcher = %#v, want containerLauncher with Egress=serve-only", resolved)
	}
}

func TestServeRelayAddress(t *testing.T) {
	cases := []struct {
		name          string
		rawURL        string
		wantTarget    string
		wantRewritten string
		wantErr       bool
	}{
		{
			name:          "explicit port",
			rawURL:        "http://127.0.0.1:7777",
			wantTarget:    "127.0.0.1:7777",
			wantRewritten: "http://127.0.0.1:8484",
		},
		{
			name:          "default http port and path preserved",
			rawURL:        "http://serve.internal/api/driver",
			wantTarget:    "serve.internal:80",
			wantRewritten: "http://127.0.0.1:8484/api/driver",
		},
		{
			name:          "https default port",
			rawURL:        "https://serve.internal",
			wantTarget:    "serve.internal:443",
			wantRewritten: "https://127.0.0.1:8484",
		},
		{name: "scheme required", rawURL: "serve.internal:7777", wantErr: true},
		{name: "host required", rawURL: "http://", wantErr: true},
		{name: "unsupported scheme rejected", rawURL: "unix:///tmp/serve.sock", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, rewritten, err := serveRelayAddress(tc.rawURL)
			if tc.wantErr {
				if !errors.Is(err, persistence.ErrInvalid) {
					t.Fatalf("err = %v, want persistence.ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("serveRelayAddress: %v", err)
			}
			if target != tc.wantTarget || rewritten != tc.wantRewritten {
				t.Fatalf("serveRelayAddress = (%q, %q), want (%q, %q)", target, rewritten, tc.wantTarget, tc.wantRewritten)
			}
		})
	}
}

func TestPrepareContainerEgressStaticModes(t *testing.T) {
	env := []string{"A=1", "LOOM_DRIVER_API_URL=http://127.0.0.1:7777"}
	cases := []struct {
		name          string
		mode          SandboxEgressMode
		wantMechanism string
		wantNetwork   []string
	}{
		{name: "all is engine default", mode: SandboxEgressAll, wantMechanism: EgressMechanismEngineDefault},
		{name: "delegated is engine default audited", mode: SandboxEgressDelegated, wantMechanism: EgressMechanismDelegated},
		{name: "none disables the network", mode: SandboxEgressNone, wantMechanism: EgressMechanismNetworkNone, wantNetwork: []string{"--network=none"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			egress, err := prepareContainerEgress(tc.mode, env)
			if err != nil {
				t.Fatalf("prepareContainerEgress: %v", err)
			}
			defer egress.close()
			if egress.mode != tc.mode || egress.mechanism != tc.wantMechanism {
				t.Fatalf("egress = %q/%q, want %q/%q", egress.mode, egress.mechanism, tc.mode, tc.wantMechanism)
			}
			if !reflect.DeepEqual(egress.networkArgs, tc.wantNetwork) {
				t.Fatalf("networkArgs = %q, want %q", egress.networkArgs, tc.wantNetwork)
			}
			if !reflect.DeepEqual(egress.env, env) {
				t.Fatalf("env = %q, want untouched %q", egress.env, env)
			}
			if len(egress.mounts) != 0 || egress.forwarderPath != "" {
				t.Fatalf("egress = %+v, want no relay mounts or forwarder outside serve-only", egress)
			}
		})
	}
}

func TestPrepareServeOnlyEgressWithoutAPIURLDegeneratesToNoNetwork(t *testing.T) {
	egress, err := prepareContainerEgress(SandboxEgressServeOnly, []string{"A=1"})
	if err != nil {
		t.Fatalf("prepareContainerEgress: %v", err)
	}
	defer egress.close()
	if egress.mode != SandboxEgressServeOnly || egress.mechanism != EgressMechanismNetworkNone {
		t.Fatalf("egress = %q/%q, want serve-only/network-none (no serve endpoint to relay)", egress.mode, egress.mechanism)
	}
	if !reflect.DeepEqual(egress.networkArgs, []string{"--network=none"}) || egress.forwarderPath != "" {
		t.Fatalf("egress = %+v, want --network=none with no relay", egress)
	}
}

func TestPrepareServeOnlyEgressRelayConstruction(t *testing.T) {
	env := []string{
		"A=1",
		"LOOM_DRIVER_API_URL=http://127.0.0.1:7777",
		"LOOM_RUN_TOKEN=tok-1",
	}
	egress, err := prepareContainerEgress(SandboxEgressServeOnly, env)
	if err != nil {
		t.Fatalf("prepareContainerEgress: %v", err)
	}
	if egress.mode != SandboxEgressServeOnly || egress.mechanism != EgressMechanismServeRelay {
		t.Fatalf("egress = %q/%q, want serve-only/%q", egress.mode, egress.mechanism, EgressMechanismServeRelay)
	}
	if !reflect.DeepEqual(egress.networkArgs, []string{"--network=none"}) {
		t.Fatalf("networkArgs = %q, want exactly --network=none", egress.networkArgs)
	}
	socketPath := envValue(egress.env, sandboxRelaySocketEnvVar)
	relayDir := filepath.Dir(socketPath)
	wantEnv := []string{
		"A=1",
		"LOOM_DRIVER_API_URL=http://127.0.0.1:8484",
		"LOOM_RUN_TOKEN=tok-1",
		sandboxRelaySocketEnvVar + "=" + socketPath,
		sandboxRelayPortEnvVar + "=8484",
	}
	if !reflect.DeepEqual(egress.env, wantEnv) {
		t.Fatalf("env = %q, want %q (URL rewritten in place, relay vars appended)", egress.env, wantEnv)
	}
	wantMounts := []string{
		"--mount", "type=bind,src=" + relayDir + ",dst=" + relayDir,
		"--mount", "type=bind,src=" + egress.forwarderPath + ",dst=" + egress.forwarderPath + ",ro",
	}
	if !reflect.DeepEqual(egress.mounts, wantMounts) {
		t.Fatalf("mounts = %q, want %q (relay dir writable for connect(2), forwarder ro)", egress.mounts, wantMounts)
	}
	if _, err := os.Stat(egress.forwarderPath); err != nil {
		t.Fatalf("stat forwarder: %v", err)
	}
	if conn, err := net.DialTimeout("unix", socketPath, time.Second); err != nil {
		t.Fatalf("relay socket not listening: %v", err)
	} else {
		_ = conn.Close()
	}
	egress.close()
	if _, err := os.Stat(relayDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("relay dir stat err = %v, want removed after close", err)
	}
	if _, err := os.Stat(egress.forwarderPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forwarder stat err = %v, want removed after close", err)
	}
}

func TestContainerLauncherRunArgsServeOnlyGolden(t *testing.T) {
	spec := LaunchSpec{BundleRoot: "/work/bundles/v1"}
	egress := &containerEgress{
		mode:        SandboxEgressServeOnly,
		mechanism:   EgressMechanismServeRelay,
		networkArgs: []string{"--network=none"},
		mounts: []string{
			"--mount", "type=bind,src=/tmp/relay,dst=/tmp/relay",
			"--mount", "type=bind,src=/tmp/egress.mjs,dst=/tmp/egress.mjs,ro",
		},
		forwarderPath: "/tmp/egress.mjs",
	}
	got, err := (&containerLauncher{Binary: "podman"}).runArgs("sbx-1", spec, "/tmp/launcher.mjs", "/tmp/run.env", nil, egress)
	if err != nil {
		t.Fatalf("runArgs: %v", err)
	}
	want := []string{
		"run", "--rm", "-i",
		"--name", "sbx-1",
		"--read-only",
		"--security-opt", "no-new-privileges",
		"--memory", "1g",
		"--cpus", "1.0",
		"--pids-limit", "256",
		"--network=none",
		"--mount", "type=bind,src=/work/bundles/v1,dst=/work/bundles/v1,ro",
		"--mount", "type=bind,src=/tmp/launcher.mjs,dst=/tmp/launcher.mjs,ro",
		"--mount", "type=bind,src=/tmp/relay,dst=/tmp/relay",
		"--mount", "type=bind,src=/tmp/egress.mjs,dst=/tmp/egress.mjs,ro",
		"--workdir", "/work/bundles/v1",
		"--env-file", "/tmp/run.env",
		"docker.io/library/node:22-slim",
		"node", "/tmp/egress.mjs", "/tmp/launcher.mjs",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runArgs = %q\nwant     %q", got, want)
	}
}

// TestServeSocketRelayForwardsToServeTarget exercises the host-side relay for
// real: a raw HTTP request over the unix socket lands on the TCP backend and
// the response rides back. After Close the socket is gone.
func TestServeSocketRelayForwardsToServeTarget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("serve-ok:" + r.URL.Path))
	}))
	defer backend.Close()
	target := strings.TrimPrefix(backend.URL, "http://")
	relay, err := startServeSocketRelay(target)
	if err != nil {
		t.Fatalf("startServeSocketRelay: %v", err)
	}
	defer relay.Close()
	conn, err := net.DialTimeout("unix", relay.socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial relay socket: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /probe HTTP/1.1\r\nHost: serve\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read relayed response: %v", err)
	}
	defer resp.Body.Close()
	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	if got := string(body[:n]); got != "serve-ok:/probe" {
		t.Fatalf("relayed body = %q, want serve-ok:/probe", got)
	}
	relay.Close()
	if _, err := net.DialTimeout("unix", relay.socketPath, time.Second); err == nil {
		t.Fatal("relay socket still accepting after Close")
	}
}

// TestSandboxEgressForwarderHostNode runs the full serve-only data path under
// host node — forwarder (TCP→unix) → Go relay (unix→TCP) → HTTP backend —
// proving the exact mechanism a serve-only container uses, on every dev
// platform (the container boundary itself is the podman-gated test's job).
func TestSandboxEgressForwarderHostNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("serve-ok"))
	}))
	defer backend.Close()
	relay, err := startServeSocketRelay(strings.TrimPrefix(backend.URL, "http://"))
	if err != nil {
		t.Fatalf("startServeSocketRelay: %v", err)
	}
	defer relay.Close()
	forwarderPath, cleanupForwarder, err := writeSandboxEgressForwarder()
	if err != nil {
		t.Fatalf("writeSandboxEgressForwarder: %v", err)
	}
	defer cleanupForwarder()
	// The forwarder reads its port from the env, so the host test can use a
	// free ephemeral port instead of the fixed in-container 8484.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	launcher := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(launcher, []byte(`
const res = await fetch(process.env.LOOM_DRIVER_API_URL + '/probe', { signal: AbortSignal.timeout(5000) });
console.log('BODY=' + (await res.text()));
`), 0o600); err != nil {
		t.Fatalf("write probe launcher: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", forwarderPath, launcher) //nolint:norawexec // real node forwarder process is the contract under test
	cmd.Env = append(os.Environ(),
		sandboxRelaySocketEnvVar+"="+relay.socketPath,
		sandboxRelayPortEnvVar+"="+strconv.Itoa(port),
		"LOOM_DRIVER_API_URL=http://127.0.0.1:"+strconv.Itoa(port),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node forwarder: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "BODY=serve-ok") {
		t.Fatalf("forwarder output = %q, want BODY=serve-ok via relay", out)
	}
}

// integrationEgressServer is the egress-probe bundle: on invoke it fetches
// the (possibly relayed) serve endpoint, an external IP, and the host
// backend port, reporting each as ok:<body> or err:<code>.
const integrationEgressServer = `
process.on('message', async (message) => {
  if (!message || message.type !== 'invoke') return;
  const probe = async (url) => {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(5000) });
      return 'ok:' + (await res.text());
    } catch (e) {
      return 'err:' + (e?.cause?.code || e?.code || e?.name || 'unknown');
    }
  };
  const summary = JSON.stringify({
    serve: await probe(process.env.LOOM_DRIVER_API_URL + '/probe'),
    external: await probe('http://1.1.1.1/'),
    hostPort: await probe('http://host.containers.internal:' + (process.env.SANDBOX_HOST_BACKEND_PORT || '1') + '/'),
  });
  process.send({ type: 'result', result: { status: 'completed', summary } });
});
process.send({ type: 'ready' });
`

type egressProbeReport struct {
	Serve    string `json:"serve"`
	External string `json:"external"`
	HostPort string `json:"hostPort"`
}

// TestContainerEgressPodmanIntegration proves the SB4 modes against real
// rootless podman (gate: LOOM_SANDBOX_PODMAN_TEST=1, see the file header).
// serve-only: the relayed serve endpoint is the only reachable address —
// external IPs and host ports are not (relay leg asserted on native Linux;
// see header for the macOS podman-machine limitation). none: nothing is
// reachable, including the un-rewritten serve URL.
func TestContainerEgressPodmanIntegration(t *testing.T) {
	if os.Getenv("LOOM_SANDBOX_PODMAN_TEST") != "1" {
		t.Skip("set LOOM_SANDBOX_PODMAN_TEST=1 to run the rootless-podman egress integration test")
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed")
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("serve-ok"))
	}))
	defer backend.Close()
	backendPort := strings.TrimPrefix(backend.URL, "http://127.0.0.1:")

	t.Run("serve-only", func(t *testing.T) {
		report, placement := launchEgressProbe(t, &containerLauncher{Binary: "podman"}, backend.URL, backendPort)
		if placement.EgressMode != string(SandboxEgressServeOnly) || placement.EgressMechanism != EgressMechanismServeRelay {
			t.Fatalf("placement egress = %q/%q, want serve-only/%q", placement.EgressMode, placement.EgressMechanism, EgressMechanismServeRelay)
		}
		if report.Serve == "ok:serve-ok" {
			t.Logf("relayed serve endpoint reachable (native Linux podman path)")
		} else if runtime.GOOS == "linux" {
			t.Fatalf("serve probe = %q, want ok:serve-ok via the unix-socket relay", report.Serve)
		} else {
			t.Logf("serve probe = %q on %s: host unix sockets do not cross the podman-machine VM boundary; relay leg asserts on native Linux", report.Serve, runtime.GOOS)
		}
		assertEgressBlocked(t, "external", report.External)
		assertEgressBlocked(t, "hostPort", report.HostPort)
	})

	t.Run("none", func(t *testing.T) {
		report, placement := launchEgressProbe(t, &containerLauncher{Binary: "podman", Egress: "none"}, backend.URL, backendPort)
		if placement.EgressMode != string(SandboxEgressNone) || placement.EgressMechanism != EgressMechanismNetworkNone {
			t.Fatalf("placement egress = %q/%q, want none/%q", placement.EgressMode, placement.EgressMechanism, EgressMechanismNetworkNone)
		}
		assertEgressBlocked(t, "serve", report.Serve)
		assertEgressBlocked(t, "external", report.External)
		assertEgressBlocked(t, "hostPort", report.HostPort)
	})
}

func assertEgressBlocked(t *testing.T, name, probe string) {
	t.Helper()
	if !strings.HasPrefix(probe, "err:") {
		t.Fatalf("%s probe = %q, want unreachable (err:*)", name, probe)
	}
}

func launchEgressProbe(t *testing.T, launcher *containerLauncher, apiURL, backendPort string) (egressProbeReport, execution.TaskRunPlacementRecord) {
	t.Helper()
	bundleRoot := t.TempDir()
	serverPath := filepath.Join(bundleRoot, "dist", "server.mjs")
	if err := os.MkdirAll(filepath.Dir(serverPath), 0o750); err != nil {
		t.Fatalf("mkdir bundle dist: %v", err)
	}
	if err := os.WriteFile(serverPath, []byte(integrationEgressServer), 0o600); err != nil {
		t.Fatalf("write bundle server: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	process, err := launcher.Launch(ctx, LaunchSpec{
		BundleRoot: bundleRoot,
		ServerPath: serverPath,
		// TrustLevel deliberately unset: unknown = untrusted, so the empty
		// egress config must resolve serve-only (the fail-closed default).
		Env: []string{
			"PATH=/usr/local/bin:/usr/bin:/bin",
			"LOOM_FLUE_SERVER_PATH=" + serverPath,
			"LOOM_FLUE_BUNDLE_ROOT=" + bundleRoot,
			"LOOM_FLUE_WORKFLOW_NAME=egress-probe",
			"LOOM_FLUE_INVOKE_PAYLOAD={}",
			"LOOM_DRIVER_RUN_ID=run-egress-it",
			"LOOM_DRIVER_API_URL=" + apiURL,
			"SANDBOX_HOST_BACKEND_PORT=" + backendPort,
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	exit, err := process.Wait()
	if err != nil {
		t.Fatalf("Wait: %v (stderr: %s)", err, exit.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(exit.Stdout), "\n")
	var frame SandboxFrame
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &frame); err != nil {
		t.Fatalf("decode result frame from stdout %q: %v (stderr: %s)", exit.Stdout, err, exit.Stderr)
	}
	if frame.Status != "completed" {
		t.Fatalf("result frame = %+v, want completed (stderr: %s)", frame, exit.Stderr)
	}
	var report egressProbeReport
	if err := json.Unmarshal([]byte(frame.Summary), &report); err != nil {
		t.Fatalf("decode probe summary %q: %v", frame.Summary, err)
	}
	t.Logf("egress probes: serve=%s external=%s hostPort=%s", report.Serve, report.External, report.HostPort)
	return report, process.Placement()
}
