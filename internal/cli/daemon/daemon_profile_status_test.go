package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

const profileDriftMessage = `profile harness version drift: /w/.loom/agent-profiles/observer/claude: ` +
	`manifest pins "2.1.236 (Claude Code)", claude reports "2.1.237 (Claude Code)" (re-provision to bless the upgrade)`

// captureProfileStdout runs fn with stdout redirected and returns what it printed.
func captureProfileStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	out := <-done
	_ = r.Close()
	return out
}

// A profile refusal is an operator-actionable configuration fault the agent
// cannot retry its way out of, and the supervise goroutine stays alive
// re-checking — that is "blocked", not "stopped".
func TestComputeAgentStatus_ProfileInvalidIsBlocked(t *testing.T) {
	ap := SupervisedAgentStatus{
		PID:        0,
		StopReason: supervisor.StopReasonProfileInvalid,
	}
	if got := computeAgentStatus(ap, 3); got != "blocked" {
		t.Errorf("computeAgentStatus() = %q, want %q", got, "blocked")
	}
}

func TestToDaemonAgentStatus_CarriesProfileError(t *testing.T) {
	das := toDaemonAgentStatus(SupervisedAgentStatus{
		Worktree:     "observer",
		Role:         "observer",
		StopReason:   supervisor.StopReasonProfileInvalid,
		ProfileError: profileDriftMessage,
	}, 3)

	if das.ProfileError != profileDriftMessage {
		t.Fatalf("ProfileError = %q, want the drift message", das.ProfileError)
	}
	if das.Status != "blocked" {
		t.Fatalf("Status = %q, want blocked", das.Status)
	}

	raw, err := json.Marshal(das)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"profile_error"`) {
		t.Fatalf("json = %s, want a profile_error field", raw)
	}
}

// omitempty is load-bearing: a healthy fleet's JSON must be byte-identical to
// what consumers saw before this field existed.
func TestToDaemonAgentStatus_HealthyAgentOmitsProfileError(t *testing.T) {
	das := toDaemonAgentStatus(SupervisedAgentStatus{Worktree: "worker", Role: "task"}, 3)
	raw, err := json.Marshal(das)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "profile_error") {
		t.Fatalf("json = %s, want profile_error omitted", raw)
	}
}

func TestPrintAgentStatus_RendersProfileRefusal(t *testing.T) {
	out := captureProfileStdout(t, func() {
		printAgentStatus(DaemonAgentStatus{
			Worktree:       "observer",
			Role:           "observer",
			Status:         "blocked",
			LastErrorClass: "NoWork",
			ProfileError:   profileDriftMessage,
		})
	})

	if !strings.Contains(out, "Profile: INVALID") {
		t.Fatalf("output = %q, want a Profile: INVALID line", out)
	}
	if !strings.Contains(out, `2.1.237 (Claude Code)`) {
		t.Fatalf("output = %q, want the drift detail", out)
	}
	// The sticky fault reads above the transient one it used to be erased by.
	if strings.Index(out, "Profile: INVALID") > strings.Index(out, "Last error:") {
		t.Fatalf("output = %q, want the profile block above Last error", out)
	}
}

func TestPrintAgentStatus_OmitsProfileBlockWhenHealthy(t *testing.T) {
	out := captureProfileStdout(t, func() {
		printAgentStatus(DaemonAgentStatus{Worktree: "worker", Role: "task", Status: "running", PID: os.Getpid()})
	})
	if strings.Contains(out, "Profile") {
		t.Fatalf("output = %q, want no profile block for a healthy agent", out)
	}
}

// One `claude` auto-update drifts every profiled agent at once, so the count is
// the fact an operator can act on at a glance.
func TestPrintProfileBlockedBanner(t *testing.T) {
	out := captureProfileStdout(t, func() {
		printProfileBlockedBanner([]DaemonAgentStatus{
			{Worktree: "observer", ProfileError: profileDriftMessage},
			{Worktree: "planner", ProfileError: profileDriftMessage},
			{Worktree: "worker"},
		})
	})

	if !strings.Contains(out, "2 agents blocked on profile verification") {
		t.Fatalf("output = %q, want the fleet-level count", out)
	}
	if !strings.Contains(out, "observer, planner") {
		t.Fatalf("output = %q, want the blocked agents named", out)
	}
	if strings.Contains(out, "worker") {
		t.Fatalf("output = %q, want healthy agents left out", out)
	}
}

func TestPrintProfileBlockedBanner_SilentWhenHealthy(t *testing.T) {
	out := captureProfileStdout(t, func() {
		printProfileBlockedBanner([]DaemonAgentStatus{{Worktree: "worker"}, {Worktree: "planner"}})
	})
	if out != "" {
		t.Fatalf("output = %q, want nothing printed for a healthy fleet", out)
	}
}
