package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
)

// Adapter is the codex harness over the supervised app-servers: it opens
// and purges threads, serves their sessions, and maps every root's
// notifications and server requests to one live feed.
//
// Resume (installing rules on a recorded thread) is 4.2b; until then Open
// refuses rules rather than run a thread without them.
type Adapter struct {
	*Supervisor

	mu    sync.Mutex
	feeds map[*feed]struct{}
	asks  map[string]map[string]Message // open server requests by root, then ask id
	// starts holds each thread's turn.started until the turn's user item
	// gives it the input's key (codex sends turn/started first).
	starts map[loomharness.NativeRef]loomharness.Event

	openMu sync.Mutex         // one Open at a time, so a key never gets two threads
	opened map[opening]string // thread ids this process opened, until purged
}

// opening is what Open is idempotent by.
type opening struct{ root, dir, key string }

// NewAdapter returns an adapter; cfg.Unrouted is its own.
func NewAdapter(cfg Config) *Adapter {
	a := &Adapter{feeds: map[*feed]struct{}{}, asks: map[string]map[string]Message{}, starts: map[loomharness.NativeRef]loomharness.Event{}, opened: map[opening]string{}}
	cfg.Unrouted = a.receive
	a.Supervisor = New(cfg)
	return a
}

// Name is the harness name.
func (a *Adapter) Name() string { return "codex" }

// LaunchFor is the launch input for an agent: its verified codex profile
// root when it has one, else the user's inherited root. profileKey is the
// agent's original key, which keeps selecting its profile after a rename.
// An existing but unverifiable profile is an error, never the user's root.
func (a *Adapter) LaunchFor(projectDir, profileKey string) (loomharness.Launch, error) {
	dir, env, err := agentprofile.HarnessEnv(projectDir, profileKey, "codex")
	if err != nil {
		return loomharness.Launch{}, err
	}
	l := loomharness.Launch{Root: a.Root(dir)}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if l.Env == nil {
			l.Env = map[string]string{}
		}
		l.Env[k] = v
	}
	return l, nil
}

// markerPrefix names a Loom thread "loom:<key>". codex keeps a name set
// before the first turn, and once the thread has its first user message
// thread/list searchTerm finds it, after a restart too (probed on 0.157.1;
// threadSource is not persisted). Before that message codex lists nothing
// for it and it has no history, so this process remembers what it opened.
const markerPrefix = "loom:"

// Open returns the thread for spec.Key in spec.Dir on spec.Launch.Root's
// app-server: the one this process opened, else the one named for the key,
// else a new one. The ref names the canonical root the thread lives under.
// On an error it returns the zero ref and leaves nothing behind: a new
// thread that cannot be named is deleted again. Only when that delete fails
// too does it return the thread's ref with both errors, so the caller
// records it and Purges it later (the port's Open failure contract).
//
// The launch root alone selects the profile (its server runs with
// CODEX_HOME set to it); codex profiles carry no secret env.
func (a *Adapter) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	if len(spec.Rules) > 0 || len(spec.Preset.Tools) > 0 {
		return loomharness.NativeRef{}, errors.New("codex: permission rules and preset tools are not installed yet (4.2); refusing to open a thread without them")
	}
	ref, created, err := a.adoptOrCreate(ctx, spec)
	if err == nil {
		return ref, nil
	}
	if !created {
		return loomharness.NativeRef{}, err
	}
	// Delete the new thread outside openMu, so a slow delete never holds up
	// another Open, and even when ctx has ended.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if derr := a.delete(dctx, ref); derr != nil {
		return ref, fmt.Errorf("%w (and deleting the new thread %s failed: %w)", err, ref.NativeID, derr)
	}
	return loomharness.NativeRef{}, err
}

// cleanupTimeout bounds deleting a thread Open created but could not name.
var cleanupTimeout = 10 * time.Second

// adoptOrCreate is Open's step under openMu: the thread this process opened
// for the key, else the one named for it, else a new named one. created
// reports a new thread that it could not name; that thread is unnamed and
// unrecorded, so no other Open can adopt it.
func (a *Adapter) adoptOrCreate(ctx context.Context, spec loomharness.OpenSpec) (ref loomharness.NativeRef, created bool, err error) {
	ref = loomharness.NativeRef{Root: a.Root(spec.Launch.Root)}
	key := opening{ref.Root, spec.Dir, spec.Key}
	a.openMu.Lock()
	defer a.openMu.Unlock()
	if ref.NativeID = a.opened[key]; ref.NativeID != "" {
		return ref, false, nil
	}
	conn, err := a.Conn(ctx, ref.Root)
	if err != nil {
		return ref, false, err
	}
	marker := markerPrefix + spec.Key
	if ref.NativeID, err = adopt(ctx, conn, spec.Dir, marker); err != nil || ref.NativeID != "" {
		return ref, false, err
	}
	params := protocol.ThreadStartParams{Cwd: &spec.Dir, HistoryMode: protocol.ThreadHistoryModePaginated}
	if spec.Model != "" {
		params.Model = &spec.Model
	}
	if spec.Preset.Persona != "" {
		params.DeveloperInstructions = &spec.Preset.Persona
	}
	var started protocol.ThreadStartResponse
	if err := conn.Call(ctx, "thread/start", params, &started); err != nil {
		return ref, false, fmt.Errorf("codex thread/start: %w", err)
	}
	ref.NativeID = started.Thread.Id
	if err := conn.Call(ctx, "thread/name/set", protocol.ThreadSetNameParams{ThreadId: ref.NativeID, Name: marker}, nil); err != nil {
		return ref, true, fmt.Errorf("codex thread/name/set: %w", err)
	}
	a.opened[key] = ref.NativeID
	return ref, false, nil
}

// adopt finds the thread named marker in dir: "" when none, an error when
// more than one.
func adopt(ctx context.Context, conn *Conn, dir, marker string) (string, error) {
	cwd, _ := json.Marshal(dir)
	params := protocol.ThreadListParams{Cwd: cwd, SearchTerm: &marker}
	var found []string
	for {
		var page protocol.ThreadListResponse
		if err := conn.Call(ctx, "thread/list", params, &page); err != nil {
			return "", fmt.Errorf("codex thread/list: %w", err)
		}
		for _, t := range page.Data {
			if t.Name != nil && *t.Name == marker {
				found = append(found, t.Id)
			}
		}
		if page.NextCursor == nil || len(page.Data) == 0 {
			break
		}
		params.Cursor = page.NextCursor
	}
	if len(found) > 1 {
		return "", fmt.Errorf("codex: %d threads named %q in %s: refusing to pick one", len(found), marker, dir)
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", nil
}

// Purge deletes exactly the given recorded threads, each on the
// app-server of its recorded root; one already gone is fine.
func (a *Adapter) Purge(ctx context.Context, owned []loomharness.NativeRef) error {
	for _, ref := range owned {
		if err := a.delete(ctx, ref); err != nil {
			return err
		}
		a.forget(ref)
	}
	return nil
}

// forget drops the record that this process opened ref.
func (a *Adapter) forget(ref loomharness.NativeRef) {
	a.openMu.Lock()
	defer a.openMu.Unlock()
	for k, id := range a.opened {
		if k.root == a.Root(ref.Root) && id == ref.NativeID {
			delete(a.opened, k)
		}
	}
}

// delete deletes one thread on its recorded root; one already gone is fine.
func (a *Adapter) delete(ctx context.Context, ref loomharness.NativeRef) error {
	err := a.session(ref).call(ctx, "thread/delete", protocol.ThreadDeleteParams{ThreadId: ref.NativeID}, nil)
	var rpc *RPCError
	if errors.As(err, &rpc) && strings.HasPrefix(rpc.Message, "no rollout found") {
		return nil // deleted before
	}
	if err != nil {
		return fmt.Errorf("codex delete %s under %s: %w", ref.NativeID, ref.Root, err)
	}
	return nil
}

// Restart restarts every app-server Loom runs, one root at a time, so the
// idle timer frees their memory; a root with no running server stays down
// until its next use. Only servers this supervisor spawned are touched.
func (a *Adapter) Restart(ctx context.Context) error {
	var errs []error
	for _, root := range a.running() {
		errs = append(errs, a.Supervisor.Restart(ctx, root))
	}
	return errors.Join(errs...)
}

// Models lists the models the inherited root's app-server offers.
func (a *Adapter) Models(ctx context.Context) ([]loomharness.Model, error) {
	conn, err := a.Conn(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []loomharness.Model
	var params protocol.ModelListParams
	for {
		var page protocol.ModelListResponse
		if err := conn.Call(ctx, "model/list", params, &page); err != nil {
			return nil, fmt.Errorf("codex model/list: %w", err)
		}
		for _, m := range page.Data {
			out = append(out, loomharness.Model{ID: m.Model, Name: m.DisplayName})
		}
		if page.NextCursor == nil || len(page.Data) == 0 {
			return out, nil
		}
		params.Cursor = page.NextCursor
	}
}

// askMethods are the server requests that are asks; any other is refused.
var askMethods = map[string]bool{
	"item/commandExecution/requestApproval": true,
	"item/fileChange/requestApproval":       true,
	"item/permissions/requestApproval":      true,
	"item/tool/requestUserInput":            true,
	"mcpServer/elicitation/request":         true,
}

// receive handles one root's messages on its reader: it keeps open asks,
// refuses server requests that are not asks, and publishes the mapped
// events. A gap is feed.gap for that root plus ask.lost for its open asks.
func (a *Adapter) receive(root string, m Message) {
	if m.Gap {
		a.mu.Lock()
		lost := a.asks[root]
		delete(a.asks, root)
		for ref := range a.starts {
			if ref.Root == root {
				delete(a.starts, ref)
			}
		}
		a.mu.Unlock()
		a.publish(loomharness.Event{Type: loomharness.EventFeedGap, Session: loomharness.NativeRef{Root: root}})
		for id, ask := range lost {
			a.publish(loomharness.Event{Type: loomharness.EventAskLost, Session: loomharness.NativeRef{Root: root, NativeID: ask.ThreadID}, AskID: id})
		}
		return
	}
	switch {
	case m.ID != nil && !askMethods[m.Method]:
		_ = m.RespondError(-32601, "loom: unsupported server request "+m.Method)
		return
	case m.ID != nil:
		a.mu.Lock()
		if a.asks[root] == nil {
			a.asks[root] = map[string]Message{}
		}
		a.asks[root][askID(m.ID)] = m
		a.mu.Unlock()
	case m.Method == "serverRequest/resolved":
		var p protocol.ServerRequestResolvedNotification
		if json.Unmarshal(m.Params, &p) == nil {
			a.mu.Lock()
			delete(a.asks[root], askID(p.RequestId))
			a.mu.Unlock()
		}
	}
	if e, ok := live(root, m); ok {
		a.started(e)
	}
}

// started publishes e, holding a turn.started until the next event of its
// thread: the turn's user item, whose key it then carries, or whatever
// came instead (a turn with no Loom input keeps an empty key).
func (a *Adapter) started(e loomharness.Event) {
	a.mu.Lock()
	start, held := a.starts[e.Session]
	delete(a.starts, e.Session)
	if e.Type == loomharness.EventTurnStarted {
		a.starts[e.Session] = e
	}
	a.mu.Unlock()
	if held {
		if e.Type == loomharness.EventMessageDelivered && e.TurnID == start.TurnID {
			start.InputKey = e.InputKey
		}
		a.publish(start)
	}
	if e.Type != loomharness.EventTurnStarted {
		a.publish(e)
	}
}

// askID is a server request id as text: a string id as is, a number as written.
func askID(id protocol.RequestId) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	return string(id)
}

// Feed subscribes to every root's events until ctx ends or Close.
func (a *Adapter) Feed(ctx context.Context) (loomharness.Feed, error) {
	ctx, cancel := context.WithCancel(ctx)
	f := &feed{ch: make(chan loomharness.Event), wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	a.mu.Lock()
	a.feeds[f] = struct{}{}
	a.mu.Unlock()
	go func() {
		f.run(ctx)
		a.mu.Lock()
		delete(a.feeds, f)
		a.mu.Unlock()
		close(f.done)
	}()
	return f, nil
}

// publish queues e on every feed; it never blocks the reader.
func (a *Adapter) publish(e loomharness.Event) {
	e.Time = time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for f := range a.feeds {
		f.push(e)
	}
}

// feed is one subscriber with an unbounded queue, so a slow reader never
// stalls an app-server connection or loses an event.
type feed struct {
	ch     chan loomharness.Event
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}

	mu sync.Mutex
	q  []loomharness.Event
}

func (f *feed) Events() <-chan loomharness.Event { return f.ch }

func (f *feed) Close() error {
	f.cancel()
	<-f.done
	return nil
}

func (f *feed) push(e loomharness.Event) {
	f.mu.Lock()
	f.q = append(f.q, e)
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *feed) run(ctx context.Context) {
	defer close(f.ch)
	for {
		f.mu.Lock()
		q := f.q
		f.q = nil
		f.mu.Unlock()
		for _, e := range q {
			select {
			case f.ch <- e:
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-f.wake:
		case <-ctx.Done():
			return
		}
	}
}
