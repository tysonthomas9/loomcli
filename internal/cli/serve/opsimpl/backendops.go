package opsimpl

import (
	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// BackendOpsImpl implements operationalview.BackendHealthQuery by inspecting the registered
// backend registry. The ops package owns the interface, cli provides this
// concrete implementation.
type BackendOpsImpl struct{}

var _ operationalview.BackendHealthQuery = (*BackendOpsImpl)(nil)

// NewBackendOps creates a new BackendOpsImpl.
func NewBackendOps() *BackendOpsImpl { return &BackendOpsImpl{} }

// BackendHealth returns readiness for one configured runtime provider without
// probing unrelated backends. It implements the Workflows HTTP adapter's
// consumer-owned health-query port at serve composition.
func (b *BackendOpsImpl) BackendHealth(name string) (operationalview.Backend, bool) {
	health, ok := backends.CheckBackendHealth(name)
	if !ok {
		return operationalview.Backend{}, false
	}
	return operationalview.Backend{
		Name: name, Available: health.Healthy, Installed: health.Installed,
		APIKeySet: health.APIKeySet, Version: health.Version, Message: health.Message,
	}, true
}

// ListBackendsHealth returns health/availability status for all registered backends.
func (b *BackendOpsImpl) ListBackendsHealth() ([]operationalview.Backend, error) {
	names := cli.ListBackends()
	result := make([]operationalview.Backend, 0, len(names))
	for _, name := range names {
		entry := operationalview.Backend{
			Name:      name,
			Available: false,
			Installed: false,
			APIKeySet: false,
		}

		backend, ok := cli.GetBackendByName(name)
		if !ok {
			result = append(result, entry)
			continue
		}

		// Assume available if registered; health check overrides below
		entry.Available = true
		entry.Installed = true
		entry.APIKeySet = true

		caps := backends.InspectCapabilities(backend)
		if caps.HasHealthCheck {
			health := caps.Health.HealthCheck()
			entry.Installed = health.Installed
			entry.APIKeySet = health.APIKeySet
			entry.Available = health.Healthy
			entry.Version = health.Version
			entry.Message = health.Message
			// Only call Meta() for DisplayName if HealthCheck didn't provide Version
			// (avoids redundant detectBinaryVersion subprocess)
			if caps.HasMeta {
				entry.DisplayName = caps.Meta.Meta().DisplayName
			}
		} else if caps.HasMeta {
			meta := caps.Meta.Meta()
			entry.DisplayName = meta.DisplayName
			entry.Version = meta.Version
		}

		result = append(result, entry)
	}
	return result, nil
}
