package domain

import "slices"

// FleetDBCapabilities is the document fleet-db serves at
// GET /api/v1/capabilities: the route families the *running binary* actually
// registered, computed by probing its own mux rather than read off a spec.
//
// The capability set is authoritative for compatibility. APIVersion describes
// only the shape of this document and Commit identifies the build; both are
// carried for humans reading a boot message and neither is compared.
type FleetDBCapabilities struct {
	// APIVersion is the version of the capability document's own shape.
	APIVersion int `json:"api_version"`
	// Commit is the fleet-db build the answer came from, "" when unknown.
	Commit string `json:"commit"`
	// Capabilities are the route-family names the server serves, sorted.
	Capabilities []string `json:"capabilities"`
}

// Has reports whether the server advertises the named capability.
//
// A server that advertises capabilities loom does not know about is still
// compatible: the check is a subset test, never equality.
func (c FleetDBCapabilities) Has(name string) bool {
	return slices.Contains(c.Capabilities, name)
}
