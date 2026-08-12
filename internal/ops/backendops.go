package ops

// BackendOps defines the interface for backend discovery and health checking.
// This interface breaks the import cycle between webui and cli packages.
// The cli package provides the concrete implementation.
type BackendOps interface {
	ListBackendsHealth() ([]BackendHealth, error)
	BackendHealth(name string) (BackendHealth, bool)
}

// BackendHealth describes a single backend's availability and metadata.
type BackendHealth struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Available   bool   `json:"available"`
	Installed   bool   `json:"installed"`
	APIKeySet   bool   `json:"api_key_set"`
	Version     string `json:"version,omitempty"`
	Message     string `json:"message,omitempty"`
}
