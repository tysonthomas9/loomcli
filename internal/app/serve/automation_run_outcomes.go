package serve

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/driver"
)

// automationDriverRunOutcomePublisher adapts Execution's consumer-owned
// outcome port at the composition boundary. The named SystemEventing workflow
// remains independent of Execution and receives only its own verified-source
// envelope and event-content request.
type automationDriverRunOutcomePublisher struct {
	emitter systemeventing.RunOutcomeEmitter
}

var _ driver.RunOutcomePublisher = (*automationDriverRunOutcomePublisher)(nil)

func newAutomationDriverRunOutcomePublisher(emitter systemeventing.RunOutcomeEmitter) (driver.RunOutcomePublisher, error) {
	if emitter == nil {
		return nil, fmt.Errorf("%w: run outcome emitter is required", systemeventing.ErrUnavailable)
	}
	return &automationDriverRunOutcomePublisher{emitter: emitter}, nil
}

func (publisher *automationDriverRunOutcomePublisher) PublishRunOutcome(ctx context.Context, outcome driver.RunOutcome) error {
	if publisher == nil || publisher.emitter == nil {
		return systemeventing.ErrUnavailable
	}
	if outcome.EventType != driver.RunFinishedEventType ||
		outcome.EventID != driver.RunFinishedEventID(outcome.RunID, outcome.Status) ||
		!outcome.Status.IsTerminal() || outcome.ActorRef != driver.RunFinishedActor {
		return fmt.Errorf("%w: invalid driver run outcome envelope", systemeventing.ErrInvalidRequest)
	}
	_, err := publisher.emitter.EmitRunOutcome(ctx, outcome.WorkspaceKey, driver.RunFinishedActor, systemeventing.EmitRequest{
		WorkspaceKey:  outcome.WorkspaceKey,
		SourceEventID: outcome.EventID,
		EventType:     outcome.EventType,
		SourceRef:     outcome.RunID,
		SubjectRef:    outcome.RunID,
		ParentEventID: outcome.ParentEventID,
		EpicID:        outcome.EpicID,
		OccurredAt:    outcome.OccurredAt,
		Payload:       outcome.Payload,
	})
	if err != nil {
		return fmt.Errorf("publish driver run outcome %q: %w", outcome.EventID, err)
	}
	return nil
}
