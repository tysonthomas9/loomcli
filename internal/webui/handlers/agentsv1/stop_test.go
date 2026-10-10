package agentsv1

import "testing"

// TestAgentSendInterruptRoute: POST /messages with "delivery":"interrupt" is
// Stop. It reports interrupted only for an interrupt, a retry by
// Idempotency-Key returns the first result, and an unknown delivery is 400.
func TestAgentSendInterruptRoute(t *testing.T) {
	srv := newServer(t, nil)

	status, out := call(t, srv, "POST", "ws/v1/agents/b1/messages", "q1", `{"text":"queued"}`)
	if status != 202 || out["interrupted"] != nil {
		t.Fatalf("queue send = %d %v; want no interrupted", status, out)
	}
	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "st1", `{"delivery":"interrupt"}`)
	if status != 202 || out["interrupted"] != true || out["message_id"] != "" || out["state"] != "active" {
		t.Fatalf("stop = %d %v", status, out)
	}
	literal(t, "stop", out, []string{"interrupted", "state"}, []string{"Interrupted"})
	if w := waiting(t, srv, "b1"); len(w) != 1 || w[0].(map[string]any)["text"] != "queued" {
		t.Fatalf("waiting after stop = %v; want the slot kept", w)
	}
	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "st1", `{"delivery":"interrupt"}`)
	if status != 202 || out["interrupted"] != true {
		t.Fatalf("stop retry = %d %v", status, out)
	}
	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "i1", `{"text":"instead","delivery":"interrupt"}`)
	if status != 202 || out["interrupted"] != true || out["state"] != "waiting" || out["replaced"] != true {
		t.Fatalf("interrupt with text = %d %v", status, out)
	}
	if w := waiting(t, srv, "b1"); len(w) != 1 || w[0].(map[string]any)["text"] != "instead" {
		t.Fatalf("waiting = %v; want the sender's slot replaced", w)
	}

	status, out = call(t, srv, "POST", "ws/v1/agents/a1/messages", "st2", `{"delivery":"interrupt"}`)
	if status != 202 || out["interrupted"] != false || out["state"] != "no_op" {
		t.Fatalf("idle stop = %d %v; want no_op, interrupted false", status, out)
	}
	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "bad", `{"text":"x","delivery":"steer"}`)
	want(t, "unknown delivery", status, out, 400, "preset_invalid")
}
