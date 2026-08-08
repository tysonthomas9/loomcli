package daemon

import (
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// applyRestartPolicyDefaults fills nil config.RestartPolicy fields with runtime defaults
// matching the getter functions in supervisor/restart.go.
func applyRestartPolicyDefaults(rp *config.RestartPolicy) {
	if rp.MaxRetries == nil {
		rp.MaxRetries = config.IntPtr(3)
	}
	if rp.BackoffInitial == nil {
		rp.BackoffInitial = config.IntPtr(2)
	}
	if rp.BackoffMax == nil {
		rp.BackoffMax = config.IntPtr(300)
	}
	if rp.OutputTimeout == nil {
		rp.OutputTimeout = config.IntPtr(900)
	}
	if rp.RateLimitBackoff == nil {
		rp.RateLimitBackoff = config.IntPtr(30)
	}
	if rp.RateLimitMaxWait == nil {
		rp.RateLimitMaxWait = config.IntPtr(300)
	}
	if rp.RateLimitNoCount == nil {
		rp.RateLimitNoCount = config.BoolPtr(true)
	}
	if rp.TimeoutBackoff == nil {
		rp.TimeoutBackoff = config.IntPtr(5)
	}
	if rp.NoWorkBackoff == nil {
		rp.NoWorkBackoff = config.IntPtr(30)
	}
	if rp.NoWorkBackoffMax == nil {
		rp.NoWorkBackoffMax = config.IntPtr(900)
	}
	if rp.IdlePollInterval == nil {
		rp.IdlePollInterval = config.IntPtr(30)
	}
	if rp.YieldTimeout == nil {
		rp.YieldTimeout = config.IntPtr(supervisor.DefaultYieldTimeout)
	}
	if rp.SigtermTimeout == nil {
		rp.SigtermTimeout = config.IntPtr(supervisor.DefaultSigtermTimeout)
	}
}

// maskDaemonSecrets replaces sensitive fields in config.DaemonSettings with "***".
// Only non-empty values are masked so users can distinguish "set" from "not set".
func maskDaemonSecrets(ds *config.DaemonSettings) {
	if ds.RedisURL != "" {
		ds.RedisURL = "***"
	}
}

// cloneIntPtr returns a fresh pointer to a copy of *v, or nil if v is nil.
func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// deepCopyRestartPolicy returns a deep copy of src with all pointer fields cloned.
func deepCopyRestartPolicy(src *config.RestartPolicy) config.RestartPolicy {
	dst := *src
	dst.MaxRetries = cloneIntPtr(src.MaxRetries)
	dst.BackoffInitial = cloneIntPtr(src.BackoffInitial)
	dst.BackoffMax = cloneIntPtr(src.BackoffMax)
	dst.OutputTimeout = cloneIntPtr(src.OutputTimeout)
	dst.RateLimitBackoff = cloneIntPtr(src.RateLimitBackoff)
	dst.RateLimitMaxWait = cloneIntPtr(src.RateLimitMaxWait)
	if src.RateLimitNoCount != nil {
		v := *src.RateLimitNoCount
		dst.RateLimitNoCount = &v
	}
	dst.TimeoutBackoff = cloneIntPtr(src.TimeoutBackoff)
	dst.NoWorkBackoff = cloneIntPtr(src.NoWorkBackoff)
	dst.NoWorkBackoffMax = cloneIntPtr(src.NoWorkBackoffMax)
	dst.IdlePollInterval = cloneIntPtr(src.IdlePollInterval)
	dst.YieldTimeout = cloneIntPtr(src.YieldTimeout)
	dst.SigtermTimeout = cloneIntPtr(src.SigtermTimeout)
	return dst
}

// resolvedConfigForDisplay returns a copy of cfg with secrets masked and
// config.RestartPolicy defaults filled in. The original cfg is not mutated.
func resolvedConfigForDisplay(cfg *config.DaemonConfig) config.DaemonConfig {
	out := *cfg

	// Deep copy Daemon to avoid mutating the original
	out.Daemon = cfg.Daemon
	out.Daemon.RestartPolicy = deepCopyRestartPolicy(&cfg.Daemon.RestartPolicy)

	// Copy pointer fields to avoid aliasing
	if cfg.Daemon.MaxAgents != nil {
		v := *cfg.Daemon.MaxAgents
		out.Daemon.MaxAgents = &v
	}
	if cfg.Daemon.StartupTimeout != nil {
		v := *cfg.Daemon.StartupTimeout
		out.Daemon.StartupTimeout = &v
	}
	if cfg.Daemon.OTel != nil {
		otelCopy := *cfg.Daemon.OTel
		out.Daemon.OTel = &otelCopy
	}

	// Copy Roles map
	if cfg.Roles != nil {
		out.Roles = make(map[string]config.RoleConfig, len(cfg.Roles))
		for k, v := range cfg.Roles {
			out.Roles[k] = v
		}
	}

	// Copy Agents slice
	if cfg.Agents != nil {
		out.Agents = make([]config.AgentEntry, len(cfg.Agents))
		copy(out.Agents, cfg.Agents)
	}

	// Apply defaults and mask secrets
	applyRestartPolicyDefaults(&out.Daemon.RestartPolicy)
	maskDaemonSecrets(&out.Daemon)

	return out
}
