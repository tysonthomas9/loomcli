package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const fleetEventPath = "internal/models/event.go"

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

func resolveFleetDBPath(repoRoot, explicit string) (string, error) {
	if explicit != "" {
		return requireFleetDB(explicit)
	}
	for _, candidate := range directFleetDBCandidates(repoRoot) {
		if isFleetDB(candidate) {
			return filepath.Clean(candidate), nil
		}
	}
	canonical := findFleetDBInAncestor(repoRoot)
	if canonical == "" {
		return "", fmt.Errorf("FleetDB checkout not found; pass -fleet-db /path/to/fleet-db")
	}
	return canonical, nil
}

func directFleetDBCandidates(repoRoot string) []string {
	return []string{
		filepath.Join(filepath.Dir(repoRoot), "fleet-db"),
		filepath.Join(repoRoot, "fleet-db"),
		filepath.Join(filepath.Dir(filepath.Dir(repoRoot)), "fleet-db"),
	}
}

func findFleetDBInAncestor(repoRoot string) string {
	for dir := filepath.Dir(repoRoot); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "fleet-db")
		if isFleetDB(candidate) {
			return filepath.Clean(candidate)
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func requireFleetDB(path string) (string, error) {
	cleaned, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve FleetDB path %s: %w", path, err)
	}
	if !isFleetDB(cleaned) {
		return "", fmt.Errorf("FleetDB event model not found at %s", filepath.Join(cleaned, fleetEventPath))
	}
	return cleaned, nil
}

func isFleetDB(path string) bool {
	return fileExists(filepath.Join(path, fleetEventPath))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func gitRevision(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "HEAD") //nolint:gosec,norawexec // Fixed git query against the selected source checkout.
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read FleetDB git revision from %s: %w", path, err)
	}
	return strings.TrimSpace(string(output)), nil
}
