package sourcecontrolcomposition

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

var _ sourcecontrol.TaskOutcomeRecorder = (*SourceControlCapability)(nil)
var _ sourcecontrol.StackBindingResolver = (*SourceControlCapability)(nil)

func (capability *SourceControlCapability) RecordTaskOutcome(
	ctx context.Context,
	command sourcecontrol.TaskOutcomeCommand,
) (bool, error) {
	if capability == nil || capability.outcomes == nil {
		return false, nil
	}
	return capability.outcomes.RecordTaskOutcome(ctx, command)
}

func (capability *SourceControlCapability) ResolveTaskStackBinding(
	ctx context.Context,
	workspace,
	repository,
	taskID string,
) (sourcecontrol.TaskStackBinding, bool, error) {
	if capability == nil || capability.stacks == nil {
		return sourcecontrol.TaskStackBinding{}, false, nil
	}
	return capability.stacks.ResolveTaskStackBinding(ctx, workspace, repository, taskID)
}
