package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
)

// Session returns the protocol methods for one recorded thread.
func (a *Adapter) Session(ref loomharness.NativeRef) *Session { return &Session{a: a, ref: ref} }

// Session is one codex thread on its recorded root's app-server. It holds
// no agent state: the running turn is read from codex.
type Session struct {
	a   *Adapter
	ref loomharness.NativeRef
}

// call runs method on the recorded root's app-server; it never falls back
// to another root. A thread codex cannot read is ErrSessionNotFound.
func (s *Session) call(ctx context.Context, method string, params, result any) error {
	if s.ref.Root == "" {
		return fmt.Errorf("codex: thread %s has no recorded root", s.ref.NativeID)
	}
	conn, err := s.a.Conn(ctx, s.ref.Root)
	if err != nil {
		return err
	}
	err = conn.Call(ctx, method, params, result)
	var rpc *RPCError
	if errors.As(err, &rpc) && strings.HasPrefix(rpc.Message, "thread not loaded") {
		return fmt.Errorf("codex %s %s: %w: %w", method, s.ref.NativeID, loomharness.ErrSessionNotFound, err)
	}
	return err
}

// Prompt starts a turn with in.Text, keyed in.Key (codex keeps it as the
// user item's clientId). codex would steer an active turn with it, so a
// running thread is ErrBusy.
func (s *Session) Prompt(ctx context.Context, in loomharness.Input) error {
	running, err := s.running(ctx)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("codex thread %s: %w", s.ref.NativeID, loomharness.ErrBusy)
	}
	text, _ := json.Marshal(map[string]string{"type": "text", "text": in.Text})
	return s.call(ctx, "turn/start", protocol.TurnStartParams{
		ThreadId: s.ref.NativeID, Input: []protocol.UserInput{text}, ClientUserMessageId: &in.Key,
	}, nil)
}

// Interrupt stops the running turn; false means none was running.
func (s *Session) Interrupt(ctx context.Context) (bool, error) {
	st, err := s.Status(ctx)
	if err != nil || st.TurnID == "" {
		return false, err
	}
	return true, s.call(ctx, "turn/interrupt", protocol.TurnInterruptParams{ThreadId: s.ref.NativeID, TurnId: st.TurnID}, nil)
}

// Status reports the thread's run state, its in-progress turn and whether
// its newest finished turn was interrupted.
func (s *Session) Status(ctx context.Context) (loomharness.Status, error) {
	running, err := s.running(ctx)
	if err != nil {
		return loomharness.Status{}, err
	}
	st := loomharness.Status{Running: running}
	limit := int64(2) // an in-progress turn, then the newest finished one
	page, err := s.turns(ctx, protocol.ThreadTurnsListParams{Limit: &limit, SortDirection: protocol.SortDirectionDesc, ItemsView: json.RawMessage(`"notLoaded"`)})
	if err != nil {
		return loomharness.Status{}, err
	}
	for _, t := range page.Data {
		if t.Status == protocol.TurnStatusInProgress {
			st.TurnID = t.Id
			continue
		}
		st.LastTurnInterrupt = t.Status == protocol.TurnStatusInterrupted
		break
	}
	return st, nil
}

func (s *Session) running(ctx context.Context) (bool, error) {
	var r protocol.ThreadReadResponse
	if err := s.call(ctx, "thread/read", protocol.ThreadReadParams{ThreadId: s.ref.NativeID}, &r); err != nil {
		return false, err
	}
	var status struct {
		Type string `json:"type"` // notLoaded | idle | active | systemError
	}
	_ = json.Unmarshal(r.Thread.Status, &status)
	return status.Type == "active", nil
}

// HasInput looks for an input keyed key among the thread's user items.
func (s *Session) HasInput(ctx context.Context, key string) (loomharness.Landed, error) {
	params := history("")
	for {
		page, err := s.turns(ctx, params)
		if err != nil {
			return loomharness.LandedUnknown, err
		}
		for _, t := range page.Data {
			for _, raw := range t.Items {
				var it item
				if json.Unmarshal(raw, &it) == nil && it.Type == "userMessage" && it.ClientID == key {
					return loomharness.LandedFound, nil
				}
			}
		}
		if page.NextCursor == nil || len(page.Data) == 0 {
			return loomharness.LandedNotFound, nil
		}
		params.Cursor = page.NextCursor
	}
}

// Messages reads one page of history, oldest first, as the events the live
// feed gave for it, with the same ItemIDs. limit counts turns.
func (s *Session) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	params := history(after)
	if limit > 0 {
		n := int64(limit)
		params.Limit = &n
	}
	page, err := s.turns(ctx, params)
	if err != nil {
		return loomharness.MessagePage{}, err
	}
	var out loomharness.MessagePage
	for _, t := range page.Data {
		e := loomharness.Event{Session: s.ref, TurnID: t.Id}
		if t.StartedAt != nil {
			e.Time = time.Unix(*t.StartedAt, 0)
		}
		for _, raw := range t.Items {
			if ie, ok := itemEvent(e, raw, false, false); ok {
				out.Events = append(out.Events, ie)
			}
		}
		if t.Status != protocol.TurnStatusInProgress {
			done := e
			if t.CompletedAt != nil {
				done.Time = time.Unix(*t.CompletedAt, 0)
			}
			done.Type, done.StopReason = loomharness.EventTurnCompleted, stopReason(t.Status)
			out.Events = append(out.Events, done)
		}
	}
	if page.NextCursor != nil && len(page.Data) > 0 {
		out.Next = *page.NextCursor
	}
	return out, nil
}

// history reads every stored item, oldest first.
func history(after string) protocol.ThreadTurnsListParams {
	p := protocol.ThreadTurnsListParams{SortDirection: protocol.SortDirectionAsc, ItemsView: json.RawMessage(`"full"`)}
	if after != "" {
		p.Cursor = &after
	}
	return p
}

// turns lists the thread's turns. A thread with no user message yet has no
// rollout file and no turns; codex 0.157.1 refuses thread/turns/list for it
// with "is not materialized yet", or, once it is named, "missing source
// rollout" (probed; the thread's path names a file that does not exist).
func (s *Session) turns(ctx context.Context, p protocol.ThreadTurnsListParams) (protocol.ThreadTurnsListResponse, error) {
	p.ThreadId = s.ref.NativeID
	var r protocol.ThreadTurnsListResponse
	err := s.call(ctx, "thread/turns/list", p, &r)
	var rpc *RPCError
	if errors.As(err, &rpc) && (strings.Contains(rpc.Message, "is not materialized yet") || strings.HasSuffix(rpc.Message, ": missing source rollout")) {
		return protocol.ThreadTurnsListResponse{}, nil
	}
	return r, err
}
