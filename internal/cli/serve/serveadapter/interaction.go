package serveadapter

import "github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter/interactioncomposition"

type InteractionConfig = interactioncomposition.InteractionConfig
type InteractionCapability = interactioncomposition.InteractionCapability

func BuildInteractionCapability(config InteractionConfig) (*InteractionCapability, error) {
	return interactioncomposition.BuildInteractionCapability(config)
}
