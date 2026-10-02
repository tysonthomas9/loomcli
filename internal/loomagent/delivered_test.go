package loomagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// deliveredText is the text of the one message.delivered row in es.
func deliveredText(t *testing.T, es []loomstore.Event) string {
	t.Helper()
	got := kinds(es, string(loomharness.EventMessageDelivered))
	if len(got) != 1 {
		t.Fatalf("message.delivered rows = %d; want 1", len(got))
	}
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(got[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Text
}

// TestDeliveredCarriesText: each harness's message.delivered, live or from
// its history, is saved and published with the delivered message's text
// from Loom's slot: OpenCode and Claude name only the input key, codex also
// sends its own copy of the text.
func TestDeliveredCarriesText(t *testing.T) {
	shapes := map[string]func(ref loomharness.NativeRef, key string) loomharness.Event{
		"opencode": func(ref loomharness.NativeRef, key string) loomharness.Event { // session.inbox.delivered
			return loomharness.Event{Type: loomharness.EventMessageDelivered, Session: ref, ItemKind: "message", ItemID: key, InputKey: key}
		},
		"claude": func(ref loomharness.NativeRef, key string) loomharness.Event { // user_message_uuids
			return loomharness.Event{Type: loomharness.EventMessageDelivered, Session: ref, ItemKind: "message", ItemID: key, InputKey: key}
		},
		"codex": func(ref loomharness.NativeRef, key string) loomharness.Event { // userMessage item
			return loomharness.Event{Type: loomharness.EventMessageDelivered, Session: ref, TurnID: "T1", ItemKind: "message",
				InputKey: key, Text: "first message"}
		},
	}
	for name, shape := range shapes {
		t.Run(name+"/live", func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			s := e.service(ServiceConfig{})
			a, ref := newLead(t, e, s, "alpha")
			mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first message", user))
			sub, err := s.Subscribe(ctx, SubscribeRequest{AgentIDs: []string{a.AgentID}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ingest(ctx, "opencode", shape(ref, defaultInputKey("", a.AgentID, "u1"))); err != nil {
				t.Fatal(err)
			}
			live := recv(t, sub, 1)
			if got := deliveredText(t, live); got != "first message" {
				t.Fatalf("live text = %q", got)
			}
			page, err := s.ListEvents(ctx, loomstore.EventQuery{AgentID: a.AgentID})
			if err != nil {
				t.Fatal(err)
			}
			if got := deliveredText(t, page.Events); got != "first message" {
				t.Fatalf("ListEvents text = %q", got)
			}
		})
		t.Run(name+"/history", func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			s := e.service(ServiceConfig{})
			a, ref := newLead(t, e, s, "alpha")
			mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first message", user))
			ev := shape(ref, defaultInputKey("", a.AgentID, "u1"))
			s.harnesses["opencode"] = tweaked{Harness: e.h, page1: []loomharness.Event{ev},
				page2: func() (loomharness.MessagePage, error) { return loomharness.MessagePage{}, nil }}
			if err := s.replay(ctx, "opencode", s.get(t, a.AgentID)); err != nil {
				t.Fatal(err)
			}
			if got := deliveredText(t, rows(t, s, a.AgentID, 0)); got != "first message" {
				t.Fatalf("replayed text = %q", got)
			}
		})
	}
}

// TestDeliveredWithoutSlotKeepsHarnessText: a delivery no slot holds (its
// slot moved on) keeps whatever text the harness sent, and a row saved with
// no text still reads.
func TestDeliveredWithoutSlotKeepsHarnessText(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s, "alpha")
	n := rows(t, s, a.AgentID, 0)
	for _, ev := range []loomharness.Event{
		{Type: loomharness.EventMessageDelivered, Session: ref, InputKey: "gone1"},
		{Type: loomharness.EventMessageDelivered, Session: ref, InputKey: "gone2", Text: "native"},
	} {
		if _, err := s.ingest(ctx, "opencode", ev); err != nil {
			t.Fatal(err)
		}
	}
	got := rows(t, s, a.AgentID, n[len(n)-1].Seq)
	if t1, t2 := deliveredText(t, got[:1]), deliveredText(t, got[1:]); t1 != "" || t2 != "native" {
		t.Fatalf("texts = %q, %q; want none and the harness's", t1, t2)
	}
}
