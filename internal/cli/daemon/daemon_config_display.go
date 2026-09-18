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
	if rp.IdlePollInterval == nil {
		rp.IdlePollInterval = config.IntPtr(30)
	}
	if rp.YieldTimeout == nil {
		rp.YieldTimeout = config.IntPtr(supervisor.DefaultYieldTimeout)
	}
	if rp.SigtermTimeout == nil {
		rp.SigtermTimeout = config.IntPtr(supervisor.DefaultSigtermTimeout)
	}
	if rp.AccountWallCooldown == nil {
		rp.AccountWallCooldown = config.IntPtr(900)
	}
}

// maskDaemonSecrets replaces sensitive fields in config.DaemonSettings with "***".
// Only non-empty values are masked so users can distinguish "set" from "not set".
func maskDaemonSecrets(ds *config.DaemonSettings) {
	if ds.RedisURL != "" {
		ds.RedisURL = "***"
	}
}

// copyIntPtr clones an optional int so a copied config never aliases the live
// one — a display call must not be able to mutate the daemon's own settings.
func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// deepCopyRestartPolicy returns a deep copy of src with all pointer fields cloned.
func deepCopyRestartPolicy(src *config.RestartPolicy) config.RestartPolicy {
	dst := *src
	dst.MaxRetries = copyIntPtr(src.MaxRetries)
	dst.BackoffInitial = copyIntPtr(src.BackoffInitial)
	dst.BackoffMax = copyIntPtr(src.BackoffMax)
	dst.OutputTimeout = copyIntPtr(src.OutputTimeout)
	dst.RateLimitBackoff = copyIntPtr(src.RateLimitBackoff)
	dst.RateLimitMaxWait = copyIntPtr(src.RateLimitMaxWait)
	dst.TimeoutBackoff = copyIntPtr(src.TimeoutBackoff)
	dst.NoWorkBackoff = copyIntPtr(src.NoWorkBackoff)
	dst.IdlePollInterval = copyIntPtr(src.IdlePollInterval)
	dst.YieldTimeout = copyIntPtr(src.YieldTimeout)
	dst.SigtermTimeout = copyIntPtr(src.SigtermTimeout)
	dst.AccountWallCooldown = copyIntPtr(src.AccountWallCooldown)
	if src.RateLimitNoCount != nil {
		v := *src.RateLimitNoCount
		dst.RateLimitNoCount = &v
	}
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
