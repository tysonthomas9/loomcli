package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// The child half of the human answer path.
//
// The role input policy's "ask" disposition means "hand this prompt to a
// person". The watchdog side of that wait already works (BeginDaemonInputWait
// suspends the idle kill), but until now the question itself never left the
// child process, so ask degraded to deny. AwaitHumanAnswer is the missing
// hop: publish the prompt to the daemon's pending-input registry, poll for an
// operator's decision, and give up cleanly at the deadline.
//
// The transport is the existing agent IPC socket. Polling, not a blocking
// server call: each IPC exchange is one short-lived connection by design, the
// wait is human-scale (seconds to minutes), and a 2s poll adds at most one
// human-imperceptible beat of latency while keeping the daemon free of
// long-held per-agent connections.

// IPC operation names for the pending-input flow. Wire contract with the
// daemon package's ipcOpInput* constants.
const (
	IPCOpInputOpen  = "input_open"
	IPCOpInputPoll  = "input_poll"
	IPCOpInputClose = "input_close"
)

// IPCInputOpenArgs mirrors the daemon's ipcInputOpenArgs.
type IPCInputOpenArgs struct {
	RequestID string           `json:"request_id"`
	Kind      string           `json:"kind"`
	Prompt    string           `json:"prompt"`
	Options   []IPCInputOption `json:"options,omitempty"`
}

// IPCInputOption mirrors the daemon's PendingInputOption.
type IPCInputOption struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// IPCInputPollArgs mirrors the daemon's ipcInputPollArgs.
type IPCInputPollArgs struct {
	RequestID string `json:"request_id"`
}

// IPCInputPollResult mirrors the daemon's ipcInputPollResult.
type IPCInputPollResult struct {
	Resolved bool            `json:"resolved"`
	Tracked  bool            `json:"tracked"`
	Answer   *IPCInputAnswer `json:"answer,omitempty"`
}

// IPCInputAnswer mirrors the daemon's PendingInputAnswer.
type IPCInputAnswer struct {
	OptionID string `json:"option_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Decline  bool   `json:"decline,omitempty"`
}

// HumanAnswerRequest is what the caller wants a person to decide.
type HumanAnswerRequest struct {
	Kind    string
	Prompt  string
	Options []IPCInputOption
}

// envInputWaitMaxSeconds is exported by the daemon into every agent child so
// both sides agree on how long a prompt may wait for a human. It mirrors the
// supervisor's LOOM_DAEMON_INPUT_WAIT_MAX_SECONDS bound; the child's deadline
// runs slightly SHORTER (see answerWaitBudget) so the wait always ends with a
// clean decline instead of the watchdog's kill.
const envInputWaitMaxSeconds = "LOOM_INPUT_WAIT_MAX_SECONDS"

const defaultAnswerWaitSeconds = 900

// answerWaitBudget is how long AwaitHumanAnswer polls before declining: the
// shared bound minus a grace beat, floored so a tiny configured bound still
// gives an operator a fighting chance to click.
func answerWaitBudget() time.Duration {
	maxWait := defaultAnswerWaitSeconds
	if v := os.Getenv(envInputWaitMaxSeconds); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxWait = n
		}
	}
	budget := time.Duration(maxWait)*time.Second - 30*time.Second
	if budget < 30*time.Second {
		budget = 30 * time.Second
	}
	return budget
}

// inputPollInterval is how often the child asks the daemon for a decision.
var inputPollInterval = 2 * time.Second

// AwaitHumanAnswer publishes the prompt to the daemon and waits for an
// operator's decision. It returns (answer, true) when a person decided, and
// (zero, false) when there is no daemon, the wait timed out, or the prompt was
// superseded — every false is accompanied by a stderr line naming the reason,
// because the caller will turn it into a deny and the operator reading the log
// deserves to know which kind of nobody-answered this was.
//
// The caller is expected to hold BeginDaemonInputWait around this (the
// input-policy callback already does), so the watchdog stays off for the
// duration.
func AwaitHumanAnswer(req HumanAnswerRequest) (IPCInputAnswer, bool) {
	client := agentIPCClientFromDefaultBackend()
	if client == nil {
		fmt.Fprintf(os.Stderr, "[loom] input_policy: kind %q is set to ask, but this run has no daemon to surface the prompt on; denying it\n", req.Kind)
		return IPCInputAnswer{}, false
	}
	return awaitHumanAnswerWithClient(client, req)
}

// awaitHumanAnswerWithClient is the daemon round trip against a concrete
// client; split from the lookup so tests can point it at a scripted daemon.
func awaitHumanAnswerWithClient(client *AgentIPCClient, req HumanAnswerRequest) (IPCInputAnswer, bool) {
	requestID := newInputRequestID()
	openArgs, _ := json.Marshal(IPCInputOpenArgs{
		RequestID: requestID,
		Kind:      req.Kind,
		Prompt:    req.Prompt,
		Options:   req.Options,
	})
	if err := client.inputOp(IPCOpInputOpen, openArgs); err != nil {
		fmt.Fprintf(os.Stderr, "[loom] input_policy: could not surface the %q prompt to the daemon (%v); declining\n", req.Kind, err)
		return IPCInputAnswer{}, false
	}

	budget := answerWaitBudget()
	deadline := time.Now().Add(budget)
	fmt.Fprintf(os.Stderr, "[loom] input_policy: waiting up to %s for a human answer (kind=%s) — `loom daemon answer` or the web UI resolves it\n",
		budget.Truncate(time.Second), req.Kind)

	pollArgs, _ := json.Marshal(IPCInputPollArgs{RequestID: requestID})
	for time.Now().Before(deadline) {
		time.Sleep(inputPollInterval)
		data, err := client.inputOpData(IPCOpInputPoll, pollArgs)
		if err != nil {
			// A transient IPC failure must not resolve the prompt; the daemon
			// bound is the backstop if the socket never comes back.
			slog.Debug("input poll failed", "err", err)
			continue
		}
		var res IPCInputPollResult
		if err := json.Unmarshal(data, &res); err != nil {
			slog.Debug("input poll returned malformed data", "err", err)
			continue
		}
		if res.Resolved && res.Answer != nil {
			return *res.Answer, true
		}
		if !res.Tracked {
			fmt.Fprintf(os.Stderr, "[loom] input_policy: the daemon no longer tracks the %q prompt (superseded or cleared); declining\n", req.Kind)
			return IPCInputAnswer{}, false
		}
	}

	_ = client.inputOp(IPCOpInputClose, pollArgs)
	fmt.Fprintf(os.Stderr, "[loom] input_policy: no human answered the %q prompt within %s; declining\n",
		req.Kind, budget.Truncate(time.Second))
	return IPCInputAnswer{}, false
}

func newInputRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
