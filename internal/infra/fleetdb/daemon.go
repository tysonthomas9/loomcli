package fleetdb

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type daemonStore struct{ client *Client }

var _ store.DaemonProfileStore = (*daemonStore)(nil)

// daemonProfileWire mirrors fleet-db's models.DaemonProfile JSON.
//
// NOTE: fleet-db's RestartPolicy schema is narrower than loom's domain
// type. Fields that exist only in loom (OutputTimeout, RateLimitBackoff,
// RateLimitMaxWait, RateLimitNoCount, TimeoutBackoff, NoWorkBackoff,
// IdlePollInterval, YieldTimeout, SigtermTimeout) are silently dropped
// on Upsert and zeroed on Get. Same for OTel: loom's Protocol /
// FlushIntervalMs / Traces / Metrics fields are not persisted.
//
// Future work: extend fleet-db's models.RestartPolicy + OTelSettings to
// cover the full set, or split per-host-only fields out of the domain
// type.
type daemonProfileWire struct {
	WorkspaceKey   string                  `json:"workspace_key"`
	PIDFile        string                  `json:"pid_file,omitempty"`
	LogDir         string                  `json:"log_dir,omitempty"`
	EventsDir      string                  `json:"events_dir,omitempty"`
	RestartPolicy  *fleetRestartPolicyWire `json:"restart_policy,omitempty"`
	MaxAgents      *int                    `json:"max_agents,omitempty"`
	IssueBackend   string                  `json:"issue_backend,omitempty"`
	StartupTimeout *int                    `json:"startup_timeout,omitempty"`
	OTel           *fleetOTelWire          `json:"otel,omitempty"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

// fleetRestartPolicyWire holds only the fields fleet-db understands.
type fleetRestartPolicyWire struct {
	MaxRetries        *int `json:"max_retries,omitempty"`
	BackoffInitial    *int `json:"backoff_initial,omitempty"`
	BackoffMax        *int `json:"backoff_max,omitempty"`
	BackoffMultiplier *int `json:"backoff_multiplier,omitempty"`
	ResetAfterSuccess *int `json:"reset_after_success,omitempty"`
}

// fleetOTelWire holds only the OTel fields fleet-db understands.
type fleetOTelWire struct {
	Enabled     *bool             `json:"enabled,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	ServiceName string            `json:"service_name,omitempty"`
	SampleRate  *float64          `json:"sample_rate,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

func (w daemonProfileWire) toDomain() *domain.DaemonProfile {
	out := &domain.DaemonProfile{
		WorkspaceKey:   w.WorkspaceKey,
		PIDFile:        w.PIDFile,
		LogDir:         w.LogDir,
		EventsDir:      w.EventsDir,
		MaxAgents:      w.MaxAgents,
		IssueBackend:   w.IssueBackend,
		StartupTimeout: w.StartupTimeout,
		UpdatedAt:      w.UpdatedAt,
	}
	if w.RestartPolicy != nil {
		out.RestartPolicy = domain.RestartPolicy{
			MaxRetries:     w.RestartPolicy.MaxRetries,
			BackoffInitial: w.RestartPolicy.BackoffInitial,
			BackoffMax:     w.RestartPolicy.BackoffMax,
			// BackoffMultiplier + ResetAfterSuccess are fleet-db-only;
			// no domain mapping yet.
		}
	}
	if w.OTel != nil {
		out.OTel = &domain.OTelSettings{
			Endpoint:    w.OTel.Endpoint,
			ServiceName: w.OTel.ServiceName,
		}
		if w.OTel.Enabled != nil {
			out.OTel.Enabled = *w.OTel.Enabled
		}
		if w.OTel.SampleRate != nil {
			out.OTel.SampleRate = *w.OTel.SampleRate
		}
	}
	return out
}

// daemonProfileUpsertWire is the PUT body shape — fleet-db rejects
// unknown fields (WorkspaceKey/UpdatedAt) since the workspace is in the
// URL path and the timestamp is server-managed.
type daemonProfileUpsertWire struct {
	PIDFile        string                  `json:"pid_file,omitempty"`
	LogDir         string                  `json:"log_dir,omitempty"`
	EventsDir      string                  `json:"events_dir,omitempty"`
	IssueBackend   string                  `json:"issue_backend,omitempty"`
	MaxAgents      *int                    `json:"max_agents,omitempty"`
	StartupTimeout *int                    `json:"startup_timeout,omitempty"`
	RestartPolicy  *fleetRestartPolicyWire `json:"restart_policy,omitempty"`
	OTel           *fleetOTelWire          `json:"otel,omitempty"`
}

func domainToUpsertWire(p *domain.DaemonProfile) daemonProfileUpsertWire {
	out := daemonProfileUpsertWire{
		PIDFile:        p.PIDFile,
		LogDir:         p.LogDir,
		EventsDir:      p.EventsDir,
		MaxAgents:      p.MaxAgents,
		IssueBackend:   p.IssueBackend,
		StartupTimeout: p.StartupTimeout,
	}
	if hasRestartPolicy(p.RestartPolicy) {
		out.RestartPolicy = &fleetRestartPolicyWire{
			MaxRetries:     p.RestartPolicy.MaxRetries,
			BackoffInitial: p.RestartPolicy.BackoffInitial,
			BackoffMax:     p.RestartPolicy.BackoffMax,
		}
	}
	if p.OTel != nil {
		enabled := p.OTel.Enabled
		sample := p.OTel.SampleRate
		out.OTel = &fleetOTelWire{
			Enabled:     &enabled,
			Endpoint:    p.OTel.Endpoint,
			ServiceName: p.OTel.ServiceName,
			SampleRate:  &sample,
		}
	}
	return out
}

// hasRestartPolicy is true when at least one mappable field is non-nil.
// Avoids sending a fully-empty restart_policy block that would clobber
// fleet-db's defaults.
func hasRestartPolicy(rp domain.RestartPolicy) bool {
	return rp.MaxRetries != nil || rp.BackoffInitial != nil || rp.BackoffMax != nil
}

func (s *daemonStore) Get(ctx context.Context, ws string) (*domain.DaemonProfile, error) {
	var resp daemonProfileWire
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/daemon", nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *daemonStore) Upsert(ctx context.Context, profile *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	body := domainToUpsertWire(profile)
	var resp daemonProfileWire
	if err := s.client.do(ctx, "PUT", "/api/v1/"+pathEscape(profile.WorkspaceKey)+"/daemon", body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}
