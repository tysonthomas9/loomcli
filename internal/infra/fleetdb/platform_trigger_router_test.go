package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// TestTriggerBindingRouterFieldsWire pins the snake_case fleet-db v1 wire for
// the Router v2 binding fields on create and update request bodies, plus the
// response decode path (domain JSON tags).
func TestTriggerBindingRouterFieldsWire(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-bindings":
			var body map[string]any
			decodeJSONBody(t, r, &body)
			checkRouterCreateBody(t, body)
			writeJSON(t, w, automation.Binding{
				WorkspaceKey:        "WS",
				BindingID:           "binding-router",
				SourceKind:          "cron",
				DriverID:            "driver-1",
				DriverVersionID:     "version-1",
				SubjectKeyTemplate:  "{{subject_ref}}|{{attrs.repo}}",
				ActorFilter:         &automation.ActorFilter{ExcludeActorKinds: []string{"workflow"}, AllowActors: []string{"agent:lead"}},
				RetryMaxAttempts:    3,
				RetryBackoffSeconds: 60,
				Schedule:            "*/5 * * * *",
				ScheduleTimezone:    "Europe/Berlin",
				Enabled:             true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS2/trigger-bindings":
			var body map[string]any
			decodeJSONBody(t, r, &body)
			if _, ok := body["actor_filter"]; ok {
				t.Fatalf("create without filter sent actor_filter = %v", body["actor_filter"])
			}
			writeJSON(t, w, automation.Binding{WorkspaceKey: "WS2", BindingID: "binding-plain", DriverID: "driver-1", DriverVersionID: "version-1", Enabled: true})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/trigger-bindings/binding-router":
			var body map[string]any
			decodeJSONBody(t, r, &body)
			checkRouterUpdateBody(t, body)
			writeJSON(t, w, automation.Binding{WorkspaceKey: "WS", BindingID: "binding-router", SubjectKeyTemplate: "{{event_type}}", RetryMaxAttempts: 7, Enabled: true})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/trigger-bindings/binding-untouched":
			var body map[string]any
			decodeJSONBody(t, r, &body)
			for _, key := range []string{"subject_key_template", "actor_filter", "retry_max_attempts", "retry_backoff_seconds", "schedule", "schedule_timezone"} {
				if _, ok := body[key]; ok {
					t.Fatalf("router-field-free patch sent %q = %v", key, body[key])
				}
			}
			writeJSON(t, w, automation.Binding{WorkspaceKey: "WS", BindingID: "binding-untouched", Name: "renamed", Enabled: true})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}

	created, err := client.TriggerBindings().Create(t.Context(), automation.TriggerBindingCreate{
		WorkspaceKey:        "WS",
		BindingID:           "binding-router",
		Name:                "Router binding",
		SourceKind:          "cron",
		DriverID:            "driver-1",
		DriverVersionID:     "version-1",
		SubjectKeyTemplate:  "{{subject_ref}}|{{attrs.repo}}",
		ActorFilter:         &automation.ActorFilter{ExcludeActorKinds: []string{"workflow"}, AllowActors: []string{"agent:lead"}},
		RetryMaxAttempts:    3,
		RetryBackoffSeconds: 60,
		Schedule:            "*/5 * * * *",
		ScheduleTimezone:    "Europe/Berlin",
		Enabled:             true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.SubjectKeyTemplate != "{{subject_ref}}|{{attrs.repo}}" || created.ActorFilter.IsZero() || created.RetryMaxAttempts != 3 || created.RetryBackoffSeconds != 60 || created.Schedule != "*/5 * * * *" || created.ScheduleTimezone != "Europe/Berlin" {
		t.Fatalf("created binding response = %+v, want router fields decoded", created)
	}

	if _, err := client.TriggerBindings().Create(t.Context(), automation.TriggerBindingCreate{
		WorkspaceKey:    "WS2",
		BindingID:       "binding-plain",
		Name:            "Plain binding",
		SourceKind:      "http",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		Enabled:         true,
	}); err != nil {
		t.Fatalf("Create plain: %v", err)
	}

	template := "{{event_type}}"
	attempts := 7
	clearFilter := automation.ActorFilter{}
	updated, err := client.TriggerBindings().Update(t.Context(), "WS", "binding-router", automation.TriggerBindingUpdate{
		SubjectKeyTemplate: &template,
		ActorFilter:        &clearFilter,
		RetryMaxAttempts:   &attempts,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.SubjectKeyTemplate != template || updated.RetryMaxAttempts != attempts {
		t.Fatalf("updated binding response = %+v", updated)
	}

	name := "renamed"
	if _, err := client.TriggerBindings().Update(t.Context(), "WS", "binding-untouched", automation.TriggerBindingUpdate{Name: &name}); err != nil {
		t.Fatalf("Update untouched: %v", err)
	}
}

// checkRouterCreateBody asserts the snake_case keys for the Router v2 fields
// on the create request body.
func checkRouterCreateBody(t *testing.T, body map[string]any) {
	t.Helper()
	want := map[string]any{
		"subject_key_template":  "{{subject_ref}}|{{attrs.repo}}",
		"retry_max_attempts":    float64(3),
		"retry_backoff_seconds": float64(60),
		"schedule":              "*/5 * * * *",
		"schedule_timezone":     "Europe/Berlin",
	}
	for key, value := range want {
		if body[key] != value {
			t.Fatalf("create body[%q] = %v, want %v", key, body[key], value)
		}
	}
	filter, ok := body["actor_filter"].(map[string]any)
	if !ok {
		t.Fatalf("create body actor_filter = %v, want object", body["actor_filter"])
	}
	exclude, ok := filter["exclude_actor_kinds"].([]any)
	if !ok || len(exclude) != 1 || exclude[0] != "workflow" {
		t.Fatalf("actor_filter.exclude_actor_kinds = %v, want [workflow]", filter["exclude_actor_kinds"])
	}
	allow, ok := filter["allow_actors"].([]any)
	if !ok || len(allow) != 1 || allow[0] != "agent:lead" {
		t.Fatalf("actor_filter.allow_actors = %v, want [agent:lead]", filter["allow_actors"])
	}
}

// checkRouterUpdateBody asserts the patch carries exactly the set router
// fields, with an empty actor_filter object as the clear sentinel.
func checkRouterUpdateBody(t *testing.T, body map[string]any) {
	t.Helper()
	if body["subject_key_template"] != "{{event_type}}" {
		t.Fatalf("update body subject_key_template = %v", body["subject_key_template"])
	}
	if body["retry_max_attempts"] != float64(7) {
		t.Fatalf("update body retry_max_attempts = %v", body["retry_max_attempts"])
	}
	filter, ok := body["actor_filter"].(map[string]any)
	if !ok || len(filter) != 0 {
		t.Fatalf("update body actor_filter = %v, want empty object", body["actor_filter"])
	}
	for _, key := range []string{"retry_backoff_seconds", "schedule", "schedule_timezone"} {
		if _, present := body[key]; present {
			t.Fatalf("update body unexpectedly carries %q = %v", key, body[key])
		}
	}
}
