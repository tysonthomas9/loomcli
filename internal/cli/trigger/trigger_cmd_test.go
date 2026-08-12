package trigger

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// parseRouterFlags registers the Router v2 flag set on a throwaway command and
// parses args, returning the bound values and flag set for Changed() checks.
func parseRouterFlags(t *testing.T, args ...string) (*routerBindingFlags, *cobra.Command) {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	var f routerBindingFlags
	registerRouterBindingFlags(cmd, &f)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse flags %v: %v", args, err)
	}
	return &f, cmd
}

func TestRouterBindingFlagsValidate(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string // substring; empty means valid
	}{
		{name: "no flags", args: nil},
		{
			name: "all valid",
			args: []string{
				"--subject-key-template", "{{subject_ref}}/{{event_type}}/{{attrs.pr_number}}",
				"--concurrency-policy", "queue",
				"--actor-filter-exclude", "workflow",
				"--actor-filter-exclude", "bot",
				"--retry-max-attempts", "3",
				"--retry-backoff", "60",
				"--schedule", "*/5 * * * *",
				"--schedule-timezone", "Europe/Berlin",
				"--event-pattern", "github.pull_request.*",
				"--event-pattern", "github.{push,release}.published",
			},
		},
		{
			name:    "invalid concurrency policy",
			args:    []string{"--concurrency-policy", "serialize"},
			wantErr: `--concurrency-policy "serialize" is invalid`,
		},
		{name: "policy allow", args: []string{"--concurrency-policy", "allow"}},
		{name: "policy forbid", args: []string{"--concurrency-policy", "forbid"}},
		{name: "policy replace", args: []string{"--concurrency-policy", "replace"}},
		{name: "policy one_active_per_epic", args: []string{"--concurrency-policy", "one_active_per_epic"}},
		{
			name:    "malformed event pattern unclosed brace",
			args:    []string{"--event-pattern", "github.{push.opened"},
			wantErr: "--event-pattern",
		},
		{
			name:    "malformed event pattern star in alternation",
			args:    []string{"--event-pattern", "github.{push,*}.opened"},
			wantErr: "--event-pattern",
		},
		{
			name:    "subject template unterminated token",
			args:    []string{"--subject-key-template", "{{subject_ref"},
			wantErr: "unterminated token",
		},
		{
			name:    "subject template bad token",
			args:    []string{"--subject-key-template", "{{payload.title}}"},
			wantErr: `token "payload.title" is invalid`,
		},
		{
			name:    "subject template empty attrs name",
			args:    []string{"--subject-key-template", "{{attrs.}}"},
			wantErr: "is invalid",
		},
		{name: "subject template literal only", args: []string{"--subject-key-template", "static-key"}},
		{
			name:    "negative retry max attempts",
			args:    []string{"--retry-max-attempts=-1"},
			wantErr: "--retry-max-attempts must be non-negative",
		},
		{
			name:    "negative retry backoff",
			args:    []string{"--retry-backoff=-30"},
			wantErr: "--retry-backoff must be non-negative",
		},
		{
			name:    "bad timezone",
			args:    []string{"--schedule", "@hourly", "--schedule-timezone", "Mars/Olympus"},
			wantErr: "not a valid IANA timezone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := parseRouterFlags(t, tt.args...)
			err := f.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRouterBindingFlagsValidateWrapsPatternSentinel(t *testing.T) {
	f, _ := parseRouterFlags(t, "--event-pattern", "github.{push")
	err := f.validate()
	if !errors.Is(err, trigger.ErrInvalidPattern) {
		t.Fatalf("validate() = %v, want errors.Is ErrInvalidPattern", err)
	}
}

func TestRouterBindingFlagsValidateForCreate(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		args    []string
		wantErr string
	}{
		{name: "github source no schedule", source: "github"},
		{
			name:    "cron source requires schedule",
			source:  "cron",
			wantErr: `--schedule is required when --source is "cron"`,
		},
		{name: "cron source with schedule", source: "cron", args: []string{"--schedule", "0 9 * * 1-5"}},
		{
			name:    "timezone requires schedule",
			source:  "github",
			args:    []string{"--schedule-timezone", "UTC"},
			wantErr: "--schedule-timezone requires --schedule",
		},
		{
			name:    "per-field rules still applied",
			source:  "cron",
			args:    []string{"--schedule", "@daily", "--concurrency-policy", "nope"},
			wantErr: "--concurrency-policy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := parseRouterFlags(t, tt.args...)
			err := f.validateForCreate(tt.source)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateForCreate(%q) = %v, want nil", tt.source, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateForCreate(%q) = %v, want error containing %q", tt.source, err, tt.wantErr)
			}
		})
	}
}

func TestRouterBindingFlagsActorFilter(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string // nil means no filter
	}{
		{name: "unset", args: nil, want: nil},
		{name: "single", args: []string{"--actor-filter-exclude", "workflow"}, want: []string{"workflow"}},
		{
			name: "repeated and trimmed",
			args: []string{"--actor-filter-exclude", " workflow ", "--actor-filter-exclude", "bot"},
			want: []string{"workflow", "bot"},
		},
		{name: "blank values drop to nil", args: []string{"--actor-filter-exclude", ""}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := parseRouterFlags(t, tt.args...)
			got := f.actorFilter()
			if tt.want == nil {
				if got != nil {
					t.Fatalf("actorFilter() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("actorFilter() = nil, want exclude kinds %v", tt.want)
			}
			if strings.Join(got.ExcludeActorKinds, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("ExcludeActorKinds = %v, want %v", got.ExcludeActorKinds, tt.want)
			}
			if len(got.AllowActors) != 0 {
				t.Fatalf("AllowActors = %v, want empty", got.AllowActors)
			}
		})
	}
}

func TestRouterBindingFlagsPatch(t *testing.T) {
	t.Run("no flags is an error", func(t *testing.T) {
		f, cmd := parseRouterFlags(t)
		_, err := f.patch(cmd.Flags())
		if err == nil || !strings.Contains(err.Error(), "no fields to update") {
			t.Fatalf("patch() error = %v, want no-fields error", err)
		}
	})

	t.Run("invalid value rejected before patch", func(t *testing.T) {
		f, cmd := parseRouterFlags(t, "--concurrency-policy", "bogus")
		_, err := f.patch(cmd.Flags())
		if err == nil || !strings.Contains(err.Error(), "--concurrency-policy") {
			t.Fatalf("patch() error = %v, want concurrency-policy error", err)
		}
	})

	t.Run("only changed fields populated", func(t *testing.T) {
		f, cmd := parseRouterFlags(t,
			"--subject-key-template", "{{subject_ref}}",
			"--concurrency-policy", "replace",
			"--retry-max-attempts", "7",
		)
		patch, err := f.patch(cmd.Flags())
		if err != nil {
			t.Fatalf("patch() error = %v", err)
		}
		if patch.SubjectKeyTemplate == nil || *patch.SubjectKeyTemplate != "{{subject_ref}}" {
			t.Fatalf("SubjectKeyTemplate = %v, want {{subject_ref}}", patch.SubjectKeyTemplate)
		}
		if patch.ConcurrencyPolicy == nil || *patch.ConcurrencyPolicy != domain.TriggerBindingConcurrencyReplace {
			t.Fatalf("ConcurrencyPolicy = %v, want replace", patch.ConcurrencyPolicy)
		}
		if patch.RetryMaxAttempts == nil || *patch.RetryMaxAttempts != 7 {
			t.Fatalf("RetryMaxAttempts = %v, want 7", patch.RetryMaxAttempts)
		}
		for name, got := range map[string]any{
			"RetryBackoffSeconds": patch.RetryBackoffSeconds,
			"Schedule":            patch.Schedule,
			"ScheduleTimezone":    patch.ScheduleTimezone,
			"EventTypePatterns":   patch.EventTypePatterns,
			"ActorFilter":         patch.ActorFilter,
		} {
			switch v := got.(type) {
			case *int:
				if v != nil {
					t.Fatalf("%s = %v, want nil", name, *v)
				}
			case *string:
				if v != nil {
					t.Fatalf("%s = %v, want nil", name, *v)
				}
			case *[]string:
				if v != nil {
					t.Fatalf("%s = %v, want nil", name, *v)
				}
			case *domain.TriggerActorFilter:
				if v != nil {
					t.Fatalf("%s = %+v, want nil", name, v)
				}
			}
		}
	})

	t.Run("schedule and patterns", func(t *testing.T) {
		f, cmd := parseRouterFlags(t,
			"--schedule", "@hourly",
			"--schedule-timezone", "UTC",
			"--retry-backoff", "120",
			"--event-pattern", "github.issues.*",
		)
		patch, err := f.patch(cmd.Flags())
		if err != nil {
			t.Fatalf("patch() error = %v", err)
		}
		if patch.Schedule == nil || *patch.Schedule != "@hourly" {
			t.Fatalf("Schedule = %v, want @hourly", patch.Schedule)
		}
		if patch.ScheduleTimezone == nil || *patch.ScheduleTimezone != "UTC" {
			t.Fatalf("ScheduleTimezone = %v, want UTC", patch.ScheduleTimezone)
		}
		if patch.RetryBackoffSeconds == nil || *patch.RetryBackoffSeconds != 120 {
			t.Fatalf("RetryBackoffSeconds = %v, want 120", patch.RetryBackoffSeconds)
		}
		if patch.EventTypePatterns == nil || len(*patch.EventTypePatterns) != 1 || (*patch.EventTypePatterns)[0] != "github.issues.*" {
			t.Fatalf("EventTypePatterns = %v, want [github.issues.*]", patch.EventTypePatterns)
		}
	})

	t.Run("actor filter set", func(t *testing.T) {
		f, cmd := parseRouterFlags(t, "--actor-filter-exclude", "workflow")
		patch, err := f.patch(cmd.Flags())
		if err != nil {
			t.Fatalf("patch() error = %v", err)
		}
		if patch.ActorFilter == nil || len(patch.ActorFilter.ExcludeActorKinds) != 1 || patch.ActorFilter.ExcludeActorKinds[0] != "workflow" {
			t.Fatalf("ActorFilter = %+v, want exclude [workflow]", patch.ActorFilter)
		}
	})

	t.Run("blank actor filter clears via zero filter", func(t *testing.T) {
		f, cmd := parseRouterFlags(t, "--actor-filter-exclude", "")
		patch, err := f.patch(cmd.Flags())
		if err != nil {
			t.Fatalf("patch() error = %v", err)
		}
		if !patch.ClearActorFilter || patch.ActorFilter != nil {
			t.Fatalf("ClearActorFilter = %t ActorFilter = %+v, want explicit clear", patch.ClearActorFilter, patch.ActorFilter)
		}
	})
}

func TestNewBindingCreateRequestCarriesRouterFields(t *testing.T) {
	// newBindingCreateRequest reads the package-level create flag state; set it
	// directly and restore afterwards so other tests see clean state.
	savedRouter, savedDriver, savedVersion, savedDisabled := bindCreateRouter, bindCreateDriver, bindCreateVersion, bindCreateDisabled
	t.Cleanup(func() {
		bindCreateRouter, bindCreateDriver, bindCreateVersion, bindCreateDisabled = savedRouter, savedDriver, savedVersion, savedDisabled
	})
	bindCreateRouter = routerBindingFlags{
		subjectKeyTemplate: " {{subject_ref}} ",
		concurrencyPolicy:  "queue",
		actorExclude:       []string{"workflow"},
		retryMaxAttempts:   9,
		retryBackoffSecs:   45,
		schedule:           " */10 * * * * ",
		scheduleTimezone:   " UTC ",
		patterns:           []string{"github.pull_request.*"},
	}
	bindCreateDriver = "drv-1"
	bindCreateVersion = "ver-1"
	bindCreateDisabled = false
	in := newBindingCreateRequest("github.pull_request.opened", "github")
	enabled := true
	want := triggerBindingCreateRequest{
		BindingID:           "binding-github-pull_request-opened",
		Name:                "github.pull_request.opened",
		SourceKind:          "github",
		RouteKey:            "github.pull_request.opened",
		EventTypePatterns:   []string{"github.pull_request.*"},
		DriverID:            "drv-1",
		DriverVersionID:     "ver-1",
		ConcurrencyPolicy:   domain.TriggerBindingConcurrencyQueue,
		SubjectKeyTemplate:  "{{subject_ref}}",
		RetryMaxAttempts:    9,
		RetryBackoffSeconds: 45,
		Schedule:            "*/10 * * * *",
		ScheduleTimezone:    "UTC",
		Enabled:             &enabled,
	}
	if in.ActorFilter == nil || len(in.ActorFilter.ExcludeActorKinds) != 1 || in.ActorFilter.ExcludeActorKinds[0] != "workflow" {
		t.Fatalf("ActorFilter = %+v, want exclude [workflow]", in.ActorFilter)
	}
	in.ActorFilter = nil
	if len(in.EventTypePatterns) != 1 || in.EventTypePatterns[0] != want.EventTypePatterns[0] {
		t.Fatalf("EventTypePatterns = %v, want %v", in.EventTypePatterns, want.EventTypePatterns)
	}
	in.EventTypePatterns, want.EventTypePatterns = nil, nil
	if !reflect.DeepEqual(in, want) {
		t.Fatalf("newBindingCreateRequest =\n%+v\nwant\n%+v", in, want)
	}
}

// TestRenderBindingsListGolden pins the human-readable `trigger bindings list`
// output, including the new Router v2 columns, so format drift is deliberate.
func TestRenderBindingsListGolden(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	bindings := []*domain.TriggerBinding{
		{
			BindingID:           "binding-github-pr-opened",
			RouteKey:            "github.pull_request.opened",
			DriverID:            "drv-epic-runner",
			ConcurrencyPolicy:   domain.TriggerBindingConcurrencyOneActivePerEpic,
			RetryMaxAttempts:    5,
			RetryBackoffSeconds: 30,
			Enabled:             true,
			CreatedAt:           now,
			UpdatedAt:           now,
		},
		{
			BindingID:           "binding-nightly-report",
			SourceKind:          "cron",
			DriverID:            "drv-reporter",
			ConcurrencyPolicy:   domain.TriggerBindingConcurrencyForbid,
			RetryMaxAttempts:    2,
			RetryBackoffSeconds: 60,
			Schedule:            "0 3 * * *",
			ScheduleTimezone:    "Europe/Berlin",
			SubjectKeyTemplate:  "{{subject_ref}}/{{event_type}}",
			Enabled:             true,
			CreatedAt:           now,
			UpdatedAt:           now,
		},
		{
			BindingID: "binding-legacy",
			RouteKey:  "github.push",
			DriverID:  "drv-ci",
			Enabled:   false,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	const golden = "binding-github-pr-opened     route=github.pull_request.opened       driver=drv-epic-runner      policy=one_active_per_epic retry=5/30s enabled=true\n" +
		"binding-nightly-report       route=                                 driver=drv-reporter         policy=forbid              retry=2/60s enabled=true schedule=\"0 3 * * *\" tz=Europe/Berlin subject-template={{subject_ref}}/{{event_type}}\n" +
		"binding-legacy               route=github.push                      driver=drv-ci               policy=-                   retry=0/0s enabled=false\n"

	var sb strings.Builder
	renderBindingsList(&sb, bindings)
	if sb.String() != golden {
		t.Fatalf("bindings list output drifted.\ngot:\n%s\nwant:\n%s", sb.String(), golden)
	}

	sb.Reset()
	renderBindingsList(&sb, nil)
	if sb.String() != "No trigger bindings.\n" {
		t.Fatalf("empty list output = %q, want %q", sb.String(), "No trigger bindings.\n")
	}
}
