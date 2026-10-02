package loomagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// shaped is the fake harness registered as name, its message.delivered in
// that harness's port shape: OpenCode (session.inbox.delivered) and Claude
// (user_message_uuids) name only the input key, codex's userMessage item
// carries its own copy of the text.
type shaped struct {
	loomharness.Harness
	name string
}

func (h shaped) shape(e loomharness.Event) loomharness.Event {
	if e.Type != loomharness.EventMessageDelivered {
		return e
	}
	e.ItemKind = "message"
	if h.name != "codex" {
		e.ItemID, e.Text = e.InputKey, ""
	}
	return e
}

func (h shaped) Name() string { return h.name }

func (h shaped) Feed(ctx context.Context) (loomharness.Feed, error) {
	f, err := h.Harness.Feed(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan loomharness.Event, 64)
	go func() {
		defer close(out)
		for e := range f.Events() {
			out <- h.shape(e)
		}
	}()
	return shapedFeed{f, out}, nil
}

type shapedFeed struct {
	loomharness.Feed
	ch chan loomharness.Event
}

func (f shapedFeed) Events() <-chan loomharness.Event { return f.ch }

func (h shaped) Session(ref loomharness.NativeRef) loomharness.Session {
	return shapedSession{h.Harness.Session(ref), h}
}

type shapedSession struct {
	loomharness.Session
	h shaped
}

func (s shapedSession) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	p, err := s.Session.Messages(ctx, after, limit)
	for i := range p.Events {
		p.Events[i] = s.h.shape(p.Events[i])
	}
	return p, err
}

var harnessNames = []string{"opencode", "codex", "claude"}

// shapedService is a service whose only harness is the fake as name, with
// a lead agent on it.
func shapedService(t *testing.T, name string) (*Service, *createEnv, loomstore.Agent, loomharness.NativeRef) {
	t.Helper()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	s.harnesses = map[string]loomharness.Harness{name: shaped{e.h, name}}
	info, err := s.Create(context.Background(), CreateRequest{Envelope: Envelope{RequestID: "alpha"}, Preset: "lead",
		Name: "alpha", Repo: "/repo", Overrides: Overrides{Harness: name, Model: "fake-model"}})
	if err != nil {
		t.Fatal(err)
	}
	a := s.get(t, info.AgentID)
	return s, e, a, loomharness.NativeRef{Root: *a.HarnessSessionRoot, NativeID: *a.HarnessSessionID}
}

// deliveredTexts maps each message.delivered row in es by input key to its text.
func deliveredTexts(t *testing.T, es []loomstore.Event) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, r := range kinds(es, string(loomharness.EventMessageDelivered)) {
		var p struct {
			InputKey string `json:"inputKey"`
			Text     string `json:"text"`
		}
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out[p.InputKey] = p.Text
	}
	return out
}

func listed(t *testing.T, s *Service, agentID string) []loomstore.Event {
	t.Helper()
	p, err := s.ListEvents(context.Background(), loomstore.EventQuery{AgentID: agentID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return p.Events
}

// TestDeliveredCarriesTextLive: on each harness, the delivery its live feed
// reports is published and listed with the message's text.
func TestDeliveredCarriesTextLive(t *testing.T) {
	for _, name := range harnessNames {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, _, a, _ := shapedService(t, name)
			sub, err := s.Subscribe(ctx, SubscribeRequest{AgentIDs: []string{a.AgentID}})
			if err != nil {
				t.Fatal(err)
			}
			feedCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { defer close(done); s.RunFeed(feedCtx, name) }()
			defer func() { cancel(); <-done }()
			mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first message", user))
			key := s.inputKey(name, a.AgentID, "u1")
			var live []loomstore.Event
			for deliveredTexts(t, live)[key] == "" {
				live = append(live, recv(t, sub, 1)...)
			}
			if got := deliveredTexts(t, live)[key]; got != "first message" {
				t.Fatalf("live text = %q", got)
			}
			if got := deliveredTexts(t, listed(t, s, a.AgentID))[key]; got != "first message" {
				t.Fatalf("ListEvents text = %q", got)
			}
		})
	}
}

// TestDeliveredTextAfterSlotMovedOn: on each harness, a delivery reported
// late (live, or from the history) carries the text that was handed over,
// after the sender's slot was reused and another sender's waiting text was
// edited and handed over in its place; the edited-away draft never appears.
func TestDeliveredTextAfterSlotMovedOn(t *testing.T) {
	for _, name := range harnessNames {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, e, a, ref := shapedService(t, name)
			h := shaped{e.h, name}
			k1, k2 := s.inputKey(name, a.AgentID, "u1"), s.inputKey(name, a.AgentID, "c2")
			mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
			mustSendMsg(t, s, sendReq(a.AgentID, "c1", "draft", child))
			mustSendMsg(t, s, sendReq(a.AgentID, "c2", "second", child)) // edits the waiting text
			// k1's turn ends with its delivery missed: c2 is handed over next.
			if err := s.HarnessEvent(ctx, a.AgentID, loomharness.Event{Type: loomharness.EventTurnCompleted,
				Session: ref, TurnID: k1, StopReason: "completed"}); err != nil {
				t.Fatal(err)
			}
			mustSendMsg(t, s, sendReq(a.AgentID, "u2", "third", user)) // the user's slot is reused
			delivered := func(key, text string) loomharness.Event {
				return h.shape(loomharness.Event{Type: loomharness.EventMessageDelivered, Session: ref,
					InputKey: key, Text: text})
			}
			if _, err := s.ingest(ctx, name, delivered(k1, "first")); err != nil { // late, live
				t.Fatal(err)
			}
			s.harnesses[name] = tweaked{Harness: h, page1: []loomharness.Event{delivered(k1, "first"), delivered(k2, "second")},
				page2: func() (loomharness.MessagePage, error) { return loomharness.MessagePage{}, nil }}
			if err := s.replay(ctx, name, s.get(t, a.AgentID)); err != nil {
				t.Fatal(err)
			}
			got := deliveredTexts(t, listed(t, s, a.AgentID))
			if len(got) != 2 || got[k1] != "first" || got[k2] != "second" {
				t.Fatalf("delivered texts = %v; want %s first, %s second", got, k1, k2)
			}
		})
	}
}

// TestDeliveredUnknownKeyKeepsHarnessText: a delivery Loom has no record of
// (a legacy receipt) keeps whatever text the harness sent, and a row saved
// with no text still reads.
func TestDeliveredUnknownKeyKeepsHarnessText(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s, "alpha")
	for _, ev := range []loomharness.Event{
		{Type: loomharness.EventMessageDelivered, Session: ref, InputKey: "gone1"},
		{Type: loomharness.EventMessageDelivered, Session: ref, InputKey: "gone2", Text: "native"},
	} {
		if _, err := s.ingest(ctx, "opencode", ev); err != nil {
			t.Fatal(err)
		}
	}
	got := deliveredTexts(t, listed(t, s, a.AgentID))
	if t1, ok := got["gone1"]; !ok || t1 != "" || got["gone2"] != "native" {
		t.Fatalf("texts = %v; want gone1 none and gone2 the harness's", got)
	}
}
