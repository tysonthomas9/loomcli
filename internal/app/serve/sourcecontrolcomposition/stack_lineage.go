package sourcecontrolcomposition

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

var _ sourcecontrol.TaskOutcomeRecorder = (*SourceControlCapability)(nil)

func (capability *SourceControlCapability) RecordTaskOutcome(
	ctx context.Context,
	command sourcecontrol.TaskOutcomeCommand,
) (bool, error) {
	if capability == nil || capability.outcomes == nil {
		return false, nil
	}
	return capability.outcomes.RecordTaskOutcome(ctx, command)
}
