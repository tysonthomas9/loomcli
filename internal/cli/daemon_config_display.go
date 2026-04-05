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
	if rp.YieldTimeout == nil {
		rp.YieldTimeout = intPtr(DefaultYieldTimeout)
	}
	if rp.SigtermTimeout == nil {
		rp.SigtermTimeout = intPtr(DefaultSigtermTimeout)
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
	if ds.Fleet != nil && ds.Fleet.APIKey != "" {
		ds.Fleet.APIKey = "***"
	}
}

// deepCopyRestartPolicy returns a deep copy of src with all pointer fields cloned.
func deepCopyRestartPolicy(src *RestartPolicy) RestartPolicy {
	dst := *src
	if src.MaxRetries != nil {
		v := *src.MaxRetries
		dst.MaxRetries = &v
	}
	if src.BackoffInitial != nil {
		v := *src.BackoffInitial
		dst.BackoffInitial = &v
	}
	if src.BackoffMax != nil {
		v := *src.BackoffMax
		dst.BackoffMax = &v
	}
	if src.OutputTimeout != nil {
		v := *src.OutputTimeout
		dst.OutputTimeout = &v
	}
	if src.RateLimitBackoff != nil {
		v := *src.RateLimitBackoff
		dst.RateLimitBackoff = &v
	}
	if src.RateLimitMaxWait != nil {
		v := *src.RateLimitMaxWait
		dst.RateLimitMaxWait = &v
	}
	if src.RateLimitNoCount != nil {
		v := *src.RateLimitNoCount
		dst.RateLimitNoCount = &v
	}
	if src.TimeoutBackoff != nil {
		v := *src.TimeoutBackoff
		dst.TimeoutBackoff = &v
	}
	if src.NoWorkBackoff != nil {
		v := *src.NoWorkBackoff
		dst.NoWorkBackoff = &v
	}
	if src.IdlePollInterval != nil {
		v := *src.IdlePollInterval
		dst.IdlePollInterval = &v
	}
	if src.YieldTimeout != nil {
		v := *src.YieldTimeout
		dst.YieldTimeout = &v
	}
	if src.SigtermTimeout != nil {
		v := *src.SigtermTimeout
		dst.SigtermTimeout = &v
	}
	return dst
}

// resolvedConfigForDisplay returns a copy of cfg with secrets masked and
// RestartPolicy defaults filled in. The original cfg is not mutated.
func resolvedConfigForDisplay(cfg *DaemonConfig) DaemonConfig {
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
	if cfg.Daemon.FleetDB != nil {
		fleetCopy := *cfg.Daemon.FleetDB
		out.Daemon.FleetDB = &fleetCopy
	}
	if cfg.Daemon.Fleet != nil {
		fleetClientCopy := *cfg.Daemon.Fleet
		out.Daemon.Fleet = &fleetClientCopy
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
