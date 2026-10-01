package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestContract runs the adapter against the pinned b30c4d0 build in a /tmp
// sandbox (own HOME and XDG roots, so its own service registration and
// config) with a local fake OpenAI-compatible model, so no login, network or
// user data is touched. The sandbox user's own `opencode serve --service`
// runs first: Loom reuses it unchanged, then starts its own service once the
// user's stops, and leaves that one running at shutdown. It purges every
// session it opens. LOOM_REAL_OPENCODE=1 enables it; LOOM_OPENCODE_BIN
// overrides the binary.
func TestContract(t *testing.T) {
	bin := realOpenCode(t)
	model := newFakeModel(t)
	sbx := newSandbox(t, "loom-opencode-contract-", fakeModelConfig(model.URL))
	repo, moved := filepath.Join(sbx, "repo"), filepath.Join(sbx, "moved")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The sandbox user's own service: Loom must use it as it is and leave its
	// registration and config byte-for-byte unchanged.
	user := startService(t, bin, sbx, contractEnv(sbx))
	regFile, cfgFile := filepath.Join(sbx, "state/opencode/service.json"), filepath.Join(sbx, "config/opencode/service.json")
	before := readAll(t, []string{regFile, cfgFile})
	a := New(Config{Bin: bin, Env: contractEnv(sbx)})
	if !strings.HasPrefix(a.registrationFile(), sbx+"/") {
		t.Fatal("the adapter looks for a service outside the sandbox")
	}
	var owned []loomharness.NativeRef
	t.Cleanup(func() {
		if err := a.Purge(context.Background(), owned); err != nil {
			t.Errorf("cleanup purge: %v", err)
		}
		pid := serverPID(a)
		a.Stop()
		// Intended (Tyson, 18:00 UTC): Loom's shutdown leaves the service it
		// started running for the user's clients. The sandbox cleanup stops it.
		if pid == 0 || !alive(pid) {
			t.Errorf("Loom's shutdown stopped its service (pid %d); it must keep running", pid)
		}
	})

	h, err := a.Health(ctx)
	if err != nil || !h.OK || h.Version.Installed.String() != "2.0.19" {
		t.Fatalf("Health = %+v, %v", h, err)
	}
	// The model snapshot fills in after OpenCode's plugins settle.
	waitFor(t, "fake/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "fake/m") })
	t.Run("ReuseAndAuth", func(t *testing.T) {
		base, pw := a.endpoint()
		if serverPID(a) != user.PID || base != user.URL || pw != user.Password {
			t.Fatalf("Loom does not use the registered service: pid %d, want %d", serverPID(a), user.PID)
		}
		if err := NewClient(base, pw).call(ctx, "GET", "/api/session/active", nil, nil); err != nil {
			t.Fatalf("right password: %v", err)
		}
		if err := NewClient(base, "wrong").call(ctx, "GET", "/api/session/active", nil, nil); !isCode(err, "auth_failed") {
			t.Fatalf("wrong password = %v; want auth_failed", err)
		}
		resp, err := http.Get(base + "/api/session/active")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("no password = %d; want 401", resp.StatusCode)
		}
	})

	launch := loomharness.Launch{Root: sbx}
	feed, err := a.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	events := collect(feed)

	spec := loomharness.OpenSpec{
		Key: "agent-1", Launch: launch, Dir: repo, Model: "fake/m", Metadata: map[string]string{"loom_agent_id": "agent-1"},
		Rules: []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}},
	}
	ref, err := a.Open(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	owned = append(owned, ref)
	if again, err := a.Open(ctx, spec); err != nil || again != ref {
		t.Fatalf("repeat Open = %v, %v; want %v", again, err, ref)
	}
	s := a.Session(ref)
	sp := func(suffix string) string { return "/api/session/" + ref.NativeID + suffix }

	t.Run("PromptRetry", func(t *testing.T) {
		key := PromptID("agent-1", "req-1")
		for range 2 {
			if err := s.Prompt(ctx, loomharness.Input{Key: key, Text: "hello"}); err != nil {
				t.Fatal(err)
			}
		}
		events.wait(t, "turn completed", func(e loomharness.Event) bool {
			return e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "completed"
		})
		// 1.6d: dispatch matches Root plus NativeID, and binds a turn by the
		// InputKey of its turn.started.
		events.wait(t, "turn.started for the prompt", func(e loomharness.Event) bool {
			return e.Session == ref && e.Type == loomharness.EventTurnStarted && e.InputKey == key
		})
		events.mu.Lock()
		for _, e := range events.events {
			if e.Session.NativeID == ref.NativeID && e.Session.Root != sbx {
				t.Errorf("%s event has Root %q; want %q", e.Type, e.Session.Root, sbx)
			}
		}
		events.mu.Unlock()
		if got, err := s.HasInput(ctx, key); err != nil || got != loomharness.LandedFound {
			t.Fatalf("HasInput = %v, %v", got, err)
		}
		if got, err := s.HasInput(ctx, PromptID("agent-1", "never")); err != nil || got != loomharness.LandedNotFound {
			t.Fatalf("HasInput(absent) = %v, %v", got, err)
		}
		delivered := 0
		for _, e := range allEvents(t, s, 100) {
			if e.Type == loomharness.EventMessageDelivered && e.InputKey == key {
				delivered++
			}
		}
		if delivered != 1 || model.requests("hello") != 1 {
			t.Fatalf("retry: %d delivered messages, %d model turns; want 1 and 1", delivered, model.requests("hello"))
		}
	})

	t.Run("Paging", func(t *testing.T) {
		whole := allEvents(t, s, 100)
		paged := allEvents(t, s, 1)
		if len(whole) < 3 || len(paged) != len(whole) {
			t.Fatalf("paged %d events, whole %d", len(paged), len(whole))
		}
		for i := range whole {
			if paged[i].ItemID != whole[i].ItemID || paged[i].Type != whole[i].Type {
				t.Fatalf("event %d: paged %+v, whole %+v", i, paged[i], whole[i])
			}
		}
	})

	t.Run("Interrupt", func(t *testing.T) {
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "req-2"), Text: "SLOW please"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "running turn", func() bool { st, err := s.Status(ctx); return err == nil && st.Running && model.requests("SLOW") > 0 })
		if ok, err := s.Interrupt(ctx); err != nil || !ok {
			t.Fatalf("Interrupt = %v, %v", ok, err)
		}
		events.wait(t, "cancelled turn", func(e loomharness.Event) bool {
			return e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "cancelled"
		})
		if st, err := s.Status(ctx); err != nil || st.Running || !st.LastTurnInterrupt || st.TurnID != "" {
			t.Fatalf("Status after interrupt = %+v, %v; want idle, LastTurnInterrupt, no TurnID", st, err)
		}
		if ok, err := s.Interrupt(ctx); err != nil || ok {
			t.Fatalf("idle Interrupt = %v, %v; want false", ok, err)
		}
	})

	t.Run("PermissionReply", func(t *testing.T) {
		done := make(chan error, 1)
		go func() {
			done <- a.call(ctx, "POST", sp("/permission"), map[string]any{"action": "shell", "resources": []string{"echo hi"}}, nil)
		}()
		var askID string
		waitFor(t, "pending permission", func() bool {
			var l struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if a.call(ctx, "GET", sp("/permission"), nil, &l) == nil && len(l.Data) == 1 {
				askID = l.Data[0].ID
			}
			return askID != ""
		})
		if err := s.Reply(ctx, askID, loomharness.Reply{Allow: true}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("permission ask: %v", err)
		}
		if err := s.Reply(ctx, askID, loomharness.Reply{Allow: true}); !isCode(err, "ask_missing") {
			t.Fatalf("second Reply = %v; want ask_missing", err)
		}
	})

	t.Run("QuestionReply", func(t *testing.T) {
		var form struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		body := map[string]any{"title": "Which color?", "fields": []map[string]string{{"key": "color", "type": "string"}}}
		if err := a.call(ctx, "POST", sp("/form"), body, &form); err != nil {
			t.Fatal(err)
		}
		events.wait(t, "form ask.opened", func(e loomharness.Event) bool {
			return e.Type == loomharness.EventAskOpened && e.AskID == form.Data.ID && e.Session.NativeID == ref.NativeID
		})
		if err := s.Reply(ctx, form.Data.ID, loomharness.Reply{Answer: "blue"}); err != nil {
			t.Fatal(err)
		}
		events.wait(t, "form ask.resolved", func(e loomharness.Event) bool {
			return e.Type == loomharness.EventAskResolved && e.AskID == form.Data.ID
		})
		var got struct {
			Data struct {
				State struct {
					Status string `json:"status"`
				} `json:"state"`
			} `json:"data"`
		}
		if err := a.call(ctx, "GET", sp("/form/"+form.Data.ID), nil, &got); err != nil || got.Data.State.Status == "pending" {
			t.Fatalf("form after Reply = %+v, %v", got, err)
		}
		if err := s.Reply(ctx, form.Data.ID, loomharness.Reply{Answer: "red"}); !isCode(err, "ask_missing") {
			t.Fatalf("second Reply = %v; want ask_missing", err)
		}
		if err := s.Reply(ctx, "frm_missing", loomharness.Reply{Answer: "red"}); !isCode(err, "ask_missing") {
			t.Fatalf("Reply(missing form) = %v; want ask_missing", err)
		}
	})

	t.Run("SetModelAndMove", func(t *testing.T) {
		if err := s.SetModel(ctx, "fake/m2"); err != nil {
			t.Fatal(err)
		}
		if err := s.Move(ctx, moved); err != nil {
			t.Fatal(err)
		}
		info := sessionInfo(t, a, ref)
		if info.Model.ID != "m2" || info.Location.Directory != moved {
			t.Fatalf("session = %+v; want model m2 in %s", info, moved)
		}
	})

	t.Run("RestartKeepsUserService", func(t *testing.T) {
		if err := a.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		if !alive(user.PID) || serverPID(a) != user.PID {
			t.Fatalf("Restart touched the user's service: alive %v, in use %d", alive(user.PID), serverPID(a))
		}
		if got, err := s.Resume(ctx, spec.Launch, spec.Rules); err != nil || got != ref {
			t.Fatalf("Resume = %v, %v; want %v", got, err, ref)
		}
		if after := readAll(t, []string{regFile, cfgFile}); !maps.Equal(before, after) {
			t.Fatal("Loom changed the user's service registration or config")
		}
	})

	t.Run("StartsServiceWhenNoneRuns", func(t *testing.T) {
		// The user stops his service (the test owns it). Loom's next call
		// starts one with its filtered environment; the configured
		// password is used and the config is not rewritten.
		stopService(t, user.PID)
		if got, err := s.Resume(ctx, spec.Launch, spec.Rules); err != nil || got != ref {
			t.Fatalf("Resume = %v, %v; want %v", got, err, ref)
		}
		reg, ok := a.registered()
		if !ok || reg.PID == user.PID || serverPID(a) != reg.PID {
			t.Fatalf("Loom does not use the service it started: registered %+v, in use %d", reg.PID, serverPID(a))
		}
		if _, pw := a.endpoint(); pw != user.Password {
			t.Fatal("Loom's service does not use the configured password")
		}
		if after := readAll(t, []string{cfgFile}); after[cfgFile] != before[cfgFile] {
			t.Fatal("the service config changed although it had a password")
		}
	})

	t.Run("RestartResume", func(t *testing.T) {
		pid := serverPID(a)
		_, pw := a.endpoint()
		if err := a.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		if _, pw2 := a.endpoint(); serverPID(a) == pid || alive(pid) || pw2 != pw {
			t.Fatalf("restart of Loom's service: pid %d -> %d (old alive %v), same configured password %v", pid, serverPID(a), alive(pid), pw2 == pw)
		}
		got, err := s.Resume(ctx, spec.Launch, spec.Rules)
		if err != nil || got != ref {
			t.Fatalf("Resume = %v, %v; want %v", got, err, ref)
		}
		if landed, err := s.HasInput(ctx, PromptID("agent-1", "req-1")); err != nil || landed != loomharness.LandedFound {
			t.Fatalf("HasInput after restart = %v, %v", landed, err)
		}
		events.wait(t, "feed gap after restart", func(e loomharness.Event) bool { return e.Type == loomharness.EventFeedGap })
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "req-3"), Text: "after restart"}); err != nil {
			t.Fatal(err)
		}
		events.wait(t, "turn after restart", func(e loomharness.Event) bool {
			return e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "completed" && model.requests("after restart") == 1
		})
		if st, err := s.Status(ctx); err != nil || st.LastTurnInterrupt {
			t.Fatalf("Status after a completed turn = %+v, %v; want LastTurnInterrupt false", st, err)
		}
	})

	t.Run("PolicyMeaning", func(t *testing.T) {
		// Loom-vocabulary rules as 1.5a compiles them (loomharness cannot
		// import loomagent), judged by the pinned OpenCode permission matcher
		// through POST /permission, which evaluates without a model call.
		allowAll := []loomharness.PermissionRule{{Action: "read", Resource: "*", Effect: "allow"},
			{Action: "edit", Resource: "*", Effect: "allow"}, {Action: "bash", Resource: "*", Effect: "allow"}}
		publishDenies := []loomharness.PermissionRule{{Action: "bash", Resource: "gh *", Effect: "deny"},
			{Action: "bash", Resource: "git push*", Effect: "deny"}}
		readOnly := []loomharness.PermissionRule{{Action: "edit", Resource: "*", Effect: "deny"}, {Action: "bash", Resource: "*", Effect: "deny"}}
		reviewer := []loomharness.PermissionRule{{Action: "*", Resource: "*", Effect: "deny"},
			{Action: "read", Resource: "*", Effect: "allow"}, {Action: "bash", Resource: "*", Effect: "ask"}}
		for _, c := range []struct {
			name  string
			rules []loomharness.PermissionRule
			want  map[[2]string]string // {native action, resource} -> effect
		}{
			{"lead", slices.Concat(allowAll, publishDenies), map[[2]string]string{
				{"shell", "gh pr view 1"}: "deny", {"shell", "gh"}: "deny", {"shell", "git push origin main"}: "deny",
				{"shell", "ls -la"}: "allow", {"shell", "git status"}: "allow", {"edit", "main.go"}: "allow",
				{"read", "main.go"}: "allow", {"grep", "TODO"}: "allow",
			}},
			{"read_only", slices.Concat(allowAll, readOnly, publishDenies), map[[2]string]string{
				{"shell", "ls -la"}: "deny", {"edit", "main.go"}: "deny", {"read", "main.go"}: "allow", {"glob", "*.go"}: "allow",
			}},
			{"reviewer", slices.Concat(reviewer, publishDenies), map[[2]string]string{
				{"shell", "ls -la"}: "ask", {"shell", "gh pr view 1"}: "deny", {"edit", "main.go"}: "deny",
				{"read", "main.go"}: "allow", {"webfetch", "https://example.com"}: "deny",
			}},
			{"last_match_wins", []loomharness.PermissionRule{{Action: "bash", Resource: "gh *", Effect: "deny"},
				{Action: "bash", Resource: "*", Effect: "allow"}}, map[[2]string]string{{"shell", "gh pr view 1"}: "allow"}},
		} {
			ref, err := a.Open(ctx, loomharness.OpenSpec{Key: "policy-" + c.name, Launch: spec.Launch, Dir: repo, Rules: c.rules})
			if err != nil {
				t.Fatal(err)
			}
			owned = append(owned, ref)
			for k, want := range c.want {
				var r struct {
					Data struct {
						ID     string `json:"id"`
						Effect string `json:"effect"`
					} `json:"data"`
				}
				path := "/api/session/" + ref.NativeID + "/permission"
				if err := a.call(ctx, "POST", path, map[string]any{"action": k[0], "resources": []string{k[1]}}, &r); err != nil {
					t.Fatal(err)
				}
				if r.Data.Effect == "ask" {
					_ = a.Session(ref).Reply(ctx, r.Data.ID, loomharness.Reply{})
				}
				if r.Data.Effect != want {
					t.Errorf("%s: %s %q = %s; want %s", c.name, k[0], k[1], r.Data.Effect, want)
				}
			}
		}
		if _, err := a.Open(ctx, loomharness.OpenSpec{Key: "policy-unmappable", Launch: spec.Launch, Dir: repo,
			Rules: []loomharness.PermissionRule{{Action: "agent_create", Resource: "*", Effect: "deny"}}}); !isCode(err, "bad_request") {
			t.Fatalf("unmappable rule Open = %v; want bad_request", err)
		}
		if _, err := a.Session(loomharness.NativeRef{NativeID: SessionID("policy-unmappable")}).Resume(ctx, spec.Launch, spec.Rules); !isCode(err, "session_missing") {
			t.Fatalf("unmappable rule created a session: %v", err)
		}
	})

	t.Run("StartFailureAndCancel", func(t *testing.T) {
		// Fresh sandbox users, so no service is registered for them.
		other := newSandbox(t, "loom-opencode-start-", fakeModelConfig(model.URL))
		before := loomServes(t, bin)
		b := New(Config{Bin: bin, Env: contractEnv(other)})
		t.Cleanup(b.Stop)
		short, stop := context.WithCancel(ctx)
		defer stop()
		go func() { // cancel once the new service process exists, mid-start
			for len(loomServes(t, bin)) == len(before) && short.Err() == nil {
				time.Sleep(5 * time.Millisecond)
			}
			stop()
		}()
		if _, err := b.Models(short); !errors.Is(err, context.Canceled) {
			t.Fatalf("Models with a cancelled start = %v; want context.Canceled", err)
		}
		// The started service is never reaped: it registers, and the next
		// call uses it instead of starting another.
		waitFor(t, "the cancelled start's service", func() bool { _, err := b.Models(ctx); return err == nil })
		reg, ok := b.registered()
		if !ok || serverPID(b) != reg.PID {
			t.Fatalf("Loom does not use the service its cancelled start left: %v, in use %d", ok, serverPID(b))
		}
		if started := slices.DeleteFunc(loomServes(t, bin), func(p int) bool { return slices.Contains(before, p) }); !slices.Equal(started, []int{reg.PID}) {
			t.Fatalf("a cancelled start left services %v; want only %d", started, reg.PID)
		}

		// A data root that is a file makes serve exit during start.
		bad := newSandbox(t, "loom-opencode-fail-", fakeModelConfig(model.URL))
		notDir := filepath.Join(bad, "not-a-dir")
		if err := os.WriteFile(notDir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		c := New(Config{Bin: bin, Env: append(contractEnv(bad), "XDG_DATA_HOME="+notDir)})
		t.Cleanup(c.Stop)
		if _, err := c.Models(ctx); !errors.Is(err, loomharness.ErrUnavailable) {
			t.Fatalf("Models with a failed start = %v; want ErrUnavailable", err)
		}
		if _, ok := c.registered(); ok {
			t.Fatal("a failed start registered a service")
		}
	})

	t.Run("PurgeOwnedOnly", func(t *testing.T) {
		sibling, err := a.Open(ctx, loomharness.OpenSpec{Key: "sibling", Launch: spec.Launch, Dir: repo})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = a.Purge(context.Background(), []loomharness.NativeRef{sibling}) }()
		if err := a.Purge(ctx, owned); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Resume(ctx, spec.Launch, spec.Rules); !isCode(err, "session_missing") {
			t.Fatalf("Resume after Purge = %v; want session_missing", err)
		}
		if _, err := a.Session(sibling).Resume(ctx, spec.Launch, spec.Rules); err != nil {
			t.Fatalf("sibling after Purge: %v", err)
		}
		if err := a.Purge(ctx, owned); err != nil {
			t.Fatalf("repeat Purge: %v", err)
		}
	})
}

// TestServiceModeRunningTurnRecovery proves a running turn survives a crash
// of the service in shared service mode: the next service's boot sweep
// resumes it from the shared database (turn.resumed) and finishes it. First
// the sandbox user's own service crashes and Loom's next call starts one (a
// cross-owner resume); then the service Loom started crashes and Loom starts
// another by itself, with no Loom call. Accepted by Tyson (18:00 UTC); the
// resumed turn runs under the rules installed last until Loom's next Prompt
// (the boot-sweep exception, autonomous sweep only).
func TestServiceModeRunningTurnRecovery(t *testing.T) {
	bin := realOpenCode(t)
	model := newFakeModel(t)
	sbx := newSandbox(t, "loom-opencode-recovery-", fakeModelConfig(model.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	user := startService(t, bin, sbx, contractEnv(sbx))
	a := New(Config{Bin: bin, Env: contractEnv(sbx)})
	var owned []loomharness.NativeRef
	t.Cleanup(func() {
		if err := a.Purge(context.Background(), owned); err != nil {
			t.Errorf("cleanup purge: %v", err)
		}
		a.Stop()
	})
	waitFor(t, "fake/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "fake/m") })
	ref, err := a.Open(ctx, loomharness.OpenSpec{Key: "recover-1", Launch: loomharness.Launch{Root: sbx}, Dir: filepath.Join(sbx, "repo"),
		Model: "fake/m", Rules: []loomharness.PermissionRule{{Action: "*", Resource: "*", Effect: "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	owned = append(owned, ref)
	s := a.Session(ref)

	for i, c := range []struct {
		name  string
		crash func(t *testing.T) int // kills the service in use, returns its pid
		call  bool                   // Loom calls in after the crash
	}{
		{"UserServiceCrash", func(t *testing.T) int { _ = syscall.Kill(user.PID, syscall.SIGKILL); return user.PID }, true},
		{"LoomServiceCrash", func(t *testing.T) int {
			pid := serverPID(a)
			if pid == user.PID || pid == 0 {
				t.Fatalf("the service in use (%d) is not one Loom started", pid)
			}
			_ = syscall.Kill(pid, syscall.SIGKILL)
			return pid
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			text := fmt.Sprintf("SLOW crash %d", i)
			if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("recover-1", text), Text: text}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "running turn", func() bool { st, err := s.Status(ctx); return err == nil && st.Running && model.requests(text) > 0 })
			pid := c.crash(t)
			if c.call {
				waitWithin(t, time.Minute, "Loom's call after the crash", func() bool { _, err := s.Status(ctx); return err == nil })
			}
			// The boot sweep continues the turn with OpenCode's restart notice.
			waitWithin(t, time.Minute, "the resumed turn's model request", func() bool { return model.saw(restartNotice, i+1) > 0 })
			if p := serverPID(a); p == pid || p == 0 {
				t.Fatalf("service in use %d after the crash of %d", p, pid)
			}
			waitWithin(t, time.Minute, "the resumed turn to finish", func() bool { st, err := s.Status(ctx); return err == nil && !st.Running })
			n := 0
			for _, e := range allEvents(t, s, 200) {
				if e.Type == loomharness.EventTurnResumed {
					n++
				}
			}
			if n != i+1 {
				t.Fatalf("%d turn.resumed events; want %d", n, i+1)
			}
		})
	}
}

// restartNotice is the synthetic message OpenCode's boot sweep continues an
// interrupted turn with (core/src/session/execution/restart.ts).
const restartNotice = "The server restarted while you were working"

func realOpenCode(t *testing.T) string {
	t.Helper()
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	if bin := os.Getenv("LOOM_OPENCODE_BIN"); bin != "" {
		return bin
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
}

func fakeModelConfig(url string) string {
	return fmt.Sprintf(`{"provider":{"fake":{"name":"Fake","npm":"@ai-sdk/openai-compatible",
		"options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}},"m2":{"name":"M2","limit":{"context":100000,"output":4000}}}}},
		"model":"fake/m"}`, url+"/v1")
}

// newSandbox makes a /tmp sandbox for one OpenCode user: HOME, TMPDIR and XDG
// roots, a repo, opencodeJSON as the user config, and a service config with
// a free loopback port and no password, so a service started there never
// binds the default port (0xc0de) a real user's service uses. At cleanup it
// stops the service registered in the sandbox (Loom leaves the one it starts
// running) and removes the sandbox. Nothing outside the sandbox is touched.
func newSandbox(t *testing.T, prefix, opencodeJSON string) string {
	t.Helper()
	sbx, err := os.MkdirTemp("/tmp", prefix)
	if err == nil {
		// macOS /tmp is a symlink; OpenCode's file watcher reports resolved
		// paths, so preset reloads need the resolved root.
		sbx, err = filepath.EvalSymlinks(sbx)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var reg registration
		if b, err := os.ReadFile(filepath.Join(sbx, "state/opencode/service.json")); err == nil && json.Unmarshal(b, &reg) == nil && alive(reg.PID) {
			stopService(t, reg.PID)
		}
		_ = os.RemoveAll(sbx)
	})
	for _, d := range []string{filepath.Join(sbx, "home"), filepath.Join(sbx, "tmp"), filepath.Join(sbx, "config/opencode"), filepath.Join(sbx, "repo")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	for name, content := range map[string]string{"opencode.json": opencodeJSON, "service.json": fmt.Sprintf(`{"port":%d}`, port)} {
		if err := os.WriteFile(filepath.Join(sbx, "config/opencode", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return sbx
}

// startService runs `opencode serve --service` in the sandbox with env, as
// the sandbox user's own service, and returns its registration.
func startService(t *testing.T, bin, sbx string, env []string) registration {
	t.Helper()
	cmd := exec.Command(bin, "serve", "--service")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() {
		if alive(cmd.Process.Pid) {
			stopService(t, cmd.Process.Pid)
		}
	})
	var reg registration
	waitWithin(t, time.Minute, "--service registration", func() bool {
		b, err := os.ReadFile(filepath.Join(sbx, "state/opencode/service.json"))
		return err == nil && json.Unmarshal(b, &reg) == nil && reg.PID == cmd.Process.Pid &&
			answers(context.Background(), reg.URL, reg.Password, reg.PID)
	})
	return reg
}

// stopService stops a sandbox service: SIGTERM, then SIGKILL after 10s.
func stopService(t *testing.T, pid int) {
	t.Helper()
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for start := time.Now(); alive(pid) && time.Since(start) < 15*time.Second; time.Sleep(50 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func readAll(t *testing.T, files []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		out[f] = string(b)
	}
	return out
}

// loomServes lists running `serve --service` processes of bin. It only
// lists; tests never signal a process they did not start.
func loomServes(t *testing.T, bin string) []int {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), bin+" serve --service") {
			var pid int
			_, _ = fmt.Sscan(line, &pid)
			pids = append(pids, pid)
		}
	}
	slices.Sort(pids)
	return pids
}

// contractEnv is the sandbox environment a real test server starts from:
// sandboxEnv, no models.dev fetch, and extra (synthetic test secrets only).
func contractEnv(sbx string, extra ...string) []string {
	return append(append(sandboxEnv(sbx), "OPENCODE_DISABLE_MODELS_FETCH=1"), extra...)
}

// hostNames is all a test server takes from the host environment: what a
// shell needs to run, never a credential, token or provider key.
var hostNames = []string{"PATH", "SHELL", "LANG", "USER", "LOGNAME"}

// hostEnv selects hostNames from the host environment.
func hostEnv() []string {
	var env []string
	for _, k := range hostNames {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// sandboxEnv is an explicitly selected environment: hostEnv, plus
// OpenCode's HOME, TMPDIR and XDG roots under sbx. It fails closed (panics)
// unless sbx is an owned directory under /tmp, so no real test can run an
// OpenCode process against the user's own HOME or XDG data.
func sandboxEnv(sbx string) []string {
	if !ownedTmp(sbx) {
		panic("opencode real tests need an owned /tmp sandbox, got " + sbx)
	}
	return append(hostEnv(),
		"HOME="+filepath.Join(sbx, "home"),
		"TMPDIR="+filepath.Join(sbx, "tmp")+"/",
		"XDG_DATA_HOME="+filepath.Join(sbx, "data"),
		"XDG_CONFIG_HOME="+filepath.Join(sbx, "config"),
		"XDG_STATE_HOME="+filepath.Join(sbx, "state"),
		"XDG_CACHE_HOME="+filepath.Join(sbx, "cache"),
	)
}

func hasModel(models []loomharness.Model, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func allEvents(t *testing.T, s loomharness.Session, limit int) []loomharness.Event {
	t.Helper()
	var out []loomharness.Event
	for after := ""; ; {
		page, err := s.Messages(context.Background(), after, limit)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page.Events...)
		if page.Next == "" {
			return out
		}
		after = page.Next
	}
}

type sessionView struct {
	Model struct {
		ID string `json:"id"`
	} `json:"model"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

func sessionInfo(t *testing.T, a *Adapter, ref loomharness.NativeRef) sessionView {
	t.Helper()
	var r struct {
		Data sessionView `json:"data"`
	}
	if err := a.call(context.Background(), "GET", "/api/session/"+ref.NativeID, nil, &r); err != nil {
		t.Fatal(err)
	}
	return r.Data
}

// eventLog keeps every feed event for later waits.
type eventLog struct {
	mu     sync.Mutex
	events []loomharness.Event
}

func collect(f loomharness.Feed) *eventLog {
	l := &eventLog{}
	go func() {
		for e := range f.Events() {
			l.mu.Lock()
			l.events = append(l.events, e)
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *eventLog) wait(t *testing.T, what string, match func(loomharness.Event) bool) {
	t.Helper()
	waitFor(t, what, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, e := range l.events {
			if match(e) {
				return true
			}
		}
		return false
	})
}

// fakeModel is an OpenAI-compatible chat completions server. A turn whose
// last message contains SLOW streams one chunk and then holds until the
// request is cancelled, unless OpenCode's restart notice follows it (the
// boot sweep resumed it).
type fakeModel struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newFakeModel(t *testing.T) *fakeModel {
	m := &fakeModel{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &req)
		last := ""
		if n := len(req.Messages); n > 0 {
			last = string(req.Messages[n-1].Content)
		}
		m.mu.Lock()
		m.bodies = append(m.bodies, string(raw))
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta map[string]string, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "m",
				"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		chunk(map[string]string{"role": "assistant", "content": "working "}, nil)
		if strings.Contains(last, "SLOW") && !strings.Contains(string(raw[bytes.LastIndex(raw, []byte("SLOW")):]), restartNotice) {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Minute):
			}
			return
		}
		chunk(map[string]string{"content": "done"}, nil)
		chunk(map[string]string{}, "stop")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(m.Close)
	return m
}

// requests counts agent turns (not title requests) whose last message
// contains text.
func (m *fakeModel) requests(text string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.bodies {
		var req struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if strings.Contains(b, "You are a title generator") {
			continue
		}
		if json.Unmarshal([]byte(b), &req) == nil && len(req.Messages) > 0 && strings.Contains(string(req.Messages[len(req.Messages)-1].Content), text) {
			n++
		}
	}
	return n
}

func (m *fakeModel) sawSystem(text string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.bodies {
		if strings.Contains(b, text) {
			return true
		}
	}
	return false
}

// saw counts agent turns (not title requests) whose request contains text
// at least times times.
func (m *fakeModel) saw(text string, times int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.bodies {
		if !strings.Contains(b, "You are a title generator") && strings.Count(b, text) >= times {
			n++
		}
	}
	return n
}

// ownedTmp reports whether dir is a directory under /tmp owned by this user.
func ownedTmp(dir string) bool {
	clean := filepath.Clean(dir)
	if !strings.HasPrefix(clean, "/tmp/") && !strings.HasPrefix(clean, "/private/tmp/") {
		return false
	}
	fi, err := os.Stat(clean)
	if err != nil || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
