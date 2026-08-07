package sandbox

// Sandbox egress modes (§7 step 9, SB4): bound what sandboxed workflow code
// can reach over the network. The container launcher resolves one declared
// egress mode per run — serve-only is the untrusted default, so an untrusted
// workflow can dial loom serve's driver API and nothing else (§9.5: all
// real egress rides scoped connectors through serve).
//
// Mechanism (locked step-9 decision): --network=none plus a unix-socket relay
// to serve. serve-only runs the container with no network namespace
// connectivity at all and bridges exactly one path back out:
//
//	workflow → http://127.0.0.1:8484 (rewritten LOOM_DRIVER_API_URL)
//	  → in-container forwarder (sandboxEgressForwarder, TCP→unix)
//	  → bind-mounted unix socket (the only egress surface in the mount table)
//	  → host-side serveSocketRelay (unix→TCP, dials ONLY the serve API
//	    address captured at launch — the relay target is fixed host-side, so
//	    container code cannot redirect it)
//
// none is --network=none with no relay: the workflow reaches nothing.
// delegated is for T3/T4 deployments where a NetworkPolicy/firewall outside
// the launcher enforces serve-only; the launcher applies the engine default
// network but records mode "delegated" so the audit trail stays truthful.
// Egress mode + mechanism are stamped into the run's placement record
// (§9.6 audit) — the enforcement mechanism can be swapped (e.g. a
// slirp4netns outbound filter) without changing the mode contract.
//
// Note for macOS development: host unix sockets do not cross the
// podman-machine VM boundary (virtiofs shares the inode, not the listener),
// so the relay leg only functions under native Linux podman — the loom-dev
// deployment shape. The host-node forwarder test covers the mechanism
// everywhere; the podman-gated test asserts the relay leg on Linux only.

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// SandboxEgressEnvVar declares the egress mode for every containerized run.
// Empty resolves per trust level: trusted → all, untrusted → serve-only.
// An explicit value is an operator decision applying to all runs (audited
// via the placement record).
const SandboxEgressEnvVar = "LOOM_DRIVER_SANDBOX_EGRESS"

// SandboxEgressMode is the declared egress bound of one containerized run.
type SandboxEgressMode string

const (
	// SandboxEgressAll leaves the engine default network: unrestricted
	// egress. Trusted default.
	SandboxEgressAll SandboxEgressMode = "all"
	// SandboxEgressServeOnly is --network=none plus the unix-socket relay to
	// serve: the workflow reaches the driver API and nothing else. Untrusted
	// default.
	SandboxEgressServeOnly SandboxEgressMode = "serve-only"
	// SandboxEgressNone is --network=none with no relay: the workflow
	// reaches nothing.
	SandboxEgressNone SandboxEgressMode = "none"
	// SandboxEgressDelegated records that serve-only is enforced OUTSIDE the
	// launcher (T3/T4 NetworkPolicy/firewall); the container itself runs on
	// the engine default network.
	SandboxEgressDelegated SandboxEgressMode = "delegated"
)

// Egress mechanism strings recorded on the placement (§9.6): the mode is the
// contract, the mechanism is how this launcher enforced it.
const (
	EgressMechanismEngineDefault = "engine-default-network"
	EgressMechanismNetworkNone   = "network-none"
	EgressMechanismServeRelay    = "network-none+unix-socket-relay"
	EgressMechanismDelegated     = "external-network-policy"
)

// Relay plumbing exported into the container env (consumed by the
// sandboxEgressForwarder wrapper, never by workflow code directly).
const (
	sandboxRelaySocketEnvVar = "LOOM_SANDBOX_RELAY_SOCKET"
	sandboxRelayPortEnvVar   = "LOOM_SANDBOX_RELAY_PORT"
	// sandboxRelayContainerPort is the loopback TCP port the in-container
	// forwarder listens on. The container netns is private, so a fixed port
	// cannot collide with anything on the host.
	sandboxRelayContainerPort = 8484
)

// resolveSandboxEgress resolves the declared egress mode for one run:
// explicit operator config wins; empty defaults per trust level (trusted →
// all, anything else → serve-only, fail closed).
func resolveSandboxEgress(configured string, trust workflowcatalog.DriverTrustLevel) (SandboxEgressMode, error) {
	mode := SandboxEgressMode(strings.ToLower(strings.TrimSpace(configured)))
	switch mode {
	case "":
		if trust.Trusted() {
			return SandboxEgressAll, nil
		}
		return SandboxEgressServeOnly, nil
	case SandboxEgressAll, SandboxEgressServeOnly, SandboxEgressNone, SandboxEgressDelegated:
		return mode, nil
	default:
		return "", fmt.Errorf("%s=%q: want %q, %q, %q or %q: %w", SandboxEgressEnvVar, configured,
			SandboxEgressAll, SandboxEgressServeOnly, SandboxEgressNone, SandboxEgressDelegated, domain.ErrInvalid)
	}
}

// containerEgress is one run's prepared egress: the (possibly rewritten)
// runtime env, the engine network args, the extra relay mounts, and the
// in-container forwarder wrapper. close releases the host-side relay and
// temp files; the container launcher calls it after the runtime exits.
type containerEgress struct {
	mode          SandboxEgressMode
	mechanism     string
	env           []string
	networkArgs   []string
	mounts        []string
	forwarderPath string
	cleanup       func()
}

func (e *containerEgress) close() {
	if e != nil && e.cleanup != nil {
		e.cleanup()
	}
}

// prepareContainerEgress builds the egress setup for one resolved mode over
// the runner-assembled runtime env (which the launcher otherwise passes
// verbatim — the LOOM_DRIVER_API_URL rewrite and the relay plumbing vars are
// SB4's only, documented, env deviations).
func prepareContainerEgress(mode SandboxEgressMode, env []string) (*containerEgress, error) {
	switch mode {
	case SandboxEgressAll:
		return &containerEgress{mode: mode, mechanism: EgressMechanismEngineDefault, env: env}, nil
	case SandboxEgressDelegated:
		return &containerEgress{mode: mode, mechanism: EgressMechanismDelegated, env: env}, nil
	case SandboxEgressNone:
		return &containerEgress{mode: mode, mechanism: EgressMechanismNetworkNone, env: env,
			networkArgs: []string{"--network=none"}}, nil
	case SandboxEgressServeOnly:
		return prepareServeOnlyEgress(env)
	default:
		return nil, fmt.Errorf("container sandbox: unknown egress mode %q: %w", mode, domain.ErrInvalid)
	}
}

// prepareServeOnlyEgress sets up --network=none plus the unix-socket relay.
// A run without LOOM_DRIVER_API_URL has no serve endpoint to relay, so
// serve-only degenerates to no network at all (mechanism network-none) —
// still fail closed, never engine default.
func prepareServeOnlyEgress(env []string) (*containerEgress, error) {
	apiURL := envValue(env, "LOOM_DRIVER_API_URL")
	if apiURL == "" {
		return &containerEgress{mode: SandboxEgressServeOnly, mechanism: EgressMechanismNetworkNone,
			env: env, networkArgs: []string{"--network=none"}}, nil
	}
	target, rewritten, err := serveRelayAddress(apiURL)
	if err != nil {
		return nil, err
	}
	relay, err := startServeSocketRelay(target)
	if err != nil {
		return nil, err
	}
	forwarderPath, cleanupForwarder, err := writeSandboxEgressForwarder()
	if err != nil {
		relay.Close()
		return nil, err
	}
	cleanup := func() {
		relay.Close()
		cleanupForwarder()
	}
	mounts, err := serveRelayMounts(relay.dir, forwarderPath)
	if err != nil {
		cleanup()
		return nil, err
	}
	egressEnv := replaceEnvValue(env, "LOOM_DRIVER_API_URL", rewritten)
	egressEnv = append(egressEnv,
		sandboxRelaySocketEnvVar+"="+relay.socketPath,
		sandboxRelayPortEnvVar+"="+strconv.Itoa(sandboxRelayContainerPort),
	)
	return &containerEgress{
		mode:          SandboxEgressServeOnly,
		mechanism:     EgressMechanismServeRelay,
		env:           egressEnv,
		networkArgs:   []string{"--network=none"},
		mounts:        mounts,
		forwarderPath: forwarderPath,
		cleanup:       cleanup,
	}, nil
}

// serveRelayMounts builds the two extra identity bind mounts: the relay
// directory (NOT ro — connect(2) on a unix socket needs write access to the
// inode, and ro mounts fail it with EROFS; the directory holds nothing but
// the socket) and the read-only forwarder script. The directory is mounted
// rather than the socket file because socket-file bind sources fail statfs
// on shared-FS engines.
func serveRelayMounts(relayDir, forwarderPath string) ([]string, error) {
	relayMount, err := bindMountArg(relayDir, false)
	if err != nil {
		return nil, err
	}
	forwarderMount, err := bindMountArg(forwarderPath, true)
	if err != nil {
		return nil, err
	}
	return []string{"--mount", relayMount, "--mount", forwarderMount}, nil
}

// serveRelayAddress parses LOOM_DRIVER_API_URL into the host-side dial
// target and the rewritten in-container URL pointing at the forwarder.
func serveRelayAddress(rawURL string) (dialTarget, rewritten string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", "", fmt.Errorf("container sandbox: parse LOOM_DRIVER_API_URL %q: %v: %w", rawURL, err, domain.ErrInvalid)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("container sandbox: LOOM_DRIVER_API_URL %q: scheme must be http or https: %w", rawURL, domain.ErrInvalid)
	}
	if parsed.Hostname() == "" {
		return "", "", fmt.Errorf("container sandbox: LOOM_DRIVER_API_URL %q has no host: %w", rawURL, domain.ErrInvalid)
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
		if parsed.Scheme == "https" {
			port = "443"
		}
	}
	dialTarget = net.JoinHostPort(parsed.Hostname(), port)
	parsed.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(sandboxRelayContainerPort))
	return dialTarget, parsed.String(), nil
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok && name == key {
			return value
		}
	}
	return ""
}

func replaceEnvValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if name, _, ok := strings.Cut(entry, "="); ok && name == key {
			out = append(out, key+"="+value)
			continue
		}
		out = append(out, entry)
	}
	return out
}

// serveSocketRelay is the host-side half of the serve-only mechanism: a unix
// socket listener that forwards every accepted connection to exactly one TCP
// target — the serve driver API address captured at launch. The target is
// fixed host-side; nothing the container sends can redirect it.
type serveSocketRelay struct {
	target     string
	dir        string
	socketPath string
	listener   net.Listener

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

func startServeSocketRelay(target string) (*serveSocketRelay, error) {
	dir, err := os.MkdirTemp("", "loom-sandbox-relay-*")
	if err != nil {
		return nil, fmt.Errorf("create sandbox relay dir: %w", err)
	}
	socketPath := filepath.Join(dir, "serve.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("listen sandbox relay socket: %w", err)
	}
	relay := &serveSocketRelay{
		target:     target,
		dir:        dir,
		socketPath: socketPath,
		listener:   listener,
		conns:      map[net.Conn]struct{}{},
	}
	go relay.acceptLoop()
	return relay, nil
}

func (r *serveSocketRelay) acceptLoop() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		go r.forward(conn)
	}
}

func (r *serveSocketRelay) forward(downstream net.Conn) {
	upstream, err := net.DialTimeout("tcp", r.target, 10*time.Second)
	if err != nil {
		_ = downstream.Close()
		return
	}
	if !r.track(downstream, upstream) {
		_ = downstream.Close()
		_ = upstream.Close()
		return
	}
	done := make(chan struct{}, 2)
	go relayCopy(upstream, downstream, done)
	go relayCopy(downstream, upstream, done)
	<-done
	<-done
	r.untrack(downstream, upstream)
	_ = downstream.Close()
	_ = upstream.Close()
}

// relayCopy streams one direction and half-closes the destination at EOF so
// the peer observes the end of stream while the reverse direction drains.
func relayCopy(dst, src net.Conn, done chan<- struct{}) {
	_, _ = io.Copy(dst, src)
	if half, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	} else {
		_ = dst.Close()
	}
	done <- struct{}{}
}

func (r *serveSocketRelay) track(conns ...net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	for _, conn := range conns {
		r.conns[conn] = struct{}{}
	}
	return true
}

func (r *serveSocketRelay) untrack(conns ...net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, conn := range conns {
		delete(r.conns, conn)
	}
}

// Close stops the listener, closes in-flight connections, and removes the
// socket directory. Idempotent; called after the container runtime exits.
func (r *serveSocketRelay) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	open := make([]net.Conn, 0, len(r.conns))
	for conn := range r.conns {
		open = append(open, conn)
	}
	r.mu.Unlock()
	_ = r.listener.Close()
	for _, conn := range open {
		_ = conn.Close()
	}
	_ = os.RemoveAll(r.dir)
}

func writeSandboxEgressForwarder() (string, func(), error) {
	forwarder, err := os.CreateTemp("", "loom-sandbox-egress-*.mjs")
	if err != nil {
		return "", nil, fmt.Errorf("create sandbox egress forwarder: %w", err)
	}
	cleanup := func() { _ = os.Remove(forwarder.Name()) }
	if _, err := forwarder.WriteString(sandboxEgressForwarder); err != nil {
		_ = forwarder.Close()
		cleanup()
		return "", nil, fmt.Errorf("write sandbox egress forwarder: %w", err)
	}
	if err := forwarder.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close sandbox egress forwarder: %w", err)
	}
	return forwarder.Name(), cleanup, nil
}

// sandboxEgressForwarder wraps the runtime launcher inside the container in
// serve-only mode: it listens on loopback :8484 BEFORE importing the
// launcher (deterministic ordering — the workflow can never dial a port that
// is not yet open) and pipes each connection into the bind-mounted unix
// socket. Sockets are unref'd so lingering keep-alive connections never hold
// the runtime process open after the result frame.
const sandboxEgressForwarder = `
import net from 'node:net';
import { pathToFileURL } from 'node:url';

const socketPath = process.env.LOOM_SANDBOX_RELAY_SOCKET || '';
const port = Number(process.env.LOOM_SANDBOX_RELAY_PORT || '0');

if (socketPath && port > 0) {
  const server = net.createServer((conn) => {
    const upstream = net.connect(socketPath);
    conn.unref();
    upstream.unref();
    conn.pipe(upstream);
    upstream.pipe(conn);
    conn.on('error', () => upstream.destroy());
    upstream.on('error', () => conn.destroy());
    conn.on('close', () => upstream.destroy());
    upstream.on('close', () => conn.destroy());
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });
  server.unref();
}

const launcherPath = process.argv[2];
if (launcherPath) {
  await import(pathToFileURL(launcherPath).href);
}
`
