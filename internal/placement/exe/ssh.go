package exe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// sshUser and sshPort are provider constants. The control-plane API
	// exposes neither -- an earlier draft invented ssh_user/ssh_port fields
	// that do not exist, and the compile error was the only thing that caught
	// it. Do not "read" these from a VM record.
	sshUser = "exedev"
	sshPort = 22

	// vmHostSuffix is how a VM name becomes an SSH host.
	vmHostSuffix = ".exe.xyz"

	// tmuxSocket namespaces loom's tmux server so it cannot collide with a
	// user's own tmux inside the VM.
	tmuxSocket = "loom"
)

// hostKeyStore pins VM host keys on first use and verifies them thereafter.
//
// exe.dev gives every VM a fresh host key and exposes it nowhere in the API,
// so there is nothing to pin in advance. Trust-on-first-use with persistence
// is therefore the strongest option available, and it is a real one: it
// detects any substitution AFTER the first connection.
//
// The residual risk is explicit and must not be papered over: the FIRST
// connection to a VM is unauthenticated, and the lead's occupant token is
// delivered over it. Accepting any key on every connection (what a spike does)
// would leave that window open forever instead of only once.
type hostKeyStore struct {
	mu   sync.Mutex
	path string
	keys map[string]string // host -> authorized-key line
}

func newHostKeyStore(path string) *hostKeyStore {
	return &hostKeyStore{path: path, keys: map[string]string{}}
}

func (s *hostKeyStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read exe host key store: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		host, key, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		s.keys[host] = strings.TrimSpace(key)
	}
	return nil
}

func (s *hostKeyStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create exe host key dir: %w", err)
	}
	hosts := make([]string, 0, len(s.keys))
	for host := range s.keys {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	var b strings.Builder
	b.WriteString("# loom exe.dev VM host keys (trust-on-first-use). Do not edit by hand.\n")
	for _, host := range hosts {
		fmt.Fprintf(&b, "%s %s\n", host, s.keys[host])
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write exe host key store: %w", err)
	}
	return os.Rename(tmp, s.path)
}

// callback returns an ssh.HostKeyCallback that pins on first sight and
// verifies afterwards.
func (s *hostKeyStore) callback() ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		host := strings.TrimSuffix(hostname, ":22")
		presented := string(ssh.MarshalAuthorizedKey(key))
		presented = strings.TrimSpace(presented)

		s.mu.Lock()
		defer s.mu.Unlock()
		known, seen := s.keys[host]
		if !seen {
			s.keys[host] = presented
			return s.persistLocked()
		}
		if known != presented {
			return fmt.Errorf(
				"exe host key for %q changed since first use; refusing to connect "+
					"(a substituted host would receive the lead's occupant token). "+
					"If the VM was legitimately rebuilt, remove its line from %s",
				host, s.path)
		}
		return nil
	}
}

// forget drops a host's pinned key. Called after a confirmed delete so a later
// VM reusing the name is pinned afresh rather than rejected as a key change.
func (s *hostKeyStore) forget(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[host]; !ok {
		return
	}
	delete(s.keys, host)
	_ = s.persistLocked()
}

// sshDialer opens authenticated sessions to a VM.
type sshDialer struct {
	signer   ssh.Signer
	hostKeys *hostKeyStore
	timeout  time.Duration
}

func newSSHDialer(keyPEM []byte, hostKeys *hostKeyStore, timeout time.Duration) (*sshDialer, error) {
	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse exe ssh key (encrypted keys are not supported): %w", err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &sshDialer{signer: signer, hostKeys: hostKeys, timeout: timeout}, nil
}

func vmHost(name string) string { return name + vmHostSuffix }

func (d *sshDialer) dial(ctx context.Context, vmName string) (*ssh.Client, error) {
	host := vmHost(vmName)
	addr := net.JoinHostPort(host, fmt.Sprint(sshPort))
	dialer := net.Dialer{Timeout: d.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial exe vm %q: %w", vmName, err)
	}
	cfg := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(d.signer)},
		HostKeyCallback: d.hostKeys.callback(),
		Timeout:         d.timeout,
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, host, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake with exe vm %q: %w", vmName, err)
	}
	return ssh.NewClient(sc, chans, reqs), nil
}

// run executes one command and returns its combined output.
//
// The output is returned even on error: tmux reports "no server running" and
// friends via a nonzero exit, and the caller must read the text to tell an
// empty result from a real failure.
func run(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("open ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()
	out, err := session.CombinedOutput(cmd)
	return string(out), err
}

// runStdin executes one command with data fed on STDIN.
//
// Secrets go this way, never in the command string. sshd runs a remote command
// as `sh -c '<the whole string>'`, so anything interpolated into it is visible
// in the VM's process list to every process in the VM -- including the lead,
// which is precisely the principal that must not hold the git or provider
// credential.
func runStdin(client *ssh.Client, cmd string, stdin []byte) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("open ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()
	session.Stdin = bytes.NewReader(stdin)
	out, err := session.CombinedOutput(cmd)
	return string(out), err
}

// shellQuote is single-quote escaping. Every value interpolated into an
// in-VM command goes through it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// tmuxCreateSession builds the durable-PTY create command.
//
// tmux IS the PTY layer here: exe.dev has no PTY API, and a plain SSH exec
// dies with its connection. A detached tmux session survives serve
// restarting, reconnecting, or losing the network -- which is the whole
// reason it was chosen.
func tmuxCreateSession(session, workdir string, env map[string]string, command []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tmux -L %s new-session -d", tmuxSocket)
	fmt.Fprintf(&b, " -s %s", shellQuote(session))
	if workdir != "" {
		fmt.Fprintf(&b, " -c %s", shellQuote(workdir))
	}
	for _, k := range sortedKeys(env) {
		fmt.Fprintf(&b, " -e %s", shellQuote(k+"="+env[k]))
	}
	if len(command) > 0 {
		parts := make([]string, 0, len(command))
		for _, arg := range command {
			parts = append(parts, shellQuote(arg))
		}
		b.WriteString(" " + shellQuote(strings.Join(parts, " ")))
	}
	return b.String()
}

// tmuxNoServer reports whether output means "no tmux server", which is an
// EMPTY session list, not a failure.
//
// LIVE-VERIFIED: tmux emits two distinct no-server signatures and both mean
// empty. Matching only one makes every first lead boot look like a failure.
//
//	socket never created: "error connecting to /tmp/tmux-1000/loom (No such file or directory)"
//	after a VM restart:   "no server running on /tmp/tmux-1000/loom"
func tmuxNoServer(out string) bool {
	return strings.Contains(out, "no server running") ||
		strings.Contains(out, "No such file or directory") ||
		strings.Contains(out, "error connecting to")
}
