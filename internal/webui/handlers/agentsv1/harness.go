package agentsv1

import (
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

// HarnessInfo is GET /harnesses/{harness}[?repo=]: the harness's last
// background capability probe, for the repo clone's project settings when
// repo is given. capabilities_supported is false for a harness without a
// probe; probed_at is absent before the first good probe. It carries the
// account's kind and label only, never an email, key or token. Health says
// why the harness is unavailable or warns that it is newer than tested.
type HarnessInfo struct {
	Harness               string         `json:"harness"`
	CapabilitiesSupported bool           `json:"capabilities_supported"`
	AccountKind           string         `json:"account_kind,omitempty"`
	AccountLabel          string         `json:"account_label,omitempty"`
	SlashCommands         []SlashCommand `json:"slash_commands"`
	ProbedAt              *time.Time     `json:"probed_at,omitempty"`
	Health                HarnessHealth  `json:"health"`
}

// HarnessHealth is the harness's status: Reason says why it is unavailable;
// VersionWarning, apart, that its version is newer than tested.
type HarnessHealth struct {
	OK             bool   `json:"ok"`
	Reason         string `json:"reason,omitempty"`
	VersionWarning string `json:"version_warning,omitempty"`
}

// SlashCommand is one command the harness offers.
type SlashCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argument_hint,omitempty"`
}

func (h *Handler) getHarness(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	harness := r.PathValue("harness")
	caps, supported, err := s.Capabilities(r.Context(), harness, r.URL.Query().Get("repo"))
	if err != nil {
		return 0, nil, err
	}
	health, err := s.Health(r.Context(), harness)
	if err != nil {
		return 0, nil, err
	}
	out := HarnessInfo{Harness: harness, CapabilitiesSupported: supported, SlashCommands: []SlashCommand{},
		Health: HarnessHealth{OK: health.OK, VersionWarning: health.Version.Warning()}}
	if !health.OK {
		out.Health.Reason = health.Warning
	}
	if caps != nil {
		out.AccountKind, out.AccountLabel, out.ProbedAt = caps.AccountKind, caps.AccountLabel, &caps.ProbedAt
		for _, c := range caps.SlashCommands {
			out.SlashCommands = append(out.SlashCommands, SlashCommand(c))
		}
	}
	return http.StatusOK, out, nil
}
