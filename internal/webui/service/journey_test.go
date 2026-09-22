package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	loomevents "github.com/tysonthomas9/loomcli/internal/events"
)

var journeyTestStart = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

type journeyWorkspaceContextKey struct{}

type journeyHistoryBackend struct {
	*fakeIssueBackend
	events      []backend.EventData
	listHistory func(backend.EventHistoryParams) (*backend.EventHistoryData, error)
}

func (b *journeyHistoryBackend) ListEventHistory(_ context.Context, id string, params backend.EventHistoryParams) (*backend.EventHistoryData, error) {
	if b.listHistory != nil {
		return b.listHistory(params)
	}
	if params.Since == nil {
		start := max(0, len(b.events)-params.Limit)
		return &backend.EventHistoryData{
			Events:      append([]backend.EventData(nil), b.events[start:]...),
			HasMore:     start > 0,
			TotalEvents: len(b.events),
		}, nil
	}

	start := 0
	if *params.Since != "" {
		var err error
		start, err = strconv.Atoi(*params.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid test cursor %q: %w", *params.Since, err)
		}
	}
	end := min(start+params.Limit, len(b.events))
	return &backend.EventHistoryData{
		Events:      append([]backend.EventData(nil), b.events[start:end]...),
		Cursor:      strconv.Itoa(end),
		HasMore:     end < len(b.events),
		TotalEvents: len(b.events),
	}, nil
}

func journeyIssueEvent(seconds int, action, actor string, changes ...backend.FieldChange) backend.EventData {
	return backend.EventData{
		ID:        fmt.Sprintf("event-%02d", seconds),
		IssueID:   "task-1",
		Kind:      action,
		Actor:     actor,
		Changes:   changes,
		CreatedAt: journeyTestStart.Add(time.Duration(seconds) * time.Second),
	}
}

func writeJourneyLifecycleEvents(t *testing.T, eventList ...loomevents.Event) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "events-2026-08-20.jsonl")
	file, err := os.Create(path) // #nosec G304 -- test-owned temporary path
	if err != nil {
		t.Fatalf("create lifecycle event file: %v", err)
	}
	encoder := json.NewEncoder(file)
	for _, event := range eventList {
		if err := encoder.Encode(event); err != nil {
			_ = file.Close()
			t.Fatalf("encode lifecycle event: %v", err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close lifecycle event file: %v", err)
	}
	return dir
}

func journeyLifecycleEvent(t *testing.T, seconds int, eventType loomevents.EventType, agent string, data any) loomevents.Event {
	t.Helper()
	event, err := loomevents.NewEvent(eventType, agent, "dev", "", data)
	if err != nil {
		t.Fatalf("create lifecycle event: %v", err)
	}
	event.Timestamp = journeyTestStart.Add(time.Duration(seconds) * time.Second)
	return event
}

func TestGetJourney_FullClosedTraceDecomposesLeadTime(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
			journeyIssueEvent(20, "issue.update", "agent-dev-1", backend.FieldChange{Field: "status", Before: "in_progress", After: "blocked"}),
			journeyIssueEvent(30, "issue.update", "operator", backend.FieldChange{Field: "status", Before: "blocked", After: "in_progress"}),
			journeyIssueEvent(40, "issue.update", "agent-dev-1", backend.FieldChange{Field: "status", Before: "in_progress", After: "review"}),
			journeyIssueEvent(50, "issue.close", "operator"),
		},
	}
	eventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 10, loomevents.TaskClaimed, "agent-dev-1", loomevents.TaskClaimedData{TaskID: "task-1"}),
		journeyLifecycleEvent(t, 50, loomevents.TaskCompleted, "agent-dev-1", loomevents.TaskCompletedData{TaskID: "task-1"}),
	)
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{EventsDir: eventsDir, Now: func() time.Time { return journeyTestStart.Add(60 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}

	gotStages := make([]string, 0, len(journey.Spans))
	for _, span := range journey.Spans {
		gotStages = append(gotStages, span.Stage)
	}
	if want := []string{"open", "in_progress", "blocked", "in_progress", "review", "closed"}; !reflect.DeepEqual(gotStages, want) {
		t.Fatalf("stages = %v, want %v", gotStages, want)
	}
	if got, want := journey.LeadTime, (JourneyLeadTime{
		TotalMS:             50_000,
		QueuedMS:            10_000,
		AgentWorkingMS:      20_000,
		WaitingOnOperatorMS: 10_000,
		HaltedMS:            10_000,
	}); got != want {
		t.Errorf("lead time = %+v, want %+v", got, want)
	}
	if len(journey.AgentWindows) != 1 {
		t.Fatalf("agent windows = %d, want 1", len(journey.AgentWindows))
	}
	if got := journey.AgentWindows[0]; got.TaskID != "task-1" || got.Agent != "agent-dev-1" || got.Outcome != "completed" || got.End == nil {
		t.Errorf("agent window = %+v", got)
	}
	if !journey.Honesty.CompleteHistory || journey.Honesty.Bounded || journey.Honesty.HasMore {
		t.Errorf("history honesty = %+v, want complete", journey.Honesty)
	}
	if !journey.Honesty.AgentWindowsAvailable {
		t.Error("agent_windows_available = false, want true")
	}
}

func TestGetJourney_InFlightUsesInjectedClock(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
		},
	}
	eventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 10, loomevents.TaskClaimed, "agent-dev-1", loomevents.TaskClaimedData{TaskID: "task-1"}),
	)
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{EventsDir: eventsDir, Now: func() time.Time { return journeyTestStart.Add(40 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if got := journey.Spans[len(journey.Spans)-1]; got.Stage != "in_progress" || got.End != nil {
		t.Errorf("final span = %+v, want open-ended in_progress", got)
	}
	if got, want := journey.LeadTime, (JourneyLeadTime{TotalMS: 40_000, QueuedMS: 10_000, AgentWorkingMS: 30_000}); got != want {
		t.Errorf("lead time = %+v, want %+v", got, want)
	}
	if len(journey.AgentWindows) != 1 || journey.AgentWindows[0].End != nil || journey.AgentWindows[0].Outcome != "running" {
		t.Errorf("agent windows = %+v, want one open running attempt", journey.AgentWindows)
	}
	if !journey.Honesty.AgentWindowsAvailable {
		t.Error("agent_windows_available = false, want true")
	}
}

func TestGetJourney_UsesEventsDirectoryForRequestWorkspace(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
		},
	}
	defaultEventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 10, loomevents.TaskClaimed, "default-agent", loomevents.TaskClaimedData{TaskID: "task-1"}),
	)
	workspaceBEventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 10, loomevents.TaskClaimed, "workspace-b-agent", loomevents.TaskClaimedData{TaskID: "task-1"}),
	)
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{
			EventsDir: defaultEventsDir,
			EventsDirForWorkspace: func(workspaceID string) string {
				if workspaceID == "workspace-b" {
					return workspaceBEventsDir
				}
				return ""
			},
			WorkspaceIDFromContext: func(ctx context.Context) string {
				workspaceID, _ := ctx.Value(journeyWorkspaceContextKey{}).(string)
				return workspaceID
			},
			Now: func() time.Time { return journeyTestStart.Add(20 * time.Second) },
		},
	)

	ctx := context.WithValue(context.Background(), journeyWorkspaceContextKey{}, "workspace-b")
	journey, err := svc.GetJourney(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if len(journey.AgentWindows) != 1 || journey.AgentWindows[0].Agent != "workspace-b-agent" {
		t.Errorf("agent windows = %+v, want workspace-b lifecycle window", journey.AgentWindows)
	}
}

func TestGetJourney_MissingLifecycleOverlayStillRendersStageFallback(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{EventsDir: t.TempDir(), Now: func() time.Time { return journeyTestStart.Add(40 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if len(journey.Spans) != 2 || len(journey.AgentWindows) != 0 {
		t.Errorf("journey = %+v, want stage-only history", journey)
	}
	if journey.Honesty.AgentWindowsAvailable || journey.LeadTime.AgentWorkingMS != 30_000 {
		t.Errorf("overlay honesty/working fallback = %+v / %+v", journey.Honesty, journey.LeadTime)
	}
	if journey.Honesty.AgentWindowsReason != "no_task_lifecycle_events" {
		t.Errorf("agent windows reason = %q, want no_task_lifecycle_events", journey.Honesty.AgentWindowsReason)
	}
}

func TestGetJourney_ReaperReleaseMarksStalledSpan(t *testing.T) {
	release := journeyIssueEvent(20, "issue.release", "system",
		backend.FieldChange{Field: "assignee", Before: "agent-dev-1", After: ""},
		backend.FieldChange{Field: "status", Before: "in_progress", After: "open"},
	)
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
			release,
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(30 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if got := journey.Spans[len(journey.Spans)-1]; got.Stage != "open" || got.Owner != nil || !got.Stalled {
		t.Errorf("post-release span = %+v, want stalled open/unassigned", got)
	}
	if journey.Honesty.AgentWindowsReason != "events_dir_not_configured" {
		t.Errorf("agent windows reason = %q, want events_dir_not_configured", journey.Honesty.AgentWindowsReason)
	}
}

func TestGetJourney_ManualReleaseIsNotStalled(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
			journeyIssueEvent(20, "issue.release", "operator"),
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(30 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if got := journey.Spans[len(journey.Spans)-1]; got.Stalled {
		t.Errorf("manual-release span = %+v, want stalled=false", got)
	}
}

func TestGetJourney_OpenLifecycleWindowStopsAtInProgressSpan(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(10, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
			journeyIssueEvent(3610, "issue.release", "system"),
		},
	}
	eventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 10, loomevents.TaskClaimed, "agent-dev-1", loomevents.TaskClaimedData{TaskID: "task-1"}),
	)
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{EventsDir: eventsDir, Now: func() time.Time { return journeyTestStart.Add(7210 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if got, want := journey.LeadTime, (JourneyLeadTime{
		TotalMS:        7_200_000,
		QueuedMS:       3_600_000,
		AgentWorkingMS: 3_600_000,
	}); got != want {
		t.Errorf("lead time = %+v, want %+v", got, want)
	}
}

func TestGetJourney_LifecycleHonestyReportsTerminalWithoutClaim(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events:           []backend.EventData{journeyIssueEvent(0, "issue.create", "operator")},
	}
	eventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 10, loomevents.TaskCompleted, "agent-dev-1", loomevents.TaskCompletedData{TaskID: "task-1"}),
	)
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{EventsDir: eventsDir, Now: func() time.Time { return journeyTestStart.Add(20 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if journey.Honesty.AgentWindowsAvailable || journey.Honesty.AgentWindowsReason != "no_claimed_window" {
		t.Errorf("agent window honesty = %+v, want no_claimed_window", journey.Honesty)
	}
}

func TestGetJourney_DeferActionWithoutChangesCreatesDeferredSpan(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.defer", "scheduler"),
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(30 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if gotStages := []string{journey.Spans[0].Stage, journey.Spans[1].Stage}; !reflect.DeepEqual(gotStages, []string{"open", "deferred"}) {
		t.Errorf("stages = %v, want [open deferred]", gotStages)
	}
}

func TestGetJourney_MultiAttemptKeepsTwoAgentWindows(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(5, "issue.claim", "agent-dev-1"),
			journeyIssueEvent(20, "issue.claim", "agent-dev-1"),
			journeyIssueEvent(40, "issue.close", "operator"),
		},
	}
	eventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 5, loomevents.TaskClaimed, "agent-dev-1", loomevents.TaskClaimedData{TaskID: "task-1"}),
		journeyLifecycleEvent(t, 15, loomevents.TaskFailed, "agent-dev-1", loomevents.TaskFailedData{TaskID: "task-1", Error: "first attempt failed"}),
		journeyLifecycleEvent(t, 20, loomevents.TaskClaimed, "agent-dev-1", loomevents.TaskClaimedData{TaskID: "task-1"}),
		journeyLifecycleEvent(t, 40, loomevents.TaskCompleted, "agent-dev-1", loomevents.TaskCompletedData{TaskID: "task-1"}),
	)
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{EventsDir: eventsDir, Now: func() time.Time { return journeyTestStart.Add(50 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if len(journey.AgentWindows) != 2 {
		t.Fatalf("agent windows = %d, want 2", len(journey.AgentWindows))
	}
	if got := []string{journey.AgentWindows[0].Outcome, journey.AgentWindows[1].Outcome}; !reflect.DeepEqual(got, []string{"failed", "completed"}) {
		t.Errorf("outcomes = %v, want [failed completed]", got)
	}
	if len(journey.Spans) != 4 || journey.Spans[1].Stage != "in_progress" || journey.Spans[2].Stage != "in_progress" {
		t.Errorf("attempt spans = %+v, want two distinct in_progress spans", journey.Spans)
	}
	if got, want := journey.LeadTime, (JourneyLeadTime{TotalMS: 40_000, QueuedMS: 5_000, AgentWorkingMS: 30_000}); got != want {
		t.Errorf("lead time = %+v, want %+v", got, want)
	}
}

func TestGetJourney_SupersedesUnclosedClaimWindow(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-a"),
		},
	}
	eventsDir := writeJourneyLifecycleEvents(t,
		journeyLifecycleEvent(t, 10, loomevents.TaskClaimed, "agent-a", loomevents.TaskClaimedData{TaskID: "task-1"}),
		journeyLifecycleEvent(t, 60, loomevents.TaskClaimed, "agent-a", loomevents.TaskClaimedData{TaskID: "task-1"}),
		journeyLifecycleEvent(t, 100, loomevents.TaskCompleted, "agent-a", loomevents.TaskCompletedData{TaskID: "task-1"}),
	)
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{EventsDir: eventsDir, Now: func() time.Time { return journeyTestStart.Add(160 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if len(journey.AgentWindows) != 2 {
		t.Fatalf("agent windows = %+v, want two attempts", journey.AgentWindows)
	}
	first, second := journey.AgentWindows[0], journey.AgentWindows[1]
	if first.End == nil || !first.End.Equal(journeyTestStart.Add(60*time.Second)) || first.Outcome != "superseded" {
		t.Errorf("first agent window = %+v, want [10,60) superseded", first)
	}
	if second.End == nil || !second.End.Equal(journeyTestStart.Add(100*time.Second)) || second.Outcome != "completed" {
		t.Errorf("second agent window = %+v, want [60,100) completed", second)
	}
	if got, want := journey.LeadTime, (JourneyLeadTime{TotalMS: 160_000, QueuedMS: 10_000, AgentWorkingMS: 90_000}); got != want {
		t.Errorf("lead time = %+v, want %+v", got, want)
	}
}

func TestGetJourney_PageCapReportsBoundedAndUnknownStart(t *testing.T) {
	eventList := make([]backend.EventData, 0, 501)
	eventList = append(eventList, journeyIssueEvent(0, "issue.create", "operator"))
	for second := 1; second < 500; second++ {
		eventList = append(eventList, journeyIssueEvent(second, "comment.add", "operator"))
	}
	eventList = append(eventList, journeyIssueEvent(500, "issue.claim", "agent-dev-1"))
	history := &journeyHistoryBackend{fakeIssueBackend: &fakeIssueBackend{}, events: eventList}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{
			Now:            func() time.Time { return journeyTestStart.Add(510 * time.Second) },
			HistoryPageCap: 1,
		},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if journey.Honesty.CompleteHistory || !journey.Honesty.Bounded || !journey.Honesty.HasMore {
		t.Errorf("history honesty = %+v, want bounded with more history", journey.Honesty)
	}
	if journey.Honesty.Reason != "history_page_cap_reached" || journey.Honesty.EventsSeen != 500 || journey.Honesty.TotalEvents != 501 {
		t.Errorf("history honesty detail = %+v", journey.Honesty)
	}
	if len(journey.Spans) != 1 || !journey.Spans[0].Approximate || !journey.Spans[0].UnknownStart {
		t.Errorf("earliest span = %+v, want approximate unknown start", journey.Spans)
	}
}

func TestGetJourney_PageFailureDegradesToBoundedHistory(t *testing.T) {
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		listHistory: func(params backend.EventHistoryParams) (*backend.EventHistoryData, error) {
			if params.Since == nil {
				return &backend.EventHistoryData{
					Events:      []backend.EventData{journeyIssueEvent(0, "issue.create", "operator")},
					HasMore:     true,
					TotalEvents: 2,
				}, nil
			}
			return nil, fmt.Errorf("fleet-db transport failed")
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(10 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if !journey.Honesty.Bounded || journey.Honesty.Reason != "history_paging_failed" {
		t.Errorf("history honesty = %+v, want bounded history_paging_failed", journey.Honesty)
	}
}

func TestGetJourney_EmptyHistoryVerifiesIssueExists(t *testing.T) {
	t.Run("unknown issue", func(t *testing.T) {
		history := &journeyHistoryBackend{fakeIssueBackend: &fakeIssueBackend{}}
		svc := NewIssueServiceWithBackend(nil, nil, nil,
			func(context.Context) backend.IssueBackend { return history },
			JourneyServiceConfig{Now: func() time.Time { return journeyTestStart }},
		)

		_, err := svc.GetJourney(context.Background(), "missing-task")
		if serviceErr, ok := err.(*ServiceError); !ok || serviceErr.Kind != KindNotFound {
			t.Fatalf("GetJourney error = %v, want not-found service error", err)
		}
	})

	t.Run("existing issue without events", func(t *testing.T) {
		history := &journeyHistoryBackend{
			fakeIssueBackend: &fakeIssueBackend{getResult: &backend.IssueDetailData{IssueData: backend.IssueData{ID: "task-1"}}},
		}
		svc := NewIssueServiceWithBackend(nil, nil, nil,
			func(context.Context) backend.IssueBackend { return history },
			JourneyServiceConfig{Now: func() time.Time { return journeyTestStart }},
		)

		journey, err := svc.GetJourney(context.Background(), "task-1")
		if err != nil {
			t.Fatalf("GetJourney: %v", err)
		}
		if !journey.Honesty.CompleteHistory || len(journey.Spans) != 0 || len(journey.AgentWindows) != 0 {
			t.Errorf("empty journey = %+v, want complete empty journey", journey)
		}
	})
}

func TestGetJourney_WalksCursorPagesToCompleteHistory(t *testing.T) {
	eventList := make([]backend.EventData, 0, 501)
	eventList = append(eventList, journeyIssueEvent(0, "issue.create", "operator"))
	for second := 1; second < 500; second++ {
		eventList = append(eventList, journeyIssueEvent(second, "comment.add", "operator"))
	}
	eventList = append(eventList, journeyIssueEvent(500, "issue.claim", "agent-dev-1"))
	history := &journeyHistoryBackend{fakeIssueBackend: &fakeIssueBackend{}, events: eventList}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(510 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if !journey.Honesty.CompleteHistory || journey.Honesty.Bounded || journey.Honesty.EventsSeen != 501 || journey.Honesty.TotalEvents != 501 {
		t.Errorf("history honesty = %+v, want all 501 events", journey.Honesty)
	}
	if journey.Spans[0].UnknownStart || journey.Spans[0].Approximate || journey.Spans[0].Stage != "open" {
		t.Errorf("first span = %+v, want exact open span", journey.Spans[0])
	}
}

func TestGetJourney_LeadTimeEndsAtFirstClosedSpan(t *testing.T) {
	label := journeyIssueEvent(200, "label.add", "operator")
	label.Metadata = map[string]string{"label": "needs-revision"}
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(10, "issue.claim", "agent-dev-1"),
			journeyIssueEvent(100, "issue.close", "operator"),
			label,
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(300 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if got := journey.LeadTime.TotalMS; got != 100_000 {
		t.Errorf("total lead time = %d, want 100000", got)
	}
}

func TestGetJourney_HistoryHonestyReasons(t *testing.T) {
	t.Run("paging unavailable", func(t *testing.T) {
		legacy := &fakeIssueBackend{listEventsResult: []backend.EventData{journeyIssueEvent(0, "issue.create", "operator")}}
		svc := NewIssueServiceWithBackend(nil, nil, nil,
			func(context.Context) backend.IssueBackend { return legacy },
			JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(10 * time.Second) }},
		)

		journey, err := svc.GetJourney(context.Background(), "task-1")
		if err != nil {
			t.Fatalf("GetJourney: %v", err)
		}
		if got := journey.Honesty.Reason; got != "history_paging_unavailable" {
			t.Errorf("history reason = %q, want history_paging_unavailable", got)
		}
	})

	t.Run("cursor stalled", func(t *testing.T) {
		history := &journeyHistoryBackend{
			fakeIssueBackend: &fakeIssueBackend{},
			listHistory: func(params backend.EventHistoryParams) (*backend.EventHistoryData, error) {
				if params.Since == nil {
					return &backend.EventHistoryData{Events: []backend.EventData{journeyIssueEvent(0, "issue.create", "operator")}, HasMore: true, TotalEvents: 2}, nil
				}
				return &backend.EventHistoryData{Cursor: "repeat", HasMore: true, TotalEvents: 2}, nil
			},
		}
		svc := NewIssueServiceWithBackend(nil, nil, nil,
			func(context.Context) backend.IssueBackend { return history },
			JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(10 * time.Second) }},
		)

		journey, err := svc.GetJourney(context.Background(), "task-1")
		if err != nil {
			t.Fatalf("GetJourney: %v", err)
		}
		if got := journey.Honesty.Reason; got != "history_cursor_stalled" {
			t.Errorf("history reason = %q, want history_cursor_stalled", got)
		}
	})

	t.Run("count mismatch", func(t *testing.T) {
		history := &journeyHistoryBackend{
			fakeIssueBackend: &fakeIssueBackend{},
			listHistory: func(params backend.EventHistoryParams) (*backend.EventHistoryData, error) {
				if params.Since == nil {
					return &backend.EventHistoryData{Events: []backend.EventData{journeyIssueEvent(0, "issue.create", "operator")}, HasMore: true, TotalEvents: 2}, nil
				}
				return &backend.EventHistoryData{Events: []backend.EventData{journeyIssueEvent(10, "issue.claim", "agent-dev-1")}, TotalEvents: 2}, nil
			},
		}
		svc := NewIssueServiceWithBackend(nil, nil, nil,
			func(context.Context) backend.IssueBackend { return history },
			JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(20 * time.Second) }},
		)

		journey, err := svc.GetJourney(context.Background(), "task-1")
		if err != nil {
			t.Fatalf("GetJourney: %v", err)
		}
		if got := journey.Honesty.Reason; got != "history_count_mismatch" {
			t.Errorf("history reason = %q, want history_count_mismatch", got)
		}
	})
}

func TestGetJourney_AssignmentPresenceAndReopenClearOwner(t *testing.T) {
	missingAssignment := journeyIssueEvent(10, "issue.assign", "dispatcher")
	emptyAssignment := journeyIssueEvent(15, "issue.assign", "dispatcher")
	emptyAssignment.Metadata = map[string]string{"assignee": ""}
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			journeyIssueEvent(5, "issue.claim", "agent-dev-1"),
			missingAssignment,
			emptyAssignment,
			journeyIssueEvent(20, "issue.reopen", "operator"),
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(30 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if len(journey.Spans) != 4 {
		t.Fatalf("spans = %+v, want four assignment-aware spans", journey.Spans)
	}
	if got := journey.Spans[1]; got.Owner == nil || *got.Owner != "agent-dev-1" || !got.End.Equal(journeyTestStart.Add(15*time.Second)) {
		t.Errorf("claimed span = %+v, want owner preserved through metadata-absent assign", got)
	}
	if journey.Spans[2].Owner != nil || journey.Spans[3].Owner != nil || journey.Spans[3].Stage != "open" {
		t.Errorf("unassign/reopen spans = %+v", journey.Spans[2:])
	}
}

func TestGetJourney_NeedsRevisionBounceCountsAsOperatorWait(t *testing.T) {
	addRevision := journeyIssueEvent(5, "label.add", "operator")
	addRevision.Metadata = map[string]string{"label": "needs-revision"}
	removeRevision := journeyIssueEvent(15, "label.remove", "operator")
	removeRevision.Metadata = map[string]string{"label": "needs-revision"}
	history := &journeyHistoryBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		events: []backend.EventData{
			journeyIssueEvent(0, "issue.create", "operator"),
			addRevision,
			removeRevision,
			journeyIssueEvent(20, "issue.claim", "agent-dev-1"),
			journeyIssueEvent(30, "issue.update", "agent-dev-1", backend.FieldChange{Field: "status", Before: "in_progress", After: "review"}),
			journeyIssueEvent(40, "issue.close", "operator"),
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil,
		func(context.Context) backend.IssueBackend { return history },
		JourneyServiceConfig{Now: func() time.Time { return journeyTestStart.Add(50 * time.Second) }},
	)

	journey, err := svc.GetJourney(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetJourney: %v", err)
	}
	if got, want := journey.LeadTime, (JourneyLeadTime{
		TotalMS:             40_000,
		QueuedMS:            10_000,
		AgentWorkingMS:      10_000,
		WaitingOnOperatorMS: 20_000,
	}); got != want {
		t.Errorf("lead time = %+v, want %+v", got, want)
	}
	if !journey.Spans[1].NeedsRevision {
		t.Errorf("needs-revision span = %+v", journey.Spans[1])
	}
}
