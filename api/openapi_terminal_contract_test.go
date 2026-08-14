package api_test

import (
	"slices"
	"testing"
)

func TestTerminalReplayControlIsCanonicalAndBounded(t *testing.T) {
	doc := readOpenAPI(t)
	schema := doc.Components.Schemas["TerminalReplayControl"]
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		t.Fatal("TerminalReplayControl must reject additional properties")
	}
	required := stringSlice(t, schema, "required")
	if !slices.Equal(required, []string{"type", "columns", "rows"}) {
		t.Fatalf("TerminalReplayControl required = %v", required)
	}
	properties := childMap(t, schema, "properties")
	if got, _ := childMap(t, properties, "type")["const"].(string); got != "terminal.replay.resize" {
		t.Fatalf("TerminalReplayControl.type const = %q", got)
	}
	for name, maximum := range map[string]int{"columns": 500, "rows": 200} {
		property := childMap(t, properties, name)
		if got, _ := property["minimum"].(int); got != 1 {
			t.Errorf("TerminalReplayControl.%s minimum = %d", name, got)
		}
		if got, _ := property["maximum"].(int); got != maximum {
			t.Errorf("TerminalReplayControl.%s maximum = %d, want %d", name, got, maximum)
		}
	}
}

func TestTerminalOpenAPITracksOnlySurvivingRoutes(t *testing.T) {
	doc := readOpenAPI(t)
	required := map[string][]string{
		"/api/workspaces/{ws}/terminal/token":                 {"get"},
		"/api/workspaces/{ws}/terminal/ws":                    {"get"},
		"/api/workspaces/{ws}/terminal/tabs":                  {"get"},
		"/api/workspaces/{ws}/terminal/tabs/{session}":        {"get", "put", "patch", "delete"},
		"/api/workspaces/{ws}/terminal/sessions/by-issue":     {"get"},
		"/api/workspaces/{ws}/terminal/state":                 {"get", "patch"},
		"/api/workspaces/{ws}/terminal/setup":                 {"post"},
		"/api/workspaces/{ws}/agents/{name}/terminal/info":    {"get"},
		"/api/workspaces/{ws}/agents/{name}/terminal/token":   {"get"},
		"/api/workspaces/{ws}/agents/{name}/terminal/session": {"post"},
		"/api/workspaces/{ws}/agents/{name}/terminal/ws":      {"get"},
	}
	for path, methods := range required {
		item, ok := doc.Paths[path]
		if !ok {
			t.Errorf("surviving terminal path %s is missing", path)
			continue
		}
		for _, method := range methods {
			if _, ok := item[method]; !ok {
				t.Errorf("surviving terminal operation %s %s is missing", method, path)
			}
		}
	}

	removed := []string{
		"/api/workspaces/{ws}/terminal/sessions",
		"/api/workspaces/{ws}/terminal/session-status",
		"/api/workspaces/{ws}/terminal/spawn",
		"/api/workspaces/{ws}/terminal/restart",
		"/api/workspaces/{ws}/terminal/kill",
		"/api/workspaces/{ws}/terminal/sessions/{session}/seed",
		"/api/workspaces/{ws}/terminal/sessions/{session}/kill",
		"/api/workspaces/{ws}/terminal/sessions/close-all",
		"/api/workspaces/{ws}/terminal/scrollback",
		"/api/workspaces/{ws}/terminal/export",
		"/api/workspaces/{ws}/terminal/scrollback-info",
	}
	for _, path := range removed {
		if _, ok := doc.Paths[path]; ok {
			t.Errorf("removed tmux-era terminal path is still advertised: %s", path)
		}
	}
}
