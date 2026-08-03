package cli

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A scripted daemon behind the real socket transport: open is acknowledged,
// the first poll says keep waiting, the second delivers the answer. This is
// the child's whole conversation with the daemon, minus only the human.
func TestAwaitHumanAnswer_DeliversTheAnswer(t *testing.T) {
	dir := shortSocketDir(t)
	socketPath := filepath.Join(dir, "ipc.sock")

	var mu sync.Mutex
	polls := 0
	var openedID string
	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		mu.Lock()
		defer mu.Unlock()
		switch req.Operation {
		case IPCOpInputOpen:
			var args IPCInputOpenArgs
			_ = json.Unmarshal(req.Args, &args)
			openedID = args.RequestID
			if args.Prompt == "" || len(args.Options) != 2 {
				return AgentIPCResponse{Error: "prompt/options not carried"}
			}
			return AgentIPCResponse{Success: true}
		case IPCOpInputPoll:
			var args IPCInputPollArgs
			_ = json.Unmarshal(req.Args, &args)
			if args.RequestID != openedID {
				return AgentIPCResponse{Error: "poll for a request that was never opened"}
			}
			polls++
			res := IPCInputPollResult{Tracked: true}
			if polls >= 2 {
				res.Resolved = true
				res.Answer = &IPCInputAnswer{OptionID: "2"}
			}
			data, _ := json.Marshal(res)
			return AgentIPCResponse{Success: true, Data: data}
		default:
			return AgentIPCResponse{Error: "unexpected op " + req.Operation}
		}
	})

	oldInterval := inputPollInterval
	inputPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { inputPollInterval = oldInterval })

	client := NewAgentIPCClient(socketPath, "critic")
	answer, ok := awaitHumanAnswerWithClient(client, HumanAnswerRequest{
		Kind:   "trust_prompt",
		Prompt: "Trust this folder?",
		Options: []IPCInputOption{
			{ID: "1", Label: "Yes"},
			{ID: "2", Label: "No"},
		},
	})
	if !ok || answer.OptionID != "2" {
		t.Fatalf("awaitHumanAnswerWithClient = (%+v, %v), want option 2", answer, ok)
	}
}

// An untracked poll result means the prompt was superseded or cleared — the
// child must stop waiting immediately rather than burn its whole budget.
func TestAwaitHumanAnswer_StopsWhenUntracked(t *testing.T) {
	dir := shortSocketDir(t)
	socketPath := filepath.Join(dir, "ipc.sock")

	startTestIPCServer(t, socketPath, func(req AgentIPCRequest) AgentIPCResponse {
		switch req.Operation {
		case IPCOpInputOpen:
			return AgentIPCResponse{Success: true}
		case IPCOpInputPoll:
			data, _ := json.Marshal(IPCInputPollResult{Tracked: false})
			return AgentIPCResponse{Success: true, Data: data}
		default:
			return AgentIPCResponse{Error: "unexpected op"}
		}
	})

	oldInterval := inputPollInterval
	inputPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { inputPollInterval = oldInterval })

	client := NewAgentIPCClient(socketPath, "critic")
	done := make(chan bool, 1)
	go func() {
		_, ok := awaitHumanAnswerWithClient(client, HumanAnswerRequest{Kind: "trust_prompt", Prompt: "p"})
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("an untracked prompt must not produce an answer")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("await did not stop on an untracked prompt")
	}
}
