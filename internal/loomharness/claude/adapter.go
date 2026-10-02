package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// Adapter is the Claude harness: one Process per session, started on its
// first Prompt. It implements the harness port so callers never branch on
// harness.
type Adapter struct {
	cfg Config // Bin, Env and Args shared by every process

	mu       sync.Mutex
	sessions map[loomharness.NativeRef]*Session // by root and native id
	feeds    map[*feed]struct{}
}

var _ loomharness.Harness = (*Adapter)(nil)

// New returns an adapter; nothing starts until a session's first Prompt.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, sessions: map[loomharness.NativeRef]*Session{}, feeds: map[*feed]struct{}{}}
}

// Name is the harness name.
func (a *Adapter) Name() string { return "claude" }

// Models lists the model aliases `claude --model` accepts; the CLI has no
// model list.
func (a *Adapter) Models(context.Context) ([]loomharness.Model, error) {
	return []loomharness.Model{{ID: "opus", Name: "Opus"}, {ID: "sonnet", Name: "Sonnet"}, {ID: "haiku", Name: "Haiku"}}, nil
}

// Health checks the installed version: refused below the minimum, a warning
// above the last tested one. It starts no session. The version child gets
// the same GitHub-token-free environment as a launch.
func (a *Adapter) Health(ctx context.Context) (loomharness.Health, error) {
	cmd := exec.CommandContext(ctx, a.cfg.Bin, "--version") //nolint:gosec // G204: the configured claude binary.
	cmd.Env = NewProcess(a.cfg, ProcessSpec{}).env()
	out, err := cmd.Output()
	if err != nil {
		return loomharness.Health{Warning: fmt.Sprintf("claude --version: %v", err)}, nil
	}
	vc, err := loomharness.CheckVersion("claude", string(out))
	if err != nil {
		return loomharness.Health{Version: vc, Warning: err.Error()}, nil
	}
	return loomharness.Health{OK: true, Version: vc, Warning: vc.Warning()}, nil
}

// Restart is a no-op for Claude (the port's contract): there is no shared
// server, and each session's process restarts on its own next Prompt.
func (a *Adapter) Restart(context.Context) error { return nil }

// Open reserves the session's UUID (derived from spec.Key, so a repeat
// returns the same session) and returns the actual NativeRef, root included,
// so the caller records it before first use. Nothing launches until the
// first Prompt, so a failed start leaves no unrecorded native session.
//
// Claude has no native rule install yet: the permission tool lands in 5.2
// and Resume's install in 5.3. Until then Open refuses any rules rather than
// run a session without them.
func (a *Adapter) Open(_ context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	if len(spec.Rules) > 0 {
		return loomharness.NativeRef{}, fmt.Errorf("claude: permission rules cannot be installed yet (5.2): %w", loomharness.ErrUnavailable)
	}
	if spec.Dir == "" {
		return loomharness.NativeRef{}, errors.New("claude: Open needs the agent's worktree")
	}
	l := spec.Launch
	if l.Root == "" {
		l.Root = sessions.ClaudeConfigDir()
	}
	// Record the root once, canonical, so a root reached through a symlink
	// (a dotfile-managed ~/.claude) stays purgeable: Purge never resolves a
	// recorded root and refuses symlinks. CLAUDE_CONFIG_DIR is left as given,
	// since Claude keys its keychain login by that exact string.
	root, err := canonical(l.Root)
	if err != nil {
		return loomharness.NativeRef{}, fmt.Errorf("claude: config root %s: %w", l.Root, err)
	}
	l.Root = root
	check := NewProcess(a.cfg, ProcessSpec{Launch: l})
	if err := check.checkRoot(check.env()); err != nil {
		return loomharness.NativeRef{}, err
	}
	ref := loomharness.NativeRef{Root: root, NativeID: SessionID(spec.Key)}
	s := a.session(ref)
	s.mu.Lock()
	s.spec.Launch, s.spec.Dir, s.spec.Model = l, spec.Dir, spec.Model
	s.mu.Unlock()
	return ref, nil
}

// Session returns the session for a recorded ref.
func (a *Adapter) Session(ref loomharness.NativeRef) loomharness.Session { return a.session(ref) }

func (a *Adapter) session(ref loomharness.NativeRef) *Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[ref]; ok {
		return s
	}
	s := &Session{a: a, ref: ref, m: newMapper(ref),
		spec: ProcessSpec{SessionID: ref.NativeID, Launch: loomharness.Launch{Root: ref.Root}}}
	a.sessions[ref] = s
	return s
}

// Purge deletes exactly <root>/projects/<project>/<session-id>.jsonl for each
// recorded ref, under the ref's own recorded root; it never re-resolves a
// root and never runs `claude project purge`. It stops the session's process
// first. It deletes only regular files reached through real directories
// inside the recorded root: a recorded root with a symlink anywhere on its
// path, a symlinked projects or project directory, or a symlinked transcript,
// that holds the session is refused, never followed.
// A ref whose root or id cannot be proven fails; an already deleted
// transcript is fine, so a failed purge can be retried after a restart.
func (a *Adapter) Purge(ctx context.Context, owned []loomharness.NativeRef) error {
	for _, ref := range owned {
		if _, err := uuid.Parse(ref.NativeID); err != nil || !filepath.IsAbs(ref.Root) {
			return fmt.Errorf("claude purge: cannot prove the session path for %q under %q", ref.NativeID, ref.Root)
		}
		if err := realDir(ref.Root); err != nil {
			return fmt.Errorf("claude purge: recorded root %s: %w", ref.Root, err)
		}
		a.mu.Lock()
		s := a.sessions[ref]
		a.mu.Unlock()
		if s != nil {
			if err := s.Close(ctx); err != nil {
				return err
			}
		}
		if err := purgeOne(ref); err != nil {
			return fmt.Errorf("claude purge %s under %s: %w", ref.NativeID, ref.Root, err)
		}
	}
	return nil
}

// canonical returns absolute path p with every symlink resolved; a path that
// does not exist yet resolves through its longest existing parent.
func canonical(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	var rest []string
	for cur := filepath.Clean(p); ; cur = filepath.Dir(cur) {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{r}, rest...)...), nil
		}
		if !os.IsNotExist(err) || cur == filepath.Dir(cur) {
			return "", err
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
	}
}

// realDir proves p is an existing directory reached without any symlink:
// p and every directory above it are checked with Lstat, never followed.
func realDir(p string) error {
	for cur := filepath.Clean(p); ; cur = filepath.Dir(cur) {
		info, err := os.Lstat(cur)
		if err != nil {
			return fmt.Errorf("cannot prove it exists: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s is a symlink or not a directory; refusing", cur)
		}
		if cur == filepath.Dir(cur) {
			return nil
		}
	}
}

// purgeOne removes ref's transcript from every real project directory of its
// root, refusing any copy reached through a symlink.
func purgeOne(ref loomharness.NativeRef) error {
	projects := filepath.Join(ref.Root, "projects")
	info, err := os.Lstat(projects)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s is not a real directory inside the recorded root; refusing", projects)
	}
	entries, err := os.ReadDir(projects)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(projects, e.Name(), ref.NativeID+".jsonl")
		info, err := os.Lstat(path)
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%s is reached through a symlink or is not a regular file; refusing", path)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Feed returns the live events of every session.
func (a *Adapter) Feed(ctx context.Context) (loomharness.Feed, error) {
	f := &feed{a: a, ch: make(chan loomharness.Event, 256), done: make(chan struct{})}
	a.mu.Lock()
	a.feeds[f] = struct{}{}
	a.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			_ = f.Close()
		case <-f.done:
		}
	}()
	return f, nil
}

// publish sends events to every open feed, in order. A slow reader holds the
// session's stdout reader (back-pressure), so nothing is dropped.
func (a *Adapter) publish(events []loomharness.Event) {
	a.mu.Lock()
	feeds := make([]*feed, 0, len(a.feeds))
	for f := range a.feeds {
		feeds = append(feeds, f)
	}
	a.mu.Unlock()
	for _, f := range feeds {
		for _, e := range events {
			select {
			case f.ch <- e:
			case <-f.done:
			}
		}
	}
}

type feed struct {
	a    *Adapter
	ch   chan loomharness.Event
	done chan struct{}
	once sync.Once
}

func (f *feed) Events() <-chan loomharness.Event { return f.ch }

func (f *feed) Close() error {
	f.once.Do(func() {
		f.a.mu.Lock()
		delete(f.a.feeds, f)
		f.a.mu.Unlock()
		close(f.done)
	})
	return nil
}

// Session is one Claude session and its process.
type Session struct {
	a   *Adapter
	ref loomharness.NativeRef

	mu   sync.Mutex
	spec ProcessSpec // the next launch's spec; SetModel and Move change it
	proc *Process
	m    *mapper
}

var _ loomharness.Session = (*Session)(nil)

// Prompt sends one input when idle, launching the process if none runs. A
// model or worktree changed since the running process started takes effect
// here, at the turn boundary, by relaunching with --resume.
func (s *Session) Prompt(ctx context.Context, in loomharness.Input) error {
	s.mu.Lock()
	if s.spec.Dir == "" {
		s.mu.Unlock()
		return fmt.Errorf("claude session %s has no worktree; Open it first", s.ref.NativeID)
	}
	proc, spec := s.proc, s.spec
	s.mu.Unlock()
	if proc != nil && proc.Busy() {
		return loomharness.ErrBusy
	}
	if proc != nil && (proc.spec.Dir != spec.Dir || proc.spec.Model != spec.Model) {
		if err := proc.Close(ctx); err != nil {
			return err
		}
		proc = nil
	}
	if proc == nil {
		cfg := s.a.cfg
		cfg.OnFrame, cfg.OnExit = s.onFrame, s.onExit
		proc = NewProcess(cfg, spec)
	}
	s.mu.Lock()
	if !proc.Running() { // this Prompt launches a process: its cost and usage start at 0
		s.m.cost, s.m.usage = 0, loomharness.Usage{}
	}
	s.proc = proc
	s.m.pending[in.Key], s.m.handed = true, in.Key
	s.mu.Unlock()
	err := proc.Prompt(ctx, in.Key, in.Text)
	if err != nil {
		s.mu.Lock()
		delete(s.m.pending, in.Key)
		if s.m.handed == in.Key {
			s.m.handed = ""
		}
		s.mu.Unlock()
	}
	return err
}

func (s *Session) onFrame(f Frame) {
	s.mu.Lock()
	events := s.m.frame(f.Raw)
	s.mu.Unlock()
	s.a.publish(events)
}

func (s *Session) onExit() {
	s.mu.Lock()
	e := s.m.exited()
	s.mu.Unlock()
	s.a.publish([]loomharness.Event{e})
}

// Interrupt ends only the running turn; the turn then completes as cancelled.
func (s *Session) Interrupt(ctx context.Context) (bool, error) {
	s.mu.Lock()
	proc := s.proc
	s.m.cancelled = proc != nil
	s.mu.Unlock()
	if proc == nil {
		return false, nil
	}
	ok, err := proc.Interrupt(ctx)
	if !ok {
		s.mu.Lock()
		s.m.cancelled = false
		s.mu.Unlock()
	}
	return ok, err
}

// Status is the adapter's own view: running from a turn's first frame (or
// its prompt) until its result.
func (s *Session) Status(context.Context) (loomharness.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	busy := s.proc != nil && s.proc.Busy()
	return loomharness.Status{Running: busy || s.m.turnID != "", TurnID: s.m.turnID, LastTurnInterrupt: s.m.lastInterrupted}, nil
}

// SetModel takes effect from the next turn: the next Prompt relaunches with
// --model and --resume.
func (s *Session) SetModel(_ context.Context, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spec.Model = model
	return nil
}

// Move takes effect from the next turn: the next Prompt relaunches in dir
// with --resume of the same session (--resume by id searches every project),
// so the NativeRef is unchanged.
func (s *Session) Move(_ context.Context, dir string) error {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("claude: move to %s: not a directory", dir)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spec.Dir = dir
	return nil
}

// Unload ends an idle session's process; its native session is kept.
func (s *Session) Unload(ctx context.Context) error {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc != nil && proc.Busy() {
		return loomharness.ErrBusy
	}
	return s.Close(ctx)
}

// Close stops only this session's process. The native transcript is kept;
// nothing project-wide is touched.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return nil
	}
	return proc.Close(ctx)
}

// Resume (5.3) must install the current rules before anything runs (R-H);
// until then it fails, and nothing runs.
func (s *Session) Resume(context.Context, loomharness.Launch, []loomharness.PermissionRule) (loomharness.NativeRef, error) {
	return loomharness.NativeRef{}, fmt.Errorf("claude: Resume is not available until 5.3 installs rules first: %w", loomharness.ErrUnavailable)
}

// Reply answers an ask through the permission tool (5.2); until then it fails.
func (s *Session) Reply(context.Context, string, loomharness.Reply) error {
	return fmt.Errorf("claude: Reply is not available until the 5.2 permission tool: %w", loomharness.ErrUnavailable)
}

// HasInput reads the transcript (5.2b); until then it fails.
func (s *Session) HasInput(context.Context, string) (loomharness.Landed, error) {
	return loomharness.LandedUnknown, fmt.Errorf("claude: HasInput is not available until the 5.2b transcript reader: %w", loomharness.ErrUnavailable)
}

// Messages reads the transcript (5.2b); until then it fails.
func (s *Session) Messages(context.Context, string, int) (loomharness.MessagePage, error) {
	return loomharness.MessagePage{}, fmt.Errorf("claude: Messages is not available until the 5.2b transcript reader: %w", loomharness.ErrUnavailable)
}
