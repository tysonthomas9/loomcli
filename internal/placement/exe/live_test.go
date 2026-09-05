package exe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// Live end-to-end test against the real exe.dev control plane and a real VM.
//
// It is env-gated rather than build-tagged, which means it COMPILES AND
// "PASSES" WHILE SKIPPING. A green `go test ./...` therefore says nothing about
// whether this ran -- the skip message names what is missing, and the run log
// is the only evidence it executed.
//
//	LOOM_EXE_LIVE=1 \
//	LOOM_EXE_TOKEN=$(cat path/to/token) \
//	LOOM_EXE_SSH_KEY_PATH=$HOME/.ssh/id_ed25519 \
//	go test ./internal/placement/exe/ -run TestLive -v -count=1 -timeout 20m
//
// It creates ONE small VM and deletes it in t.Cleanup even when the test
// fails, because a leaked VM bills until someone notices.
func liveProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	if os.Getenv("LOOM_EXE_LIVE") != "1" {
		t.Skip("live test: set LOOM_EXE_LIVE=1 (plus LOOM_EXE_TOKEN and LOOM_EXE_SSH_KEY_PATH) to run against real exe.dev")
	}
	token := strings.TrimSpace(os.Getenv("LOOM_EXE_TOKEN"))
	if token == "" {
		t.Fatal("LOOM_EXE_LIVE=1 but LOOM_EXE_TOKEN is empty")
	}
	keyPath := strings.TrimSpace(os.Getenv("LOOM_EXE_SSH_KEY_PATH"))
	if keyPath == "" {
		t.Fatal("LOOM_EXE_LIVE=1 but LOOM_EXE_SSH_KEY_PATH is empty (select an exe.dev-registered key explicitly)")
	}
	// A fresh host-key store per run, so trust-on-first-use is exercised as a
	// first use rather than reading a pin from an earlier run.
	hostKeys := filepath.Join(t.TempDir(), "known_hosts")

	provider, err := New(Config{
		Token:       token,
		SSHKeyPath:  keyPath,
		HostKeyPath: hostKeys,
		Image:       strings.TrimSpace(os.Getenv("LOOM_EXE_IMAGE")),
	})
	if err != nil {
		t.Fatalf("construct provider: %v", err)
	}
	return provider, hostKeys
}

// liveVMName is unique per run: a collision with a leftover VM would make the
// test either reuse someone else's machine or fail as a duplicate.
func liveVMName() string {
	return fmt.Sprintf("loom-live-%d", time.Now().UTC().Unix())
}

func TestLiveExeLeadLifecycle(t *testing.T) {
	provider, hostKeyPath := liveProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	name := liveVMName()
	labels := map[string]string{
		placement.PlacementLabelKey:   name,
		placement.EnvironmentLabelKey: "loom-live-test",
	}

	t.Logf("creating VM %q", name)
	created, err := provider.Create(ctx, placement.CreateRequest{
		Name:      name,
		AgentName: "live-lead",
		Labels:    labels,
		Resource:  placement.ResourceSize{VCPU: 2, MemGiB: 4},
	})
	// Cleanup is registered even on a failed create: Unknown means a VM MAY
	// exist, and that is exactly the case that leaks.
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer ccancel()
		if derr := provider.Delete(cctx, name); derr != nil && !errors.Is(derr, placement.ErrSandboxNotFound) {
			t.Errorf("LEAKED VM %q -- delete failed: %v", name, derr)
			return
		}
		t.Logf("deleted VM %q", name)
	})
	if err != nil {
		t.Fatalf("Create: %v (outcome=%q)", err, created.Outcome)
	}
	if created.Outcome != placement.CreateOutcomeCreated {
		t.Fatalf("Create outcome = %q, want %q", created.Outcome, placement.CreateOutcomeCreated)
	}
	if created.SandboxID != name {
		t.Fatalf("SandboxID = %q, want the VM name %q", created.SandboxID, name)
	}

	t.Run("Get is an authoritative point read", func(t *testing.T) {
		raw, err := provider.pointRead(ctx, name)
		if err != nil {
			t.Fatalf("pointRead: %v", err)
		}
		t.Logf("service SSH route: dest=%q host=%q user=%q", raw.SSHDest, raw.SSHHost, raw.SSHUser)
		got, err := provider.Get(ctx, name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != name {
			t.Errorf("ID = %q, want %q", got.ID, name)
		}
		if got.State != placement.ProviderSandboxRunning {
			t.Errorf("State = %q, want running (raw=%q)", got.State, got.RawState)
		}
		for k, want := range labels {
			if got.Labels[k] != want {
				t.Errorf("label %q = %q, want %q (tag round-trip lost it)", k, got.Labels[k], want)
			}
		}
	})

	t.Run("FindByName resolves the same VM", func(t *testing.T) {
		got, err := provider.FindByName(ctx, name)
		if err != nil {
			t.Fatalf("FindByName: %v", err)
		}
		if got.ID != name {
			t.Errorf("ID = %q, want %q", got.ID, name)
		}
	})

	t.Run("FindByName reports absence, not a false positive", func(t *testing.T) {
		_, err := provider.FindByName(ctx, name+"-does-not-exist")
		if !errors.Is(err, placement.ErrSandboxNotFound) {
			t.Fatalf("err = %v, want ErrSandboxNotFound", err)
		}
	})

	t.Run("ListManaged matches on labels", func(t *testing.T) {
		found, err := provider.ListManaged(ctx, map[string]string{
			placement.EnvironmentLabelKey: "loom-live-test",
		})
		if err != nil {
			t.Fatalf("ListManaged: %v", err)
		}
		var hit bool
		for _, sandbox := range found {
			if sandbox.ID == name {
				hit = true
			}
		}
		if !hit {
			t.Errorf("ListManaged did not return %q; got %d sandboxes", name, len(found))
		}
	})

	t.Run("EnsureRunning reports no resume", func(t *testing.T) {
		// exe.dev has no start operation, so a running VM must report false --
		// "nothing was resumed" -- rather than claiming a recovery.
		resumed, err := provider.EnsureRunning(ctx, name)
		if err != nil {
			t.Fatalf("EnsureRunning: %v", err)
		}
		if resumed {
			t.Error("EnsureRunning = true, but exe.dev cannot resume anything")
		}
	})

	t.Run("SetAutostopInterval fails loudly", func(t *testing.T) {
		if provider.SupportsParking() {
			t.Fatal("SupportsParking() = true, but exe.dev has no stop/start")
		}
		if err := provider.SetAutostopInterval(ctx, name, time.Minute); err == nil {
			t.Error("SetAutostopInterval succeeded; it must fail so a bypassed capability gate is caught")
		}
	})

	const secret = "live-secret-value-8fj2"
	t.Run("PrepareLeadBoot seeds files and clones", func(t *testing.T) {
		err := provider.PrepareLeadBoot(ctx, name, placement.LeadBootPrep{
			PromptPath: "/home/exedev/prompt.md",
			PromptText: "# live lead prompt\n",
			Files: []placement.SandboxFile{
				{Path: "/home/exedev/.loom-live/creds.json", Content: []byte(`{"token":"` + secret + `"}`), Mode: "600"},
			},
			Repo: &placement.RepoClone{
				Name:      "hello",
				RemoteURL: "https://github.com/octocat/Hello-World",
				Checkout:  "/home/exedev/hello",
			},
		})
		if err != nil {
			t.Fatalf("PrepareLeadBoot: %v", err)
		}
	})

	t.Run("seeded content and permissions are correct in the VM", func(t *testing.T) {
		client, err := provider.dial(ctx, name)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = client.Close() }()

		out, err := run(client, "cat /home/exedev/.loom-live/creds.json; stat -c %a /home/exedev/.loom-live/creds.json; "+
			"cat /home/exedev/prompt.md; test -d /home/exedev/hello/.git && echo CLONE-OK")
		if err != nil {
			t.Fatalf("verify prep: %v (%s)", err, out)
		}
		for _, want := range []string{secret, "600", "live lead prompt", "CLONE-OK"} {
			if !strings.Contains(out, want) {
				t.Errorf("prep output missing %q:\n%s", want, out)
			}
		}
		// No credential helper may survive the clone.
		leftover, _ := run(client, "ls /home/exedev/*.loom-askpass* 2>/dev/null | wc -l")
		if strings.TrimSpace(leftover) != "0" {
			t.Errorf("a credential helper survived the clone: %q", leftover)
		}
	})

	t.Run("PTY lifecycle", func(t *testing.T) {
		spec := placement.ProcessSpec{
			SessionID:  placement.LeadPTYSessionID,
			WorkingDir: "/home/exedev",
			Env:        map[string]string{"LOOM_LIVE_MARKER": "marker-ok"},
			Command:    []string{"bash", "-lc", "echo BOOT-OK; echo env=$LOOM_LIVE_MARKER; exec sleep 900"},
			TTY:        true,
		}
		if err := provider.CreatePty(ctx, name, spec); err != nil {
			t.Fatalf("CreatePty: %v", err)
		}

		if err := provider.CreatePty(ctx, name, spec); !errors.Is(err, placement.ErrPtySessionAlreadyExists) {
			// Idempotency is a success for the broker. Anything else means a
			// retried boot could start a SECOND lead in one sandbox.
			t.Errorf("duplicate CreatePty = %v, want ErrPtySessionAlreadyExists", err)
		}

		sessions, err := provider.ListPtySessions(ctx, name)
		if err != nil {
			t.Fatalf("ListPtySessions: %v", err)
		}
		var haveLead bool
		for _, s := range sessions {
			if s.SessionID == placement.LeadPTYSessionID {
				haveLead = true
			}
		}
		if !haveLead {
			t.Fatalf("lead session absent from %v", sessions)
		}

		t.Run("attach reads the running lead's output", func(t *testing.T) {
			attachment, err := provider.AttachPTY(ctx, name, placement.LeadPTYSessionID)
			if err != nil {
				t.Fatalf("AttachPTY: %v", err)
			}
			defer func() { _ = attachment.Close() }()

			var buf strings.Builder
			deadline := time.After(45 * time.Second)
			for !strings.Contains(buf.String(), "marker-ok") {
				select {
				case chunk, ok := <-attachment.Output():
					if !ok {
						t.Fatalf("upstream closed before the marker; read:\n%s", buf.String())
					}
					buf.Write(chunk)
				case <-deadline:
					t.Fatalf("timed out waiting for the lead's output; read:\n%s", buf.String())
				}
			}
			if !strings.Contains(buf.String(), "BOOT-OK") {
				t.Errorf("attach did not replay the session's earlier output:\n%s", buf.String())
			}
			if err := attachment.Resize(ctx, 100, 30); err != nil {
				t.Errorf("Resize: %v", err)
			}
		})

		t.Run("attach to an absent session is a typed absence", func(t *testing.T) {
			_, err := provider.AttachPTY(ctx, name, "no-such-session")
			if !errors.Is(err, ErrPTYSessionNotFound) {
				t.Fatalf("err = %v, want ErrPTYSessionNotFound", err)
			}
		})

		if err := provider.KillPtySession(ctx, name, placement.LeadPTYSessionID); err != nil {
			t.Fatalf("KillPtySession: %v", err)
		}
		if err := provider.KillPtySession(ctx, name, placement.LeadPTYSessionID); err != nil {
			t.Errorf("killing an already-dead session = %v, want nil (absent-safe)", err)
		}
	})

	t.Run("host key was pinned on first use", func(t *testing.T) {
		raw, err := os.ReadFile(hostKeyPath)
		if err != nil {
			t.Fatalf("read host key store: %v", err)
		}
		if !strings.Contains(string(raw), vmHost(name)) {
			t.Errorf("host key store has no entry for %q:\n%s", vmHost(name), raw)
		}
	})
}
