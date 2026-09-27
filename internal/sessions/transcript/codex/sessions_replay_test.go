//go:build sessionsreplay

package codex

import "testing"

// #244: codex exec --json emits item.completed, not rollout response_item.
func TestSessionsReplay244ModernCodexMessage(t *testing.T) {
	data := []byte("{\"type\":\"thread.started\",\"thread_id\":\"thread-1\"}\n" +
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"item-1\",\"type\":\"agent_message\",\"text\":\"done\"}}\n")
	events, err := Events(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Text != "done" {
		t.Fatalf("#244: modern Codex message lost: %+v", events)
	}
}
