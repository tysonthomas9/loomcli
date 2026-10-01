package opencode

import (
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
// sandbox (own HOME and XDG roots) with a local fake OpenAI-compatible model,
// so no login, network or user data is touched. It purges every session it
// opens. LOOM_REAL_OPENCODE=1 enables it; LOOM_OPENCODE_BIN overrides the
// binary.
func TestContract(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	model := newFakeModel(t)
	sbx, err := os.MkdirTemp("/tmp", "loom-opencode-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sbx) })
	repo, moved := filepath.Join(sbx, "repo"), filepath.Join(sbx, "moved")
	for _, d := range []string{filepath.Join(sbx, "home"), filepath.Join(sbx, "tmp"), filepath.Join(sbx, "config/opencode"), repo, moved} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	userConfig := fmt.Sprintf(`{"provider":{"fake":{"name":"Fake","npm":"@ai-sdk/openai-compatible",
		"options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}},"m2":{"name":"M2","limit":{"context":100000,"output":4000}}}}},
		"model":"fake/m"}`, model.URL+"/v1")
	if err := os.WriteFile(filepath.Join(sbx, "config/opencode/opencode.json"), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// A separate --service instance in the same sandbox, started first: Loom's
	// plain server must run beside it and leave its registration and config
	// byte-for-byte unchanged.
	svcPW := startService(t, bin, sbx)
	registration := []string{filepath.Join(sbx, "state/opencode/service.json"), filepath.Join(sbx, "config/opencode/service.json")}
	before := readAll(t, registration)
	t.Cleanup(func() {
		if after := readAll(t, registration); !maps.Equal(before, after) {
			t.Errorf("Loom changed the OpenCode service registration or config:\nbefore %v\nafter  %v", before, after)
		}
	})
	a := New(Config{Bin: bin, Env: contractEnv(sbx), Presets: []loomharness.PresetConfig{{Name: "tester", Persona: "LOOM-PERSONA-MARKER"}}})
	var owned []loomharness.NativeRef
	t.Cleanup(func() {
		if err := a.Purge(context.Background(), owned); err != nil {
			t.Errorf("cleanup purge: %v", err)
		}
		pid, start := serverPID(a), time.Now()
		a.Stop()
		if took := time.Since(start); took >= stopGrace {
			t.Errorf("Stop took %s: the server ignored its stdin closing and was killed", took)
		}
		if alive(pid) || len(a.owned()) != 0 {
			t.Errorf("owned tree still running after Stop: server %v, owned %v", alive(pid), a.owned())
		}
	})

	h, err := a.Health(ctx)
	if err != nil || !h.OK || h.Version.Installed.String() != "2.0.19" {
		t.Fatalf("Health = %+v, %v", h, err)
	}
	// The model snapshot fills in after OpenCode's plugins settle.
	waitFor(t, "fake/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "fake/m") })
	t.Run("Auth", func(t *testing.T) {
		base, pw := a.endpoint()
		if pw == svcPW {
			t.Fatal("Loom's server reuses the service password")
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

	feed, err := a.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	events := collect(feed)

	spec := loomharness.OpenSpec{
		Key: "agent-1", Launch: loomharness.Launch{Root: sbx}, Preset: loomharness.PresetConfig{Name: "tester"},
		Dir: repo, Model: "fake/m", Metadata: map[string]string{"loom_agent_id": "agent-1"},
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
		if !model.sawSystem("LOOM-PERSONA-MARKER") {
			t.Fatal("the loom-tester preset persona never reached the model (preset merge)")
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

	t.Run("RestartResume", func(t *testing.T) {
		pid := serverPID(a)
		_, pw := a.endpoint()
		if err := a.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		if _, pw2 := a.endpoint(); serverPID(a) == pid || alive(pid) || pw2 == pw || len(pw2) < 40 {
			t.Fatalf("restart: pid %d -> %d (old alive %v), new per-boot password %v", pid, serverPID(a), alive(pid), pw2 != pw && len(pw2) >= 40)
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

	t.Run("OwnedTree", func(t *testing.T) {
		// OpenCode runs shell commands detached from the server's group, so a
		// crashed server leaves them behind; the restart must reap them.
		shellCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		go func() { _ = a.call(shellCtx, "POST", sp("/shell"), map[string]string{"command": "sleep 300"}, nil) }()
		pid := serverPID(a)
		var orphans []int
		waitFor(t, "detached shell command", func() bool {
			orphans = slices.DeleteFunc(a.owned(), func(p int) bool { return p == pid })
			return len(orphans) > 0
		})
		for _, o := range orphans {
			if g, err := syscall.Getpgid(o); err == nil && g == pid {
				t.Fatalf("shell command %d is in the server's group; the test no longer exercises a detached tree", o)
			}
		}
		time.Sleep(2 * trackEvery) // let track record the command
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "restart after crash", func() bool { p := serverPID(a); return p != 0 && p != pid })
		if n := liveCount(orphans); n != 0 {
			t.Fatalf("%d processes of the crashed tree survived the restart: %v", n, orphans)
		}
	})

	t.Run("CrashResume", func(t *testing.T) {
		key := PromptID("agent-1", "req-crash")
		if err := s.Prompt(ctx, loomharness.Input{Key: key, Text: "SLOW crash"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "running turn", func() bool {
			st, err := s.Status(ctx)
			return err == nil && st.Running && model.requests("SLOW crash") > 0
		})
		pid := serverPID(a)
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "restart after crash", func() bool { p := serverPID(a); return p != 0 && p != pid })
		if got, err := s.Resume(ctx, spec.Launch, spec.Rules); err != nil || got != ref {
			t.Fatalf("Resume after crash = %v, %v", got, err)
		}
		// R-D evidence: plain serve keeps the session but does not resume the
		// interrupted turn (OpenCode's crash recovery runs only in --service
		// mode). Pinned so a behavior change is noticed; Loom must not
		// auto-continue (Decision Agent).
		time.Sleep(8 * time.Second)
		if st, err := s.Status(ctx); err != nil || st.Running {
			t.Fatalf("Status after crash = %+v, %v; want not running", st, err)
		}
		if n := model.requests("SLOW crash"); n != 1 {
			t.Fatalf("model saw %d SLOW crash turns; plain serve now resumes, revisit R-D", n)
		}
		for _, e := range allEvents(t, s, 200) {
			if e.Type == loomharness.EventTurnResumed {
				t.Fatalf("plain serve emitted %s; revisit R-D", e.Type)
			}
		}
		_, _ = s.Interrupt(ctx)
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
		servers := func() []int { return loomServes(t, bin) }
		before := servers()
		b := New(Config{Bin: bin, Env: contractEnv(sbx)})
		t.Cleanup(b.Stop)
		short, stop := context.WithCancel(ctx)
		defer stop()
		go func() { // cancel once the new server process exists, mid-start
			for len(servers()) == len(before) && short.Err() == nil {
				time.Sleep(5 * time.Millisecond)
			}
			stop()
		}()
		if _, err := b.Models(short); !errors.Is(err, context.Canceled) {
			t.Fatalf("Models with a cancelled start = %v; want context.Canceled", err)
		}
		// A data root that is a file makes serve exit during start.
		notDir := filepath.Join(sbx, "not-a-dir")
		if err := os.WriteFile(notDir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		c := New(Config{Bin: bin, Env: append(contractEnv(sbx), "XDG_DATA_HOME="+notDir)})
		t.Cleanup(c.Stop)
		_, err := c.Models(ctx)
		if !errors.Is(err, loomharness.ErrUnavailable) {
			t.Fatalf("Models with a failed start = %v; want ErrUnavailable", err)
		}
		if after := servers(); !slices.Equal(after, before) {
			t.Fatalf("a cancelled or failed start left servers: before %v, after %v", before, after)
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

// startService runs `opencode serve --service` in the sandbox, as a user's
// own background service would, and returns its password. The test owns it
// and stops it at cleanup.
func startService(t *testing.T, bin, sbx string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	cmd := exec.Command(bin, "serve", "--service", "--hostname", "127.0.0.1", "--port", port)
	cmd.Env = contractEnv(sbx)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	var reg struct {
		PID      int    `json:"pid"`
		Password string `json:"password"`
	}
	waitFor(t, "--service registration", func() bool {
		b, err := os.ReadFile(filepath.Join(sbx, "state/opencode/service.json"))
		return err == nil && json.Unmarshal(b, &reg) == nil && reg.PID == cmd.Process.Pid &&
			answers(context.Background(), "http://127.0.0.1:"+port, reg.Password, reg.PID)
	})
	return reg.Password
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

// loomServes lists running `serve --stdio` processes of bin, as Loom starts them.
func loomServes(t *testing.T, bin string) []int {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, bin+" serve --stdio --hostname") {
			var pid int
			_, _ = fmt.Sscan(line, &pid)
			pids = append(pids, pid)
		}
	}
	slices.Sort(pids)
	return pids
}

// contractEnv is the sandbox environment: OpenCode's HOME, TMPDIR and XDG
// roots under sbx, no provider keys, and no models.dev fetch.
func contractEnv(sbx string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "OPENCODE_") || strings.HasPrefix(k, "XDG_") || strings.HasSuffix(k, "_API_KEY") || k == "HOME" || k == "TMPDIR" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+filepath.Join(sbx, "home"),
		"TMPDIR="+filepath.Join(sbx, "tmp")+"/",
		"XDG_DATA_HOME="+filepath.Join(sbx, "data"),
		"XDG_CONFIG_HOME="+filepath.Join(sbx, "config"),
		"XDG_STATE_HOME="+filepath.Join(sbx, "state"),
		"XDG_CACHE_HOME="+filepath.Join(sbx, "cache"),
		"OPENCODE_DISABLE_MODELS_FETCH=1",
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
// request is cancelled.
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
		if strings.Contains(last, "SLOW") {
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
