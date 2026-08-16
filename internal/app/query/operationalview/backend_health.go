// Package operationalview defines the immutable local backend availability
// projection used by delivery.
package operationalview

// BackendHealthQuery exposes backend discovery without exposing backend construction or
// mutation mechanisms.
type BackendHealthQuery interface {
	ListBackendsHealth() ([]Backend, error)
	BackendHealth(name string) (Backend, bool)
}

// Backend describes one configured backend's current availability.
type Backend struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Available   bool   `json:"available"`
	Installed   bool   `json:"installed"`
	APIKeySet   bool   `json:"api_key_set"`
	Version     string `json:"version,omitempty"`
	Message     string `json:"message,omitempty"`
}
