package flue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// projectRuntime records the state of a bootstrapped managed flue project so
// repeated loom invocations can skip install/build when nothing changed. It
// is written to <projectDir>/loom-runtime.json after a successful setup.
type projectRuntime struct {
	// TemplateVersion is computeTemplateHash() at the time of setup. A
	// mismatch with the current binary's embedded template triggers a full
	// re-scaffold + reinstall + rebuild.
	TemplateVersion string `json:"template_version"`
	// NodeVersion is the `node --version` string used for the last build.
	NodeVersion string `json:"node_version"`
	// PkgManager is the package manager used to install ("npm" or "pnpm").
	PkgManager  string    `json:"pkg_manager"`
	InstalledAt time.Time `json:"installed_at"`
	BuiltAt     time.Time `json:"built_at"`
}

// runtimeFileName is deliberately distinct from flue's own runtime.json
// (used by the long-lived server, Phase 2) so the two never collide.
const runtimeFileName = "loom-runtime.json"

func runtimePath(projectDir string) string {
	return filepath.Join(projectDir, runtimeFileName)
}

func readRuntime(projectDir string) (*projectRuntime, error) {
	data, err := os.ReadFile(runtimePath(projectDir)) //nolint:gosec // path under loom data dir
	if err != nil {
		return nil, err
	}
	var r projectRuntime
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("flue: parse %s: %w", runtimePath(projectDir), err)
	}
	return &r, nil
}

func writeRuntime(projectDir string, r projectRuntime) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("flue: marshal runtime: %w", err)
	}
	path := runtimePath(projectDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil { //nolint:gosec // user-private metadata
		return fmt.Errorf("flue: write runtime tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("flue: rename runtime %s: %w", path, err)
	}
	return nil
}
