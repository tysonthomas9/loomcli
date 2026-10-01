package fleetsim

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

// Step is one scripted worker action against the real Loom adapter.
type Step struct {
	Name string
	Run  func(ctx context.Context, b *fleet.FleetBackend) error
}

// ScriptedWorker stands in for the agent process. It issues its writes through
// a real FleetBackend whose configured X-Actor is the worker CLI identity
// (operator@local in the captured localdogfood runs), not the supervisor's
// claim actor. It never sleeps: the Sim decides when each request applies.
type ScriptedWorker struct {
	Attempt string
	Backend *fleet.FleetBackend
	Steps   []Step
}

// NewScriptedWorker binds a worker attempt to the Sim with the given wire
// actor.
func NewScriptedWorker(sim *Sim, attempt, wireActor string, steps ...Step) *ScriptedWorker {
	return &ScriptedWorker{Attempt: attempt, Backend: sim.Backend(attempt, wireActor), Steps: steps}
}

// Run executes the steps in order and returns the process exit code the
// worker would report: 0 when every step succeeded, 1 at the first failure.
func (w *ScriptedWorker) Run(ctx context.Context) (int, error) {
	for _, s := range w.Steps {
		if err := s.Run(ctx, w.Backend); err != nil {
			return 1, fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return 0, nil
}

// CloseStep is `loom data close <id> --session <s> --reason <r>`: the data
// close command calls IssueBackend.Close with an empty Actor, so the adapter
// uses its configured actor for the best-effort /assign and the /close.
func CloseStep(issueID, session, reason string) Step {
	return Step{Name: "close " + issueID, Run: func(ctx context.Context, b *fleet.FleetBackend) error {
		_, err := b.Close(ctx, issueID, backend.CloseParams{Reason: reason, Session: session})
		return err
	}}
}
