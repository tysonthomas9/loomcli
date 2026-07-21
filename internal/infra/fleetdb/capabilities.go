package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	// CapabilitiesAPIPath is the deployment-level FleetDB capability manifest.
	// It is intentionally workspace-independent: readiness must fail before a
	// capability-specific mutation is attempted in any workspace.
	CapabilitiesAPIPath = "/api/v1/capabilities"

	// SupportedCapabilitiesAPIRevision is the manifest schema this client can
	// interpret. Capability keys carry their own versions independently.
	SupportedCapabilitiesAPIRevision = "v1"
)

// CapabilityIncompatibilityKind identifies why the running FleetDB deployment
// cannot satisfy the capabilities required by the enabled Loom slices.
type CapabilityIncompatibilityKind string

const (
	CapabilityEndpointUnavailable CapabilityIncompatibilityKind = "endpoint_unavailable"
	CapabilityRevisionUnsupported CapabilityIncompatibilityKind = "revision_unsupported"
	CapabilityKeysMissing         CapabilityIncompatibilityKind = "keys_missing"
)

// CapabilityIncompatibilityError is returned when FleetDB is reachable but its
// deployment capability manifest is absent or incompatible. Callers should use
// errors.As rather than parsing Error. Required and Missing are normalized,
// sorted, and unique so startup failures are stable and actionable.
type CapabilityIncompatibilityError struct {
	Kind        CapabilityIncompatibilityKind
	APIRevision string
	Required    []string
	Missing     []string
	Cause       error
}

func (e *CapabilityIncompatibilityError) Error() string {
	if e == nil {
		return "fleetdb: incompatible deployment"
	}
	required := strings.Join(e.Required, ", ")
	switch e.Kind {
	case CapabilityEndpointUnavailable:
		return fmt.Sprintf(
			"fleetdb: incompatible deployment: capabilities endpoint %s is unavailable (HTTP 404); required capabilities: %s",
			CapabilitiesAPIPath,
			required,
		)
	case CapabilityRevisionUnsupported:
		return fmt.Sprintf(
			"fleetdb: incompatible deployment: capabilities API revision %q is unsupported (want %q); required capabilities: %s",
			e.APIRevision,
			SupportedCapabilitiesAPIRevision,
			required,
		)
	case CapabilityKeysMissing:
		return fmt.Sprintf(
			"fleetdb: incompatible deployment: capabilities API revision %q is missing required capabilities: %s",
			e.APIRevision,
			strings.Join(e.Missing, ", "),
		)
	default:
		return fmt.Sprintf("fleetdb: incompatible deployment: required capabilities: %s", required)
	}
}

// Unwrap retains the underlying HTTP 404 for diagnostics while the typed error
// remains the authoritative classification for startup/readiness handling.
func (e *CapabilityIncompatibilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type capabilityManifest struct {
	APIRevision  string   `json:"api_revision"`
	Capabilities []string `json:"capabilities"`
}

// RequireCapabilities verifies that the running FleetDB deployment advertises
// every caller-supplied capability key. An empty normalized requirement set is
// a strict no-op, preserving compatibility for Loom configurations that have
// not enabled a capability-negotiated slice.
func (c *Client) RequireCapabilities(ctx context.Context, requiredKeys []string) error {
	required := normalizeCapabilityKeys(requiredKeys)
	if len(required) == 0 {
		return nil
	}

	var manifest capabilityManifest
	if err := c.do(ctx, http.MethodGet, CapabilitiesAPIPath, nil, &manifest); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &CapabilityIncompatibilityError{
				Kind:     CapabilityEndpointUnavailable,
				Required: required,
				Cause:    err,
			}
		}
		return fmt.Errorf(
			"fleetdb: check required capabilities %s: %w",
			strings.Join(required, ", "),
			err,
		)
	}

	manifest.APIRevision = strings.TrimSpace(manifest.APIRevision)
	if manifest.APIRevision != SupportedCapabilitiesAPIRevision {
		return &CapabilityIncompatibilityError{
			Kind:        CapabilityRevisionUnsupported,
			APIRevision: manifest.APIRevision,
			Required:    required,
		}
	}

	available := normalizeCapabilityKeys(manifest.Capabilities)
	missing := missingCapabilityKeys(required, available)
	if len(missing) != 0 {
		return &CapabilityIncompatibilityError{
			Kind:        CapabilityKeysMissing,
			APIRevision: manifest.APIRevision,
			Required:    required,
			Missing:     missing,
		}
	}
	return nil
}

func normalizeCapabilityKeys(keys []string) []string {
	unique := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			unique[key] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(unique))
	for key := range unique {
		normalized = append(normalized, key)
	}
	sort.Strings(normalized)
	return normalized
}

func missingCapabilityKeys(required, available []string) []string {
	availableSet := make(map[string]struct{}, len(available))
	for _, key := range available {
		availableSet[key] = struct{}{}
	}
	missing := make([]string, 0)
	for _, key := range required {
		if _, ok := availableSet[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}
