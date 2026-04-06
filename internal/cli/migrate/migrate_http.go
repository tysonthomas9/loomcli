package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// HTTP helper functions for fleet migration

func fleetGet(cfg *migrateConfig, client *http.Client, path string) (*http.Response, error) {
	url := strings.TrimRight(cfg.fleetURL, "/") + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setFleetHeaders(cfg, req)
	return doFleetRequest(client, req)
}

func fleetPost(cfg *migrateConfig, client *http.Client, path string, body interface{}) (*http.Response, error) {
	url := strings.TrimRight(cfg.fleetURL, "/") + path
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setFleetHeaders(cfg, req)
	return doFleetRequest(client, req)
}

func fleetPatch(cfg *migrateConfig, client *http.Client, path string, body interface{}) (*http.Response, error) {
	url := strings.TrimRight(cfg.fleetURL, "/") + path
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setFleetHeaders(cfg, req)
	return doFleetRequest(client, req)
}

func setFleetHeaders(cfg *migrateConfig, req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if cfg.apiKey != "" {
		req.Header.Set("X-Fleet-API-Key", cfg.apiKey)
	}
}

func doFleetRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode >= 400 && ct != "" && !strings.Contains(ct, "application/json") && !strings.Contains(ct, "text/plain") {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("fleet server returned non-JSON response (HTTP %d, Content-Type: %s): %s", resp.StatusCode, ct, truncate(string(body), 200))
	}

	return resp, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Config update helpers for fleet migration

// migrateUpdateConfig updates loom.yaml with fleet backend configuration.
func migrateUpdateConfig(cfg *migrateConfig) error {
	loomYamlPath := filepath.Join(cfg.projectDir, "loom.yaml")

	data, isNew, err := loadOrCreateLoomYAML(loomYamlPath)
	if err != nil {
		return err
	}
	if isNew {
		fmt.Println("  Created loom.yaml with fleet backend configuration.")
	}

	applyFleetConfig(data, cfg)

	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling loom.yaml: %w", err)
	}

	return os.WriteFile(loomYamlPath, out, 0644) //nolint:gosec // project config file
}

func loadOrCreateLoomYAML(path string) (map[string]interface{}, bool, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // project config file path
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, false, fmt.Errorf("reading loom.yaml: %w", err)
		}
		return map[string]interface{}{"version": 2}, true, nil
	}

	var data map[string]interface{}
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, false, fmt.Errorf("parsing loom.yaml: %w", err)
	}
	return data, false, nil
}

func applyFleetConfig(data map[string]interface{}, cfg *migrateConfig) {
	daemon, ok := data["daemon"].(map[string]interface{})
	if !ok {
		daemon = make(map[string]interface{})
	}

	oldBackend, _ := daemon["issue_backend"].(string)
	daemon["issue_backend"] = "fleet"

	fleet, ok := daemon["fleet"].(map[string]interface{})
	if !ok {
		fleet = make(map[string]interface{})
	}
	fleet["url"] = cfg.fleetURL
	fleet["workspace"] = cfg.workspace
	if cfg.apiKey != "" {
		fleet["api_key"] = cfg.apiKey
	}

	daemon["fleet"] = fleet
	data["daemon"] = daemon

	if oldBackend != "" && oldBackend != "fleet" {
		fmt.Printf("  Updated daemon.issue_backend from %q to \"fleet\" in loom.yaml.\n", oldBackend)
	}
}
