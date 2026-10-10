package loomagent

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// IdleAfter is how long an agent goes with no turn, no waiting message and
// no open ask before its harness memory is freed (design v2 §4.15).
var IdleAfter = 30 * time.Minute

// RunIdle is the one lifecycle timer for services that share harnesses: on
// each tick it runs IdleSweep over services() until ctx ends.
func RunIdle(ctx context.Context, tick <-chan time.Time, services func() []*Service) {
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick:
			IdleSweep(ctx, now, services())
		}
	}
}

// IdleSweep unloads every session this process loaded whose agent has been
// idle for IdleAfter at now, then restarts each harness on which it unloaded
// one and every agent, in every service, is idle that long. It holds the lock
// of every agent it looked at until it is done, so no turn starts meanwhile;
// an agent whose lock is taken counts as busy. The agents stay idle: the next
// Prompt or Respond resumes the session lazily.
func IdleSweep(ctx context.Context, now time.Time, services []*Service) {
	busy, unloaded := map[string]bool{}, map[string]loomharness.Harness{}
	var unlock []func()
	defer func() {
		for _, u := range unlock {
			u()
		}
	}()
	for _, s := range services {
		unlock = append(unlock, s.unloadIdle(ctx, now, busy, unloaded)...)
	}
	for name, h := range unloaded {
		if busy[name] {
			continue
		}
		if err := h.Restart(ctx); err != nil {
			slog.Warn("loomagent: idle restart", "harness", name, "error", err)
		}
		for _, s := range services {
			s.forgetResumed(name)
		}
	}
}

// unloadIdle is IdleSweep for s's agents; it returns the unlocks of the
// agent locks it took.
func (s *Service) unloadIdle(ctx context.Context, now time.Time, busy map[string]bool,
	unloaded map[string]loomharness.Harness) (unlock []func()) {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, IncludeArchived: true})
	if err != nil {
		slog.Warn("loomagent: idle sweep", "error", err)
		for name := range s.harnesses {
			busy[name] = true
		}
		return nil
	}
	for _, a := range agents {
		l := s.agentLock(a.AgentID)
		if !l.TryLock() {
			busy[a.Harness] = true
			continue
		}
		unlock = append(unlock, l.Unlock)
		cur, err := s.live(ctx, a.AgentID)
		if err != nil || !s.idleFor(ctx, cur, now) {
			busy[a.Harness] = true
			continue
		}
		a = cur
		sess, ref, err := s.current(ctx, a)
		s.mu.Lock()
		loaded := s.resumed[a.Harness][ref]
		s.mu.Unlock()
		if err != nil || sess == nil || !loaded {
			continue
		}
		if st, err := sess.Status(ctx); err != nil || st.Running {
			busy[a.Harness] = true // a harness sub-agent may still run
			continue
		}
		if err := sess.Unload(ctx); err != nil {
			busy[a.Harness] = true
			continue
		}
		s.mu.Lock()
		delete(s.resumed[a.Harness], ref)
		s.mu.Unlock()
		unloaded[a.Harness] = s.harnesses[a.Harness]
	}
	return unlock
}

// idleFor reports whether a has had no turn, waiting or handed message, open
// ask or state change for IdleAfter at now.
func (s *Service) idleFor(ctx context.Context, a loomstore.Agent, now time.Time) bool {
	if !slices.Contains([]string{StateIdle, StateFinished, StateArchived}, a.State) || a.RunningTurnID != nil ||
		len(s.openAsks(a.AgentID)) > 0 {
		return false
	}
	changed, err := time.Parse(time.RFC3339Nano, a.UpdatedAt)
	if err != nil || now.Sub(changed) < IdleAfter {
		return false
	}
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil {
		return false
	}
	return !slices.ContainsFunc(slots, func(sl loomstore.Slot) bool {
		return sl.State == loomstore.SlotWaiting || sl.State == loomstore.SlotHanded
	})
}
