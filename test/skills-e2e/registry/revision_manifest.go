package registry

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var exactGitRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

// CompatibilityRevisionManifest is the exact source and runtime identity of
// one public provider compatibility run.
type CompatibilityRevisionManifest struct {
	LoomRevision   string
	FleetRevision  string
	CorpusRevision string
	Backend        Backend
	Provider       Provider
}

// ValidateCompatibilityRevisionManifest proves that the persisted manifest
// names the checkouts and actual adapters supplied by the running test.
func ValidateCompatibilityRevisionManifest(r io.Reader, want CompatibilityRevisionManifest) error {
	if err := validateCompatibilityRevisionManifest(want); err != nil {
		return fmt.Errorf("expected revision manifest: %w", err)
	}
	fields := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("invalid revision manifest line %q", line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, duplicate := fields[key]; duplicate {
			return fmt.Errorf("duplicate revision manifest field %q", key)
		}
		fields[key] = value
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read revision manifest: %w", err)
	}

	checks := map[string]string{
		"loomcli_sha":         want.LoomRevision,
		"fleetdb_sha":         want.FleetRevision,
		"vercel_skills_sha":   want.CorpusRevision,
		"persistence_backend": string(want.Backend),
		"object_provider":     string(want.Provider),
	}
	for key, expected := range checks {
		if actual := fields[key]; actual != expected {
			return fmt.Errorf("revision manifest %s = %q, want %q", key, actual, expected)
		}
	}
	return nil
}

func validateCompatibilityRevisionManifest(manifest CompatibilityRevisionManifest) error {
	for name, revision := range map[string]string{
		"loomcli_sha": manifest.LoomRevision, "fleetdb_sha": manifest.FleetRevision, "vercel_skills_sha": manifest.CorpusRevision,
	} {
		if !exactGitRevision.MatchString(revision) {
			return fmt.Errorf("%s must be an exact lowercase 40-character Git revision", name)
		}
	}
	return validateCoordinate(EvidenceCoordinate{Repository: RepositoryLoom, Backend: manifest.Backend, Provider: manifest.Provider})
}
