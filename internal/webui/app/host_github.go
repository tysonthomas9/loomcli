package app

import (
	"github.com/tysonthomas9/loomcli/internal/prwatch"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
)

// HostGitHub is the host GitHub connector the agents' PR watches read
// through (OR10), as the host's own viewer, with no agent or bridge. nil
// without a store or vault key; PR watches are then unavailable.
func HostGitHub(cfg webui.ServerConfig) prwatch.Host {
	disp := (&Server{config: cfg}).buildConnectorDispatcher()
	if disp == nil {
		return nil
	}
	return modbuilder.NewHostGitHub(cfg.Store, disp, cfg.LocalSettingsDir)
}
