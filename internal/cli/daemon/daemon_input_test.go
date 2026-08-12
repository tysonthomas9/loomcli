package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testPending(agent, reqID string) *PendingInput {
	return &PendingInput{
		RequestID: reqID,
		Agent:     agent,
		Kind:      "trust_prompt",
		Prompt:    "Do you trust the files in this folder?",
		Options: []PendingInputOption{
			{ID: "1", Label: "Yes, proceed"},
			{ID: "2", Label: "No, exit"},
		},
		AskedAt: time.Now(),
	}
}

const maxAge = time.Hour

// The whole answer path in one arc: the child opens, the operator sees it,
// answers it, and the child's next poll consumes it — after which the slot is
// gone from both sides.
func TestInputRegistry_OpenAnswerConsume(t *testing.T) {
	r := newInputRegistry()
	r.open(testPending("critic", "req-1"))

	if got := r.get("critic", maxAge); got == nil || got.Prompt == "" {
		t.Fatalf("get = %+v, want the pending prompt", got)
	}

	if err := r.answer("critic", "req-1", PendingInputAnswer{OptionID: "1"}, maxAge); err != nil {
		t.Fatalf("answer: %v", err)
	}

	answer, tracked := r.consume("critic", "req-1")
	if !tracked || answer == nil || answer.OptionID != "1" {
		t.Fatalf("consume = (%+v, %v), want the option-1 answer", answer, tracked)
	}
	if got := r.get("critic", maxAge); got != nil {
		t.Fatalf("slot survived consumption: %+v", got)
	}
}

func TestInputRegistry_AnswerValidation(t *testing.T) {
	r := newInputRegistry()

	if err := r.answer("critic", "", PendingInputAnswer{OptionID: "1"}, maxAge); err == nil {
		t.Fatal("answering an agent with no pending prompt must fail")
	}

	r.open(testPending("critic", "req-1"))
	if err := r.answer("critic", "req-1", PendingInputAnswer{OptionID: "9"}, maxAge); err == nil {
		t.Fatal("an option the prompt never offered must be rejected")
	}
	if err := r.answer("critic", "req-STALE", PendingInputAnswer{OptionID: "1"}, maxAge); err == nil {
		t.Fatal("answering a replaced request id must be rejected")
	}
	if err := r.answer("critic", "req-1", PendingInputAnswer{}, maxAge); err == nil {
		t.Fatal("an empty answer must be rejected")
	}
	if err := r.answer("critic", "req-1", PendingInputAnswer{OptionID: "1"}, maxAge); err != nil {
		t.Fatalf("a valid answer must land: %v", err)
	}
	if err := r.answer("critic", "req-1", PendingInputAnswer{OptionID: "2"}, maxAge); err == nil {
		t.Fatal("double-answering must be rejected")
	}
}

// A prompt older than the visibility bound is treated as absent for readers
// and answerers — but never deleted by them, because only the child retires
// its own slot.
func TestInputRegistry_AgedPromptInvisibleButNotDeleted(t *testing.T) {
	r := newInputRegistry()
	p := testPending("critic", "req-1")
	p.AskedAt = time.Now().Add(-2 * time.Hour)
	r.open(p)

	if got := r.get("critic", maxAge); got != nil {
		t.Fatalf("aged prompt visible: %+v", got)
	}
	if got := r.list(maxAge); len(got) != 0 {
		t.Fatalf("aged prompt listed: %+v", got)
	}
	if err := r.answer("critic", "req-1", PendingInputAnswer{OptionID: "1"}, maxAge); err == nil {
		t.Fatal("answering an aged prompt must fail")
	}
	// The slow child still owns its slot: poll says "keep waiting", not "gone".
	if answer, tracked := r.consume("critic", "req-1"); answer != nil || !tracked {
		t.Fatalf("consume = (%+v, %v), want unresolved-but-tracked", answer, tracked)
	}
}

// A replacing prompt must strand any answer aimed at its predecessor.
func TestInputRegistry_ReplacementStrandsTheOldRequest(t *testing.T) {
	r := newInputRegistry()
	r.open(testPending("critic", "req-1"))
	r.open(testPending("critic", "req-2"))

	if _, tracked := r.consume("critic", "req-1"); tracked {
		t.Fatal("the replaced request must read as no longer tracked")
	}
	if err := r.answer("critic", "req-1", PendingInputAnswer{OptionID: "1"}, maxAge); err == nil {
		t.Fatal("answering the replaced request id must fail")
	}
	if err := r.answer("critic", "req-2", PendingInputAnswer{OptionID: "1"}, maxAge); err != nil {
		t.Fatalf("the live request must accept the answer: %v", err)
	}
}

// The IPC + control handlers, end to end through their JSON shapes: the same
// arc as the registry test but over the wire structs the child and the
// operator actually send.
func TestDaemonInputHandlers_EndToEnd(t *testing.T) {
	d := &Daemon{inputs: newInputRegistry()}

	openArgs, _ := json.Marshal(ipcInputOpenArgs{
		RequestID: "req-1",
		Kind:      "trust_prompt",
		Prompt:    "Trust this folder?",
		Options:   []PendingInputOption{{ID: "1", Label: "Yes"}, {ID: "2", Label: "No"}},
	})
	if resp := d.handleIPCInputOpen(AgentIPCRequest{AgentName: "critic", Args: openArgs}); !resp.Success {
		t.Fatalf("input_open: %s", resp.Error)
	}

	// Operator lists all pending prompts (empty name = all).
	listResp := d.handleAgentInputGet("")
	if !listResp.Success {
		t.Fatalf("agent_input_get: %s", listResp.Error)
	}
	var pending []*PendingInput
	if err := json.Unmarshal(listResp.Data, &pending); err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v (err %v), want exactly the critic prompt", pending, err)
	}

	// First poll: unresolved, keep waiting.
	pollArgs, _ := json.Marshal(ipcInputPollArgs{RequestID: "req-1"})
	pollResp := d.handleIPCInputPoll(AgentIPCRequest{AgentName: "critic", Args: pollArgs})
	var poll ipcInputPollResult
	_ = json.Unmarshal(pollResp.Data, &poll)
	if poll.Resolved || !poll.Tracked {
		t.Fatalf("first poll = %+v, want unresolved-but-tracked", poll)
	}

	// Operator answers with a bad option first, then a good one.
	badArgs, _ := json.Marshal(controlInputAnswerArgs{OptionID: "9"})
	if resp := d.handleAgentInputAnswer("critic", badArgs); resp.Success {
		t.Fatal("an unoffered option must be rejected")
	}
	goodArgs, _ := json.Marshal(controlInputAnswerArgs{RequestID: "req-1", OptionID: "2"})
	if resp := d.handleAgentInputAnswer("critic", goodArgs); !resp.Success {
		t.Fatalf("agent_input_answer: %s", resp.Error)
	}

	// Second poll: delivered and consumed.
	pollResp = d.handleIPCInputPoll(AgentIPCRequest{AgentName: "critic", Args: pollArgs})
	_ = json.Unmarshal(pollResp.Data, &poll)
	if !poll.Resolved || poll.Answer == nil || poll.Answer.OptionID != "2" {
		t.Fatalf("second poll = %+v, want the option-2 answer", poll)
	}
	if resp := d.handleAgentInputGet("critic"); !resp.Success || !strings.Contains(string(resp.Data), "[]") {
		t.Fatalf("after consumption the agent must have no pending prompt, got %s", resp.Data)
	}
}
