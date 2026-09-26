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
// NOTE: the whole of loom's domain.RestartPolicy now round-trips —
// fleet-db's models.RestartPolicy models all twelve fields. What remains
// unmapped is the reverse direction plus OTel:
//
//   - BackoffMultiplier / ResetAfterSuccess exist only in fleet-db and
//     have no domain counterpart, so Get zeroes them and Upsert never
//     sends them.
//   - OTel: loom's Protocol / FlushIntervalMs / Traces / Metrics fields
//     are not in fleet-db's schema, so they are dropped on Upsert and
//     zeroed on Get.
//
// Future work: extend fleet-db's models.OTelSettings to cover the full
// set, or split per-host-only fields out of the domain type.
type daemonProfileWire struct {
	WorkspaceKey   string                  `json:"workspace_key"`
	PIDFile        string                  `json:"pid_file,omitempty"`
	LogDir         string                  `json:"log_dir,omitempty"`
	EventsDir      string                  `json:"events_dir,omitempty"`
	RestartPolicy  *fleetRestartPolicyWire `json:"restart_policy,omitempty"`
	MaxAgents      *int                    `json:"max_agents,omitempty"`
	IssueBackend   string                  `json:"issue_backend,omitempty"`
	AgentBackend   string                  `json:"agent_backend,omitempty"`
	StartupTimeout *int                    `json:"startup_timeout,omitempty"`
	OTel           *fleetOTelWire          `json:"otel,omitempty"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

// fleetRestartPolicyWire holds the fields fleet-db understands. Tags are
// identical to domain.RestartPolicy and to fleet-db's models.RestartPolicy;
// BackoffMultiplier and ResetAfterSuccess have no domain counterpart.
type fleetRestartPolicyWire struct {
	MaxRetries        *int  `json:"max_retries,omitempty"`
	BackoffInitial    *int  `json:"backoff_initial,omitempty"`
	BackoffMax        *int  `json:"backoff_max,omitempty"`
	BackoffMultiplier *int  `json:"backoff_multiplier,omitempty"`
	ResetAfterSuccess *int  `json:"reset_after_success,omitempty"`
	OutputTimeout     *int  `json:"output_timeout,omitempty"`
	RateLimitBackoff  *int  `json:"rate_limit_backoff,omitempty"`
	RateLimitMaxWait  *int  `json:"rate_limit_max_wait,omitempty"`
	RateLimitNoCount  *bool `json:"rate_limit_no_count,omitempty"`
	TimeoutBackoff    *int  `json:"timeout_backoff,omitempty"`
	NoWorkBackoff     *int  `json:"no_work_backoff,omitempty"`
	IdlePollInterval  *int  `json:"idle_poll_interval,omitempty"`
	YieldTimeout      *int  `json:"yield_timeout,omitempty"`
	SigtermTimeout    *int  `json:"sigterm_timeout,omitempty"`
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
		AgentBackend:   w.AgentBackend,
		StartupTimeout: w.StartupTimeout,
		UpdatedAt:      w.UpdatedAt,
	}
	if w.RestartPolicy != nil {
		out.RestartPolicy = domain.RestartPolicy{
			MaxRetries:       w.RestartPolicy.MaxRetries,
			BackoffInitial:   w.RestartPolicy.BackoffInitial,
			BackoffMax:       w.RestartPolicy.BackoffMax,
			OutputTimeout:    w.RestartPolicy.OutputTimeout,
			RateLimitBackoff: w.RestartPolicy.RateLimitBackoff,
			RateLimitMaxWait: w.RestartPolicy.RateLimitMaxWait,
			RateLimitNoCount: w.RestartPolicy.RateLimitNoCount,
			TimeoutBackoff:   w.RestartPolicy.TimeoutBackoff,
			NoWorkBackoff:    w.RestartPolicy.NoWorkBackoff,
			IdlePollInterval: w.RestartPolicy.IdlePollInterval,
			YieldTimeout:     w.RestartPolicy.YieldTimeout,
			SigtermTimeout:   w.RestartPolicy.SigtermTimeout,
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
	AgentBackend   string                  `json:"agent_backend,omitempty"`
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
		AgentBackend:   p.AgentBackend,
		StartupTimeout: p.StartupTimeout,
	}
	if hasRestartPolicy(p.RestartPolicy) {
		out.RestartPolicy = &fleetRestartPolicyWire{
			MaxRetries:       p.RestartPolicy.MaxRetries,
			BackoffInitial:   p.RestartPolicy.BackoffInitial,
			BackoffMax:       p.RestartPolicy.BackoffMax,
			OutputTimeout:    p.RestartPolicy.OutputTimeout,
			RateLimitBackoff: p.RestartPolicy.RateLimitBackoff,
			RateLimitMaxWait: p.RestartPolicy.RateLimitMaxWait,
			RateLimitNoCount: p.RestartPolicy.RateLimitNoCount,
			TimeoutBackoff:   p.RestartPolicy.TimeoutBackoff,
			NoWorkBackoff:    p.RestartPolicy.NoWorkBackoff,
			IdlePollInterval: p.RestartPolicy.IdlePollInterval,
			YieldTimeout:     p.RestartPolicy.YieldTimeout,
			SigtermTimeout:   p.RestartPolicy.SigtermTimeout,
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
	return rp.MaxRetries != nil ||
		rp.BackoffInitial != nil ||
		rp.BackoffMax != nil ||
		rp.OutputTimeout != nil ||
		rp.RateLimitBackoff != nil ||
		rp.RateLimitMaxWait != nil ||
		rp.RateLimitNoCount != nil ||
		rp.TimeoutBackoff != nil ||
		rp.NoWorkBackoff != nil ||
		rp.IdlePollInterval != nil ||
		rp.YieldTimeout != nil ||
		rp.SigtermTimeout != nil
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
