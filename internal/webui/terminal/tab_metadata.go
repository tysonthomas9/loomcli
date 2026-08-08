package terminal

import "github.com/tysonthomas9/loomcli/internal/modules/interaction"

// TabMetadata keeps terminal-facing callers source-compatible with
// Interaction's canonical terminal-tab projection.
type TabMetadata = interaction.TabMetadata

// LaunchSpec keeps terminal-facing callers source-compatible with
// Interaction's canonical terminal launch contract.
type LaunchSpec = interaction.LaunchSpec

// TabMetadataReader is the narrow terminal-tab read port.
type TabMetadataReader = interaction.TabMetadataReader

// TabMetadataStore is the terminal-tab persistence port.
type TabMetadataStore = interaction.TabMetadataStore

// ValidateSessionName keeps terminal-facing callers source-compatible with
// Interaction's canonical validation policy.
func ValidateSessionName(name string) error {
	return interaction.ValidateTerminalSessionName(name)
}
