//go:build daemon_bugreplay

package supervisor

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

type quarantineEvent uint8

const (
	quarantineNoProgress quarantineEvent = iota
	quarantineDesign
	quarantineNotes
	quarantineComment
	quarantineLabelProgress
	quarantineInfrastructureKill
)

func (e quarantineEvent) String() string {
	return [...]string{"no-progress", "design", "notes", "comment", "label", "infrastructure-kill"}[e]
}

// #522/#520: a task's visible progress resets the consecutive-kill count,
// while an infrastructure kill cannot add to it. The first two generated
// transitions exercise comment and label progress; the remaining sequence
// explores their order with design, notes, no progress and infrastructure.
func TestPropertyQuarantineCountsOnlyFreshNoProgressKills(t *testing.T) {
	seed := int64(522)
	if value := os.Getenv("DAEMON_PROPERTY_SEED"); value != "" {
		var err error
		seed, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatalf("DAEMON_PROPERTY_SEED: %v", err)
		}
	}
	t.Logf("seed=%d", seed)
	rng := rand.New(rand.NewSource(seed + 522))
	const iterations = 100
	const depth = 16
	states := make(map[string]struct{})
	executed, maxDepth := 0, 0
	defer func() { t.Logf("iterations=%d max_depth=%d distinct_states=%d", executed, maxDepth, len(states)) }()
	for iteration := 0; iteration < iterations; iteration++ {
		executed++
		var design, notes string
		var comments []backend.CommentData
		var labels []string
		mock := clitest.NewMockIssueBackend()
		mock.GetFn = func(_ context.Context, _ string) (*backend.IssueDetailData, error) {
			return &backend.IssueDetailData{
				IssueData: backend.IssueData{Design: design, Notes: notes, Labels: append([]string(nil), labels...)},
				Comments:  append([]backend.CommentData(nil), comments...),
			}, nil
		}
		s := newQuarantineSupervisor(mock)
		ap := newKilledAgent(t, "property-agent", "property-task", timeoutOutcome())
		count := 0
		trace := make([]quarantineEvent, 0, depth+2)
		for step := 0; step < depth; step++ {
			e := quarantineEvent(rng.Intn(6))
			if step == 0 {
				e = quarantineComment
			} else if step == 1 {
				e = quarantineLabelProgress
			}
			// A progress delta needs a previous successful GET baseline.
			// Establish it through the same production ledger hook.
			if count == 0 && e != quarantineNoProgress && e != quarantineInfrastructureKill {
				ap.LastError = &agenterr.AgentError{Class: timeoutOutcome()}
				s.recordTaskExitForQuarantine(ap, 137)
				count++
				trace = append(trace, quarantineNoProgress)
			}
			switch e {
			case quarantineDesign:
				design = fmt.Sprintf("design-%d-%d", iteration, step)
			case quarantineNotes:
				notes = fmt.Sprintf("notes-%d-%d", iteration, step)
			case quarantineComment:
				comments = append(comments, backend.CommentData{ID: int64(len(comments) + 1), Text: "task progress"})
			case quarantineLabelProgress:
				labels = append(labels, fmt.Sprintf("progress-%d", step))
			case quarantineInfrastructureKill:
				ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome)}
			}
			if e != quarantineInfrastructureKill {
				ap.LastError = &agenterr.AgentError{Class: timeoutOutcome()}
			}
			s.recordTaskExitForQuarantine(ap, 137)
			trace = append(trace, e)
			switch e {
			case quarantineNoProgress:
				count++
			case quarantineDesign, quarantineNotes, quarantineComment, quarantineLabelProgress:
				count = 0
			}
			if len(trace) > maxDepth {
				maxDepth = len(trace)
			}
			actual := recordCount(s, "property-task")
			states[fmt.Sprintf("%s/%d/%d", e, count, actual)] = struct{}{}
			if actual != count {
				t.Fatalf("#522/#520 seed=%d iteration=%d step=%d trace=%v: count=%d, want %d", seed, iteration, step, trace, actual, count)
			}
		}
	}
}
