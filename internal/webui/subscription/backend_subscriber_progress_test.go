package subscription

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestSubscriberDrainRejectsCursorStallsAndCycles(t *testing.T) {
	cases := []struct {
		name  string
		pages []backend.MutationPage
	}{
		{"empty", []backend.MutationPage{{HasMore: true}}},
		{"same", []backend.MutationPage{{Cursor: "start", HasMore: true}}},
		{"cycle", []backend.MutationPage{{Cursor: "next", HasMore: true}, {Cursor: "start", HasMore: true}}},
		{"terminal-cycle", []backend.MutationPage{{Cursor: "next", HasMore: true}, {Cursor: "start"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newScriptedCursorBackend()
			calls := 0
			b.getPageFn = func(context.Context, string, int) (backend.MutationPage, error) {
				if calls >= len(tc.pages) {
					t.Fatal("drain read beyond rejected page")
				}
				page := tc.pages[calls]
				calls++
				return page, nil
			}
			sub := newBackendMutationSubscriber(b, nil, "WS", "start", defaultSubscriberBudgets())
			defer sub.Stop()
			head, err := sub.drainToHead("start")
			if err == nil || !strings.Contains(err.Error(), "mutation pagination") {
				t.Fatalf("head=%q err=%v; want progress rejection", head, err)
			}
			if head != "" {
				t.Fatalf("failed drain returned trusted head %q", head)
			}
			if got := sub.Head(); got != "start" {
				t.Fatalf("failed drain moved subscriber head to %q", got)
			}
		})
	}
}

func TestSubscriberLiveRejectsBeforeCursorOrBroadcast(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()
	client := realtime.NewClient(1, 8, "start", nil, "WS")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.RegisterClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	sub := newBackendMutationSubscriber(newScriptedCursorBackend(), hub, "WS", "start", defaultSubscriberBudgets())
	defer sub.Stop()
	event := backend.MutationData{Cursor: "next", Type: "update", Timestamp: time.Now()}
	if _, err := sub.handlePage("start", backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "next", HasMore: true, Events: []backend.MutationData{event}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Send():
	case <-ctx.Done():
		t.Fatal("accepted mutation not delivered")
	}
	for _, cursor := range []string{"", "next", "start"} {
		event.Cursor = cursor
		if _, err := sub.handlePage("next", backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: cursor, HasMore: true, Events: []backend.MutationData{event}}); err == nil {
			t.Fatalf("accepted invalid live cursor %q", cursor)
		}
		if got := sub.Head(); got != "next" {
			t.Fatalf("invalid page advanced head to %q", got)
		}
	}
	// A valid page following rejected responses still resumes from the last
	// accepted head, without poisoning the cursor set with failed responses.
	event.Cursor = "terminal"
	if _, err := sub.handlePage("next", backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "terminal", Events: []backend.MutationData{event}}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-client.Send():
		if got.Cursor != "terminal" {
			t.Fatalf("rejected page broadcast cursor %q", got.Cursor)
		}
	case <-ctx.Done():
		t.Fatal("terminal mutation not delivered")
	}
	if sub.livePageCursors != nil {
		t.Fatal("finished burst retained cursor history")
	}
	// Cancellation removes the ordinary idle delay without changing semantics.
	sub.cancel()
	for _, cursor := range []string{"terminal", ""} {
		timeout, err := sub.handlePage("terminal", backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: cursor})
		if err != nil || timeout != int64(backendWaitTimeout/time.Millisecond) {
			t.Fatalf("idle page rejected: timeout=%d err=%v", timeout, err)
		}
		if sub.Head() != "terminal" {
			t.Fatal("idle page changed head")
		}
	}
}

func TestSubscriberLiveHistoryBoundDoesNotCapHealthyBurst(t *testing.T) {
	budgets := defaultSubscriberBudgets()
	budgets.maxDrainPages = 3
	sub := newBackendMutationSubscriber(newScriptedCursorBackend(), nil, "WS", "start", budgets)
	defer sub.Stop()
	previous := "start"
	for i := 0; i < 12; i++ {
		next := fmt.Sprintf("opaque-%d", i)
		if _, err := sub.handlePage(previous, backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: next, HasMore: true}); err != nil {
			t.Fatalf("healthy page%d rejected: %v", i, err)
		}
		if len(sub.livePageCursors) > 3 || len(sub.livePageOrder) > 3 {
			t.Fatal("live history exceeded bounded window")
		}
		previous = next
	}
	if _, err := sub.handlePage(previous, backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "opaque-10", HasMore: true}); err == nil {
		t.Fatal("recent cycle accepted")
	}
	if sub.Head() != previous {
		t.Fatal("rejected cycle moved head")
	}
}
