package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// BillingWallMarker had a classification arm, a precedence over auth, and an
// account-wall policy — and no emitter. The screen-scrape detector that used
// to raise it was removed for cause (PUPPET-442: 11 detections, 0 true
// positives, all of them agent output quoting a banner), and nothing upstream
// could name a billing failure without re-reading a screen.
//
// harness-wrapper reads the harness's own transcript tag now, so a walled turn
// arrives carrying chat.CodeBillingWall. This file walks the whole path that
// code takes — turn → marker → log → classifier → wall policy — because each
// link was tested and the chain was not.
//
// What it deliberately does NOT do is assert on a screen. The verdict's source
// is a tag the harness wrote about its own API call; that is what makes it
// immune to the failure mode that sank the detector, and a test that fed it a
// banner would be testing the thing we removed.

// TestBillingWall_TurnCodeReachesTheWallPolicy is the end-to-end link.
func TestBillingWall_TurnCodeReachesTheWallPolicy(t *testing.T) {
	// 1. The turn harness-wrapper emits for a billing_error tag.
	turn := chat.Turn{
		Role:   chat.RoleAssistant,
		State:  chat.TurnStateErrored,
		Code:   chat.CodeBillingWall,
		Reason: chat.ReasonBillingWall + " (harness tag: billing_error; Credit balance too low)",
	}

	// 2. loom turns it into a marked invocation error. Reaching into the
	//    backends package from here would be a layering violation, so the
	//    marker line is built the way that package builds it — the format is
	//    the contract between them, and agenterr's matcher is what reads it.
	markerLine := agenterr.BillingWallMarker + ": " + turn.Reason

	// 3. The supervisor reads the marker out of the agent log, scoped to this
	//    run, and it outranks the stop reason.
	s := newClassifySupervisor("claude")
	ap := newMarkerAgent(t, StopReasonRunDurationExceeded, "running turn...\n"+markerLine+"\n")
	s.classifyAgentExit(ap, 143)

	ap.Mu.Lock()
	lastErr := ap.LastError
	ap.Mu.Unlock()
	if lastErr == nil {
		t.Fatal("no classification recorded")
	}
	if lastErr.Class != agenterr.OutcomeFromHarness(wrapper.ErrBilling) {
		t.Fatalf("Class = %v, want ErrBilling", lastErr.Class)
	}

	// 4. ErrBilling earns a wall. wallScopeFor is the policy's own predicate,
	//    so this asserts the outcome reaches the gate rather than re-deriving
	//    what the gate then does with it.
	if scope := wallScopeFor(lastErr.Class); scope == wallScopeNone {
		t.Fatalf("wallScopeFor(ErrBilling) = none — the verdict never reaches the account wall")
	}
}

// TestBillingWall_MarkerBeforeTheRunOffsetIsIgnored is the correlation half.
//
// Agent logs are append-only and per-role, so they span days and many runs. A
// billing marker is categorical and fatal; one left in the log by a previous
// run would condemn every later run on that agent until someone rotated the
// file by hand. That is the exact bug LogFileStartOffset exists to prevent, and
// giving the marker an emitter is what makes it reachable for the first time.
func TestBillingWall_MarkerBeforeTheRunOffsetIsIgnored(t *testing.T) {
	previous := "old run\n" + agenterr.BillingWallMarker + ": Credit balance too low\n"
	current := "this run is fine\nwrote 3 files\n"

	logPath := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(logPath, []byte(previous+current), 0o600); err != nil {
		t.Fatal(err)
	}
	offset := int64(len(previous))

	if _, ok := agenterr.ClassifyMarkerFromLogAt(logPath, offset); ok {
		t.Error("a previous run's billing marker condemned this run")
	}
	// ...and it is found when the run really did write it.
	ae, ok := agenterr.ClassifyMarkerFromLogAt(logPath, 0)
	if !ok || ae.Class != agenterr.OutcomeFromHarness(wrapper.ErrBilling) {
		t.Fatalf("whole-file read = %+v/%v, want ErrBilling", ae, ok)
	}
}

// TestBillingWall_OutranksAuth pins the precedence, which is load-bearing for
// the operator message: a screen showing both an empty balance and a stale
// logged-out banner is a billing problem, and "renew the harness login" would
// send them to fix the wrong thing.
func TestBillingWall_OutranksAuth(t *testing.T) {
	tail := strings.Join([]string{
		agenterr.AuthRequiredMarker + ": auth_required",
		agenterr.BillingWallMarker + ": billing_wall",
	}, "\n")
	ae, ok := agenterr.ClassifyMarker(tail)
	if !ok {
		t.Fatal("no marker classified")
	}
	if ae.Class != agenterr.OutcomeFromHarness(wrapper.ErrBilling) {
		t.Fatalf("Class = %v, want ErrBilling to outrank the auth marker", ae.Class)
	}
}
