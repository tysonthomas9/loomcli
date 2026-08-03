package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// The pending-input registry: the daemon half of the human answer path.
//
// The child's input-policy callback (backends/backend_input_policy.go) owns a
// harness prompt for exactly as long as it runs; role policy "ask" means that
// callback must hold the prompt open until a person picks an option. The
// watchdog half of that wait already exists (the InputWait edges suspend the
// idle kill), but the QUESTION itself used to die inside the child process —
// nothing carried its text or options anywhere an operator could see, so "ask"
// degraded to deny.
//
// This registry is where the question becomes visible and answerable. The
// child registers it over agent IPC (input_open), polls for a resolution
// (input_poll), and an operator answers over the control socket
// (agent_input_answer) from the CLI or the web UI. One slot per agent, not a
// queue: pkg/chat delivers requests to the callback synchronously one at a
// time, so a second open from the same agent means the first prompt is gone —
// it replaces the slot.
//
// The registry is deliberately memory-only. A pending question is meaningful
// exactly while the asking process is alive and waiting; persisting it would
// invite answering a prompt whose agent is long dead. Crash recovery is
// ageing: entries older than the input-wait bound are invisible to readers and
// rejected for answering, so a slot orphaned by a child crash cannot trap a
// later operator's click.

// PendingInputOption is one selectable option of a pending prompt.
type PendingInputOption struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// PendingInput is one agent's outstanding interactive prompt.
type PendingInput struct {
	// RequestID is minted by the child per prompt; poll and answer both carry
	// it so a late answer to a replaced prompt cannot resolve its successor.
	RequestID string               `json:"request_id"`
	Agent     string               `json:"agent"`
	Kind      string               `json:"kind"`
	Prompt    string               `json:"prompt"`
	Options   []PendingInputOption `json:"options,omitempty"`
	AskedAt   time.Time            `json:"asked_at"`

	// Answer is nil until an operator resolves the prompt.
	Answer *PendingInputAnswer `json:"answer,omitempty"`
}

// PendingInputAnswer is the operator's resolution of a pending prompt.
// Exactly one of OptionID, Text, or Decline carries the decision.
type PendingInputAnswer struct {
	OptionID string `json:"option_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Decline  bool   `json:"decline,omitempty"`
}

// inputRegistry holds at most one pending prompt per agent.
type inputRegistry struct {
	mu      sync.Mutex
	pending map[string]*PendingInput // agent name -> slot
}

func newInputRegistry() *inputRegistry {
	return &inputRegistry{pending: make(map[string]*PendingInput)}
}

// open registers (or replaces) the agent's pending prompt.
func (r *inputRegistry) open(p *PendingInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev := r.pending[p.Agent]; prev != nil && prev.RequestID != p.RequestID {
		slog.Info("pending input replaced by a newer prompt",
			"agent", p.Agent, "old_request", prev.RequestID, "new_request", p.RequestID)
	}
	r.pending[p.Agent] = p
}

// get returns the agent's live pending prompt, treating entries older than
// maxAge as absent. Age, not deletion, is the crash-recovery story: only the
// child may retire its own slot (poll-consume or close), so a reader ageing an
// entry out must not delete what a live-but-slow child still polls.
func (r *inputRegistry) get(agent string, maxAge time.Duration) *PendingInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending[agent]
	if p == nil || time.Since(p.AskedAt) > maxAge {
		return nil
	}
	cp := *p
	return &cp
}

// list returns every live pending prompt, oldest first.
func (r *inputRegistry) list(maxAge time.Duration) []*PendingInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*PendingInput, 0, len(r.pending))
	for _, p := range r.pending {
		if time.Since(p.AskedAt) > maxAge {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sortPendingByAge(out)
	return out
}

// answer resolves the agent's pending prompt. The request must still be live,
// the id must match the prompt being answered, and an option answer must name
// an option the prompt actually offered — an operator answering a stale screen
// gets an error, never a silent misdelivery to the next prompt.
func (r *inputRegistry) answer(agent, requestID string, a PendingInputAnswer, maxAge time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending[agent]
	if p == nil || time.Since(p.AskedAt) > maxAge {
		return fmt.Errorf("agent %q has no pending input request", agent)
	}
	if requestID != "" && requestID != p.RequestID {
		return fmt.Errorf("pending input request for %q is %q, not %q — the prompt changed under you", agent, p.RequestID, requestID)
	}
	if p.Answer != nil {
		return fmt.Errorf("pending input request for %q is already answered", agent)
	}
	if a.OptionID != "" {
		if opt := findOption(p.Options, a.OptionID); opt == nil {
			return fmt.Errorf("prompt for %q offers no option %q (options: %s)", agent, a.OptionID, optionIDs(p.Options))
		}
	} else if a.Text == "" && !a.Decline {
		return fmt.Errorf("an answer needs an option, text, or an explicit decline")
	}
	p.Answer = &a
	return nil
}

// consume returns the answer for (agent, requestID) and retires the slot when
// it is resolved. Unresolved returns (nil, true) — keep polling. A slot owned
// by a DIFFERENT request returns (nil, false) so a replaced child stops
// waiting on a prompt the registry no longer tracks.
func (r *inputRegistry) consume(agent, requestID string) (*PendingInputAnswer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending[agent]
	if p == nil || p.RequestID != requestID {
		return nil, false
	}
	if p.Answer == nil {
		return nil, true
	}
	delete(r.pending, agent)
	return p.Answer, true
}

// close retires the agent's slot if it still belongs to requestID (empty id
// force-clears, for the agent-exit edge).
func (r *inputRegistry) close(agent, requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.pending[agent]; p != nil && (requestID == "" || p.RequestID == requestID) {
		delete(r.pending, agent)
	}
}

func sortPendingByAge(ps []*PendingInput) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].AskedAt.Before(ps[j-1].AskedAt); j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

func findOption(opts []PendingInputOption, id string) *PendingInputOption {
	for i := range opts {
		if opts[i].ID == id {
			return &opts[i]
		}
	}
	return nil
}

func optionIDs(opts []PendingInputOption) string {
	ids := make([]string, len(opts))
	for i, o := range opts {
		ids[i] = o.ID
	}
	return strings.Join(ids, ", ")
}

// inputMaxAge is the visibility bound for pending prompts: the same input-wait
// ceiling the watchdog uses, plus a grace period so a prompt does not vanish
// from the operator's screen at the exact moment the child gives up on it —
// the child's own deadline is what retires the wait; this bound only has to
// outlive it.
func (d *Daemon) inputMaxAge() time.Duration {
	maxWait := defaultInputWaitMaxSecondsFallback
	if d.sup != nil {
		if v := d.sup.GetInputWaitMax(); v > 0 {
			maxWait = v
		}
	}
	return time.Duration(maxWait)*time.Second + 2*time.Minute
}

// defaultInputWaitMaxSecondsFallback mirrors the supervisor's default bound for
// the degenerate no-supervisor path (tests, early startup).
const defaultInputWaitMaxSecondsFallback = 900

// --- agent IPC handlers (child side of the socket) ---

// ipcInputOpenArgs is the input_open payload.
// Wire-compatible with cli.IPCInputOpenArgs.
type ipcInputOpenArgs struct {
	RequestID string               `json:"request_id"`
	Kind      string               `json:"kind"`
	Prompt    string               `json:"prompt"`
	Options   []PendingInputOption `json:"options,omitempty"`
}

// ipcInputPollArgs is the input_poll / input_close payload.
// Wire-compatible with cli.IPCInputPollArgs.
type ipcInputPollArgs struct {
	RequestID string `json:"request_id"`
}

// ipcInputPollResult is the input_poll response body.
// Wire-compatible with cli.IPCInputPollResult.
type ipcInputPollResult struct {
	// Resolved reports whether Answer carries a decision. False with Tracked
	// true means keep waiting.
	Resolved bool `json:"resolved"`
	// Tracked is false when the registry no longer owns this request — the
	// prompt was replaced or force-cleared, and the child should stop waiting.
	Tracked bool                `json:"tracked"`
	Answer  *PendingInputAnswer `json:"answer,omitempty"`
}

func (d *Daemon) handleIPCInputOpen(req AgentIPCRequest) AgentIPCResponse {
	var args ipcInputOpenArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return AgentIPCResponse{Error: fmt.Sprintf("input_open: bad args: %v", err)}
	}
	if args.RequestID == "" || args.Prompt == "" {
		return AgentIPCResponse{Error: "input_open: request_id and prompt are required"}
	}
	d.inputs.open(&PendingInput{
		RequestID: args.RequestID,
		Agent:     req.AgentName,
		Kind:      args.Kind,
		Prompt:    args.Prompt,
		Options:   args.Options,
		AskedAt:   time.Now(),
	})
	slog.Info("agent is waiting on a human answer",
		"worktree", req.AgentName, "kind", args.Kind, "request", args.RequestID,
		"prompt", firstLine(args.Prompt))
	return AgentIPCResponse{Success: true}
}

func (d *Daemon) handleIPCInputPoll(req AgentIPCRequest) AgentIPCResponse {
	var args ipcInputPollArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return AgentIPCResponse{Error: fmt.Sprintf("input_poll: bad args: %v", err)}
	}
	answer, tracked := d.inputs.consume(req.AgentName, args.RequestID)
	res := ipcInputPollResult{Resolved: answer != nil, Tracked: tracked, Answer: answer}
	if answer != nil {
		slog.Info("human answer delivered to agent",
			"worktree", req.AgentName, "request", args.RequestID,
			"option", answer.OptionID, "decline", answer.Decline)
	}
	data, _ := json.Marshal(res)
	return AgentIPCResponse{Success: true, Data: data}
}

func (d *Daemon) handleIPCInputClose(req AgentIPCRequest) AgentIPCResponse {
	var args ipcInputPollArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return AgentIPCResponse{Error: fmt.Sprintf("input_close: bad args: %v", err)}
	}
	d.inputs.close(req.AgentName, args.RequestID)
	return AgentIPCResponse{Success: true}
}

// --- control socket handlers (operator side) ---

// controlInputAnswerArgs is the agent_input_answer payload.
type controlInputAnswerArgs struct {
	RequestID string `json:"request_id,omitempty"`
	OptionID  string `json:"option_id,omitempty"`
	Text      string `json:"text,omitempty"`
	Decline   bool   `json:"decline,omitempty"`
}

// handleAgentInputGet returns the named agent's pending prompt, or every
// pending prompt when name is empty. The payload is always a LIST so the two
// shapes decode the same way.
func (d *Daemon) handleAgentInputGet(name string) DaemonControlResponse {
	maxAge := d.inputMaxAge()
	var pending []*PendingInput
	if name == "" {
		pending = d.inputs.list(maxAge)
	} else if p := d.inputs.get(name, maxAge); p != nil {
		pending = []*PendingInput{p}
	} else {
		pending = []*PendingInput{}
	}
	data, _ := json.Marshal(pending)
	return DaemonControlResponse{Success: true, Data: data}
}

func (d *Daemon) handleAgentInputAnswer(name string, rawArgs json.RawMessage) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}
	var args controlInputAnswerArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return DaemonControlResponse{Error: fmt.Sprintf("agent_input_answer: bad args: %v", err)}
		}
	}
	err := d.inputs.answer(name, args.RequestID, PendingInputAnswer{
		OptionID: args.OptionID,
		Text:     args.Text,
		Decline:  args.Decline,
	}, d.inputMaxAge())
	if err != nil {
		return DaemonControlResponse{Error: err.Error()}
	}
	slog.Info("pending input answered via control socket",
		"worktree", name, "option", args.OptionID, "decline", args.Decline)
	return DaemonControlResponse{Success: true}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
