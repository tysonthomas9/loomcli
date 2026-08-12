package serve

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// RunCaptureCapability is the composition-owned immutable evidence view. It
// cannot mutate any lifecycle owner or Artifact.
type RunCaptureCapability struct{ api runcapture.API }

func (capability *RunCaptureCapability) RunCaptureAPI() runcapture.API {
	if capability == nil {
		return nil
	}
	return capability.api
}

func NewRunCaptureCapability(
	executions execution.TaskRunQueries,
	interactions interaction.SessionQueries,
	artifactQueries artifacts.QueryAPI,
) (*RunCaptureCapability, error) {
	service, err := runcapture.New(executions, interactions, artifactQueries)
	if err != nil {
		return nil, fmt.Errorf("compose Run Capture: %w", err)
	}
	return &RunCaptureCapability{api: service}, nil
}
