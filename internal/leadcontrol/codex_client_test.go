package leadcontrol

import (
	"encoding/json"
	"testing"
)

func TestCodexThreadWithTurnsUnmarshalAndPlainText(t *testing.T) {
	raw := []byte(`{
		"thread": {
			"id": "thread-1",
			"preview": "hello",
			"cwd": "/tmp/repo",
			"status": {"type": "idle"},
			"turns": [
				{
					"id": "turn-1",
					"startedAt": "2026-07-09T01:02:03Z",
					"completedAt": "2026-07-09T01:02:04Z",
					"durationMs": 1000,
					"status": "completed",
					"items": [
						{
							"type": "userMessage",
							"id": "item-user",
							"content": [
								{"type": "text", "text": "hello", "text_elements": []},
								{"type": "image", "text": "ignored"}
							]
						},
						{
							"type": "agentMessage",
							"id": "item-agent",
							"text": "hi there",
							"phase": "final_answer",
							"memoryCitation": null
						}
					],
					"itemsView": "expanded"
				}
			]
		}
	}`)
	var result struct {
		Thread CodexThread `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal thread/read result: %v", err)
	}
	thread := result.Thread
	if thread.ID != "thread-1" || len(thread.Turns) != 1 || len(thread.Turns[0].Items) != 2 {
		t.Fatalf("thread = %+v, want one turn with two items", thread)
	}
	user := thread.Turns[0].Items[0]
	if got := user.PlainText(); got != "hello" {
		t.Fatalf("user PlainText() = %q, want hello", got)
	}
	agent := thread.Turns[0].Items[1]
	if got := agent.PlainText(); got != "hi there" {
		t.Fatalf("agent PlainText() = %q, want hi there", got)
	}
	if agent.Phase != "final_answer" {
		t.Fatalf("agent phase = %q, want final_answer", agent.Phase)
	}
	if got := (CodexTurnItem{Type: "toolCall", Text: "ignored"}).PlainText(); got != "" {
		t.Fatalf("unknown PlainText() = %q, want empty", got)
	}
}
