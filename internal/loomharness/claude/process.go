// Package claude is the Claude Code harness adapter. There is no Go Agent
// SDK, so Loom drives the user's installed, unmodified `claude` over the same
// stream-json protocol the SDKs use: one long-running process per agent
// (design v2 §8.1.6), on the user's own login.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/proctree"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// stopGrace is how long Close waits after SIGTERM before SIGKILL; trackEvery
// is how often the process's descendants are recorded.
var (
	stopGrace  = 10 * time.Second
	trackEvery = time.Second
)

// Config configures every process of one Claude harness.
type Config struct {
	Bin     string      // the user's installed claude
	Env     []string    // the base environment; nil is the user's own (R1)
	Args    []string    // extra flags, such as the permission tool and MCP config
	OnFrame func(Frame) // every stdout frame in order, called from the reader; may be nil
	OnExit  func()      // the running process exited without Close; may be nil
}

// Frame is one stdout line of the stream-json protocol.
type Frame struct {
	Type, Subtype string
	Raw           []byte
}

// ProcessSpec is one agent's process.
type ProcessSpec struct {
	SessionID string             // reserved at Open (SessionID), saved before the first launch
	Launch    loomharness.Launch // the agent's profile root and env (LaunchFor)
	Dir       string             // the agent's worktree
	Model     string
}

// SessionID reserves the Claude session UUID for an Open key. It is derived,
// so a repeated Open with the same key reserves the same UUID.
func SessionID(key string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("loom:claude:"+key)).String()
}

// LaunchFor resolves an agent's Claude launch from its fixed profile key, so
// a renamed agent keeps its profile. It uses the one shared resolve, verify
// and export policy: an existing verified .loom/agent-profiles/<key>/claude
// sets CLAUDE_CONFIG_DIR and exports its nonempty oauth-token as
// CLAUDE_CODE_OAUTH_TOKEN; an invalid one refuses with its typed error and
// never falls back; with none, nothing is set and the user's own Claude root
// and login are inherited unchanged. Root is the same root transcript
// discovery and doctor resolve (sessions.ClaudeConfigDirFor).
func LaunchFor(projectDir, profileKey string) (loomharness.Launch, error) {
	dir, assignments, err := agentprofile.HarnessEnv(projectDir, profileKey, "claude")
	if err != nil {
		return loomharness.Launch{}, fmt.Errorf("claude profile %q: %w", profileKey, err)
	}
	l := loomharness.Launch{Root: dir, Env: map[string]string{}}
	for _, kv := range assignments {
		k, v, _ := strings.Cut(kv, "=")
		l.Env[k] = v
	}
	if dir == "" {
		l.Root = sessions.ClaudeConfigDir()
	}
	return l, nil
}

// githubTokens never reach Claude nor the tools it runs: agents publish
// through Loom (R32). The same list as the OpenCode adapter.
var githubTokens = []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE", "LOOM_PR_GIT_PASSWORD"}

// Process is one agent's long-running `claude -p` stream-json process. It
// starts on the first Prompt and keeps running between turns.
type Process struct {
	cfg  Config
	spec ProcessSpec

	launchMu sync.Mutex // orders Prompt and Close

	mu      sync.Mutex // the state the reader shares
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	exited  chan struct{}
	inited  chan struct{} // closed by system/init
	first   chan struct{} // closed by the first frame
	stderr  *tail
	tree    *proctree.Tree // the running process's recorded descendants
	caps    []string
	busy    bool
	pending map[string]chan controlResponse
	warning string
	closing bool // Close or a failed launch is stopping the process
}

// NewProcess returns a process; nothing starts until the first Prompt.
func NewProcess(cfg Config, spec ProcessSpec) *Process {
	return &Process{cfg: cfg, spec: spec, pending: map[string]chan controlResponse{}}
}

// Warning is the version warning of the last launch, or "".
func (p *Process) Warning() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.warning
}

// Busy reports whether a prompted turn is running (until its result).
func (p *Process) Busy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.busy
}

// Prompt sends one user message, keyed by key (echoed as its uuid), and
// launches the process first if none runs. It returns ErrBusy during a turn.
func (p *Process) Prompt(ctx context.Context, key, text string) error {
	line, err := json.Marshal(map[string]any{
		"type": "user", "uuid": key, "session_id": p.spec.SessionID,
		"message": map[string]string{"role": "user", "content": text},
	})
	if err != nil {
		return err
	}
	p.launchMu.Lock()
	defer p.launchMu.Unlock()
	p.mu.Lock()
	if p.busy {
		p.mu.Unlock()
		return loomharness.ErrBusy
	}
	if p.cmd != nil {
		p.busy = true
		err := p.writeLocked(line)
		p.busy = err == nil
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()
	return p.launch(ctx, line)
}

// launch starts the process with --resume when this session's transcript
// exists, else --session-id, and sends line. Claude refuses a --session-id
// that is already in use, so that answer relaunches with --resume.
func (p *Process) launch(ctx context.Context, line []byte) error {
	resume := p.transcriptExists()
	for {
		if err := p.start(ctx, resume); err != nil {
			return err
		}
		p.mu.Lock()
		p.busy = true
		_ = p.writeLocked(line) // an early exit is read from stderr below
		first, exited, stderr := p.first, p.exited, p.stderr
		p.mu.Unlock()
		select {
		case <-first:
			return nil
		case <-ctx.Done():
			p.stop()
			return ctx.Err()
		case <-exited:
		}
		if !resume && strings.Contains(stderr.String(), "already in use") {
			resume = true
			continue
		}
		return fmt.Errorf("claude exited at launch: %s: %w", strings.TrimSpace(stderr.String()), loomharness.ErrUnavailable)
	}
}

// start checks the version, then starts the process and its reader.
func (p *Process) start(ctx context.Context, resume bool) error {
	env := p.env()
	if err := p.checkRoot(env); err != nil {
		return err
	}
	ver := exec.CommandContext(ctx, p.cfg.Bin, "--version") //nolint:gosec // G204: the configured claude binary.
	ver.Env = env
	out, err := ver.Output()
	if err != nil {
		return fmt.Errorf("claude --version: %w: %w", loomharness.ErrUnavailable, err)
	}
	vc, err := loomharness.CheckVersion("claude", string(out))
	if err != nil {
		return err
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-partial-messages", "--replay-user-messages"}
	if resume {
		args = append(args, "--resume", p.spec.SessionID)
	} else {
		args = append(args, "--session-id", p.spec.SessionID)
	}
	if p.spec.Model != "" {
		args = append(args, "--model", p.spec.Model)
	}
	cmd := exec.Command(p.cfg.Bin, append(args, p.cfg.Args...)...) //nolint:gosec // G204: the configured claude binary.
	cmd.Dir, cmd.Env = p.spec.Dir, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr := &tail{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude: start: %w: %w", loomharness.ErrUnavailable, err)
	}
	tree := proctree.New()
	p.mu.Lock()
	p.cmd, p.stdin, p.stderr, p.caps, p.warning, p.tree = cmd, stdin, stderr, nil, vc.Warning(), tree
	p.exited, p.inited, p.first = make(chan struct{}), make(chan struct{}), make(chan struct{})
	exited, inited, first := p.exited, p.inited, p.first
	p.mu.Unlock()
	go tree.Track(cmd.Process.Pid, exited, trackEvery)
	go p.read(cmd, stdout, tree, exited, inited, first)
	return nil
}

// read handles every stdout frame, then reaps the process.
func (p *Process) read(cmd *exec.Cmd, stdout io.Reader, tree *proctree.Tree, exited, inited, first chan struct{}) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		raw := slices.Clone(sc.Bytes())
		var f struct {
			Type, Subtype string
			Capabilities  []string
			Response      controlResponse
		}
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		p.mu.Lock()
		switch {
		case f.Type == "system" && f.Subtype == "init":
			p.caps = f.Capabilities
			closeOnce(inited)
		case f.Type == "result":
			p.busy = false
		case f.Type == "control_response":
			if ch, ok := p.pending[f.Response.RequestID]; ok {
				delete(p.pending, f.Response.RequestID)
				ch <- f.Response
			}
		}
		p.mu.Unlock()
		closeOnce(first)
		if p.cfg.OnFrame != nil {
			p.cfg.OnFrame(Frame{Type: f.Type, Subtype: f.Subtype, Raw: raw})
		}
	}
	// stdout closed: Claude is exiting. Tool processes it started may outlive
	// it, inside or outside its process group. Record them, signal the group
	// while the unreaped leader still holds its id (so it cannot be reused),
	// and reap exactly the recorded tree (SIGTERM, then SIGKILL) before
	// reporting the exit.
	tree.Record(cmd.Process.Pid)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	_ = cmd.Wait()
	tree.Reap(stopGrace)
	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd, p.busy = nil, false
	}
	lost := !p.closing && isClosed(first)
	p.closing = false
	p.mu.Unlock()
	close(exited)
	if lost && p.cfg.OnExit != nil {
		p.cfg.OnExit()
	}
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

type controlResponse struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}

// Interrupt ends only the current turn with the documented stream-json
// control_request, once system/init advertised interrupt_receipt_v1, and
// waits for its receipt. The process keeps running for the next prompt. It
// never signals the process. It reports false when no turn runs.
func (p *Process) Interrupt(ctx context.Context) (bool, error) {
	p.mu.Lock()
	if p.cmd == nil || !p.busy {
		p.mu.Unlock()
		return false, nil
	}
	inited, exited := p.inited, p.exited
	p.mu.Unlock()
	select {
	case <-inited:
	case <-exited:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
	id := uuid.NewString()
	ch := make(chan controlResponse, 1)
	p.mu.Lock()
	if !slices.Contains(p.caps, "interrupt_receipt_v1") {
		p.mu.Unlock()
		return false, fmt.Errorf("claude did not advertise interrupt_receipt_v1: %w", loomharness.ErrUnavailable)
	}
	line, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": map[string]string{"subtype": "interrupt"}})
	p.pending[id] = ch
	err := p.writeLocked(line)
	p.mu.Unlock()
	if err != nil {
		return false, err
	}
	select {
	case r := <-ch:
		if r.Subtype != "success" {
			return false, fmt.Errorf("claude interrupt refused: %s", r.Error)
		}
		return true, nil
	case <-exited:
		return false, fmt.Errorf("claude exited before the interrupt receipt: %w", loomharness.ErrUnavailable)
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return false, ctx.Err()
	}
}

// Close stops this agent's process (SIGTERM, then SIGKILL of its group after
// stopGrace) and, before returning, every recorded descendant still running
// (proctree), even when Claude itself exits at once. Its native session is
// kept, so the next Prompt resumes it.
func (p *Process) Close(context.Context) error {
	p.launchMu.Lock()
	defer p.launchMu.Unlock()
	p.stop()
	return nil
}

func (p *Process) stop() {
	p.mu.Lock()
	cmd, exited, tree := p.cmd, p.exited, p.tree
	p.closing = cmd != nil
	p.mu.Unlock()
	if cmd == nil {
		return
	}
	tree.Record(cmd.Process.Pid)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(stopGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	}
}

func (p *Process) writeLocked(line []byte) error {
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("claude: write stdin: %w: %w", loomharness.ErrUnavailable, err)
	}
	return nil
}

// transcriptExists reports whether this session's own transcript exists
// under its root; --resume by id finds it in any project.
func (p *Process) transcriptExists() bool {
	root := p.spec.Launch.Root
	if root == "" {
		root = sessions.ClaudeConfigDir()
	}
	m, _ := filepath.Glob(filepath.Join(root, "projects", "*", p.spec.SessionID+".jsonl"))
	return len(m) > 0
}

// checkRoot fails unless the config dir the child will use (CLAUDE_CONFIG_DIR
// in env, else HOME/.claude) resolves to the recorded root, so its transcripts
// land where discovery and Purge look. It runs at Open and before every launch:
// an alias retargeted since Open is refused, never followed.
func (p *Process) checkRoot(env []string) error {
	root := p.spec.Launch.Root
	if root == "" {
		root = sessions.ClaudeConfigDir()
	}
	dir, ok := lookup(env, "CLAUDE_CONFIG_DIR")
	if !ok || dir == "" {
		home, _ := lookup(env, "HOME")
		dir = filepath.Join(home, ".claude")
	}
	want, err := canonical(root)
	if err != nil {
		return fmt.Errorf("claude: recorded root %s: %w", root, err)
	}
	got, err := canonical(dir)
	if err != nil {
		return fmt.Errorf("claude: config dir %s: %w", dir, err)
	}
	if got != want {
		return fmt.Errorf("claude: config dir %s resolves to %s, not the recorded root %s; refusing", dir, got, want)
	}
	return nil
}

// lookup returns the value of key in env.
func lookup(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v, true
		}
	}
	return "", false
}

// env is the base environment without GitHub tokens, with the launch env
// applied over it, so a profile token overrides an inherited one. HOME is
// never replaced.
func (p *Process) env() []string {
	base := p.cfg.Env
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+len(p.spec.Launch.Env))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, set := p.spec.Launch.Env[k]; !set && !slices.Contains(githubTokens, k) {
			out = append(out, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(p.spec.Launch.Env)) {
		if !slices.Contains(githubTokens, k) {
			out = append(out, k+"="+p.spec.Launch.Env[k])
		}
	}
	return out
}

// tail keeps the last 4 KiB of stderr for launch errors.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if n := len(t.buf) - 4096; n > 0 {
		t.buf = t.buf[n:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
