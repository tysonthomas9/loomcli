package cli

// applyRestartPolicyDefaults fills nil RestartPolicy fields with runtime defaults
// matching the getter functions in daemon_restart.go.
func applyRestartPolicyDefaults(rp *RestartPolicy) {
	if rp.MaxRetries == nil {
		rp.MaxRetries = intPtr(3)
	}
	if rp.BackoffInitial == nil {
		rp.BackoffInitial = intPtr(2)
	}
	if rp.BackoffMax == nil {
		rp.BackoffMax = intPtr(300)
	}
	if rp.OutputTimeout == nil {
		rp.OutputTimeout = intPtr(900)
	}
	if rp.RateLimitBackoff == nil {
		rp.RateLimitBackoff = intPtr(30)
	}
	if rp.RateLimitMaxWait == nil {
		rp.RateLimitMaxWait = intPtr(300)
	}
	if rp.RateLimitNoCount == nil {
		rp.RateLimitNoCount = boolPtr(true)
	}
	if rp.TimeoutBackoff == nil {
		rp.TimeoutBackoff = intPtr(5)
	}
	if rp.NoWorkBackoff == nil {
		rp.NoWorkBackoff = intPtr(30)
	}
	if rp.IdlePollInterval == nil {
		rp.IdlePollInterval = intPtr(30)
	}
}

// maskDaemonSecrets replaces sensitive fields in DaemonSettings with "***".
// Only non-empty values are masked so users can distinguish "set" from "not set".
func maskDaemonSecrets(ds *DaemonSettings) {
	if ds.RedisURL != "" {
		ds.RedisURL = "***"
	}
	if ds.FleetDB != nil && ds.FleetDB.RedisURL != "" {
		ds.FleetDB.RedisURL = "***"
	}
}

// resolvedConfigForDisplay returns a copy of cfg with secrets masked and
// RestartPolicy defaults filled in. The original cfg is not mutated.
func resolvedConfigForDisplay(cfg *DaemonConfig) DaemonConfig {
	out := *cfg

	// Deep copy Daemon to avoid mutating the original
	out.Daemon = cfg.Daemon
	out.Daemon.RestartPolicy = cfg.Daemon.RestartPolicy

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
	if cfg.Daemon.FleetDB != nil {
		fleetCopy := *cfg.Daemon.FleetDB
		out.Daemon.FleetDB = &fleetCopy
	}

	// Copy RestartPolicy pointer fields
	rp := &out.Daemon.RestartPolicy
	if cfg.Daemon.RestartPolicy.MaxRetries != nil {
		v := *cfg.Daemon.RestartPolicy.MaxRetries
		rp.MaxRetries = &v
	}
	if cfg.Daemon.RestartPolicy.BackoffInitial != nil {
		v := *cfg.Daemon.RestartPolicy.BackoffInitial
		rp.BackoffInitial = &v
	}
	if cfg.Daemon.RestartPolicy.BackoffMax != nil {
		v := *cfg.Daemon.RestartPolicy.BackoffMax
		rp.BackoffMax = &v
	}
	if cfg.Daemon.RestartPolicy.OutputTimeout != nil {
		v := *cfg.Daemon.RestartPolicy.OutputTimeout
		rp.OutputTimeout = &v
	}
	if cfg.Daemon.RestartPolicy.RateLimitBackoff != nil {
		v := *cfg.Daemon.RestartPolicy.RateLimitBackoff
		rp.RateLimitBackoff = &v
	}
	if cfg.Daemon.RestartPolicy.RateLimitMaxWait != nil {
		v := *cfg.Daemon.RestartPolicy.RateLimitMaxWait
		rp.RateLimitMaxWait = &v
	}
	if cfg.Daemon.RestartPolicy.RateLimitNoCount != nil {
		v := *cfg.Daemon.RestartPolicy.RateLimitNoCount
		rp.RateLimitNoCount = &v
	}
	if cfg.Daemon.RestartPolicy.TimeoutBackoff != nil {
		v := *cfg.Daemon.RestartPolicy.TimeoutBackoff
		rp.TimeoutBackoff = &v
	}
	if cfg.Daemon.RestartPolicy.NoWorkBackoff != nil {
		v := *cfg.Daemon.RestartPolicy.NoWorkBackoff
		rp.NoWorkBackoff = &v
	}
	if cfg.Daemon.RestartPolicy.IdlePollInterval != nil {
		v := *cfg.Daemon.RestartPolicy.IdlePollInterval
		rp.IdlePollInterval = &v
	}

	// Copy Roles map
	if cfg.Roles != nil {
		out.Roles = make(map[string]RoleConfig, len(cfg.Roles))
		for k, v := range cfg.Roles {
			out.Roles[k] = v
		}
	}

	// Copy Agents slice
	if cfg.Agents != nil {
		out.Agents = make([]AgentEntry, len(cfg.Agents))
		copy(out.Agents, cfg.Agents)
	}

	// Apply defaults and mask secrets
	applyRestartPolicyDefaults(&out.Daemon.RestartPolicy)
	maskDaemonSecrets(&out.Daemon)

	return out
}
