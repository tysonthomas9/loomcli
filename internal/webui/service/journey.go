package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	loomevents "github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/types"
)

const (
	journeyHistoryTailLimit = 500
	journeyHistoryPageLimit = 200
	defaultJourneyPageCap   = 100
	journeySpanKindStatus   = "status"
	needsRevisionLabel      = "needs-revision"
)

// JourneyServiceConfig supplies host-local data and deterministic fold knobs.
// HistoryPageCap is primarily a safety bound; zero selects the production
// default. Now is injectable so open spans never read wall-clock time directly.
type JourneyServiceConfig struct {
	EventsDir      string
	Now            func() time.Time
	HistoryPageCap int
}

func normalizeJourneyServiceConfig(configs []JourneyServiceConfig) JourneyServiceConfig {
	config := JourneyServiceConfig{}
	if len(configs) > 0 {
		config = configs[0]
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.HistoryPageCap <= 0 {
		config.HistoryPageCap = defaultJourneyPageCap
	}
	return config
}

func (s *issueServiceImpl) GetJourney(ctx context.Context, issueID string) (*Journey, error) {
	history, honesty, err := s.readJourneyHistory(ctx, issueID)
	if err != nil {
		return nil, err
	}
	now := s.journey.Now().UTC()
	spans := foldJourneySpans(history, honesty.Bounded)
	windows, available, windowReason := readJourneyAgentWindows(s.journey.EventsDir, issueID)
	honesty.AgentWindowsAvailable = available
	honesty.AgentWindowsReason = windowReason

	return &Journey{
		Spans:        spans,
		AgentWindows: windows,
		LeadTime:     decomposeJourneyLeadTime(spans, windows, available, now),
		Honesty:      honesty,
	}, nil
}

func (s *issueServiceImpl) readJourneyHistory(ctx context.Context, issueID string) ([]*types.Event, JourneyHonesty, error) {
	tail, err := s.ListEventHistory(ctx, EventListParams{IssueID: issueID, Limit: journeyHistoryTailLimit})
	if err != nil {
		return nil, JourneyHonesty{}, err
	}
	if tail == nil {
		return nil, JourneyHonesty{}, ErrInternal("event history result missing", nil)
	}

	// A known total matching an unbounded tail proves completeness without a
	// second request. Unknown totals still take the forward paging seam so a
	// legacy bounded ListEvents result is never mislabeled complete.
	if tail.TotalEvents > 0 && !tail.HasMore && len(tail.Events) == tail.TotalEvents {
		return tail.Events, JourneyHonesty{
			CompleteHistory: true,
			EventsSeen:      len(tail.Events),
			TotalEvents:     tail.TotalEvents,
		}, nil
	}

	cursor := ""
	events := make([]*types.Event, 0, max(len(tail.Events), tail.TotalEvents))
	totalEvents := tail.TotalEvents
	for page := 0; page < s.journey.HistoryPageCap; page++ {
		result, pageErr := s.ListEventHistory(ctx, EventListParams{
			IssueID: issueID,
			Limit:   journeyHistoryPageLimit,
			Since:   &cursor,
		})
		if pageErr != nil {
			var serviceErr *ServiceError
			if errors.As(pageErr, &serviceErr) && serviceErr.Kind == KindValidation {
				boundedEvents, boundedHonesty := boundedJourneyHistory(tail, "history_paging_unavailable")
				return boundedEvents, boundedHonesty, nil
			}
			return nil, JourneyHonesty{}, pageErr
		}
		if result == nil {
			boundedEvents, boundedHonesty := boundedJourneyHistory(tail, "history_page_missing")
			return boundedEvents, boundedHonesty, nil
		}
		events = append(events, result.Events...)
		if result.TotalEvents > 0 {
			totalEvents = result.TotalEvents
		}
		if !result.HasMore {
			if totalEvents > 0 && len(events) != totalEvents {
				boundedEvents, boundedHonesty := boundedJourneyHistory(tail, "history_count_mismatch")
				boundedHonesty.TotalEvents = totalEvents
				return boundedEvents, boundedHonesty, nil
			}
			return events, JourneyHonesty{
				CompleteHistory: true,
				EventsSeen:      len(events),
				TotalEvents:     totalEvents,
			}, nil
		}
		if result.Cursor == "" || result.Cursor == cursor {
			boundedEvents, boundedHonesty := boundedJourneyHistory(tail, "history_cursor_stalled")
			return boundedEvents, boundedHonesty, nil
		}
		cursor = result.Cursor
	}

	boundedEvents, boundedHonesty := boundedJourneyHistory(tail, "history_page_cap_reached")
	return boundedEvents, boundedHonesty, nil
}

func boundedJourneyHistory(tail *EventListResult, reason string) ([]*types.Event, JourneyHonesty) {
	return tail.Events, JourneyHonesty{
		Bounded:     true,
		HasMore:     tail.HasMore,
		EventsSeen:  len(tail.Events),
		TotalEvents: tail.TotalEvents,
		Reason:      reason,
	}
}

type journeyFoldState struct {
	status        string
	owner         *string
	ownerKnown    bool
	needsRevision bool
}

type orderedIssueEvent struct {
	event *types.Event
	index int
}

func foldJourneySpans(eventList []*types.Event, bounded bool) []JourneySpan {
	ordered := make([]orderedIssueEvent, 0, len(eventList))
	for index, event := range eventList {
		if event != nil {
			ordered = append(ordered, orderedIssueEvent{event: event, index: index})
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].event.CreatedAt.Equal(ordered[j].event.CreatedAt) {
			return ordered[i].index < ordered[j].index
		}
		return ordered[i].event.CreatedAt.Before(ordered[j].event.CreatedAt)
	})

	state := journeyFoldState{}
	spans := make([]JourneySpan, 0)
	for _, item := range ordered {
		event := item.event
		before := state
		forceBoundary := false
		action := strings.TrimSpace(strings.ToLower(string(event.EventType)))

		switch action {
		case "issue.create":
			state.status = "open"
			state.owner, state.ownerKnown = assignmentFromCreation(event)
			state.needsRevision = labelSetAfter(event, needsRevisionLabel, false)
		case "issue.claim":
			state.status = "in_progress"
			state.owner, state.ownerKnown = normalizedStringPointer(event.Actor), true
			forceBoundary = before.status != ""
		case "issue.release":
			state.status = "open"
			state.owner, state.ownerKnown = nil, true
		case "issue.defer":
			state.status = "deferred"
		case "issue.undefer":
			state.status = "open"
		case "issue.close":
			state.status = "closed"
		case "issue.reopen":
			state.status = "open"
			state.owner, state.ownerKnown = nil, true
		case "issue.assign":
			if assignee, present := event.Metadata["assignee"]; present {
				state.owner, state.ownerKnown = normalizedStringPointer(assignee), true
			}
		case "label.add":
			if strings.EqualFold(strings.TrimSpace(event.Metadata["label"]), needsRevisionLabel) {
				state.needsRevision = true
			}
		case "label.remove":
			if strings.EqualFold(strings.TrimSpace(event.Metadata["label"]), needsRevisionLabel) {
				state.needsRevision = false
			}
		case "issue.update":
			if status, ok := changeAfter(event, "status"); ok {
				state.status = normalizedStatus(status)
			}
			state.needsRevision = labelSetAfter(event, needsRevisionLabel, state.needsRevision)
		}

		if state.status == "" {
			continue
		}
		changed := before.status != state.status || before.ownerKnown != state.ownerKnown ||
			!equalOptionalString(before.owner, state.owner) || before.needsRevision != state.needsRevision
		if !changed && !forceBoundary {
			continue
		}

		at := event.CreatedAt.UTC()
		if len(spans) > 0 {
			spans[len(spans)-1].End = timePointer(at)
		}
		span := JourneySpan{
			Kind:          journeySpanKindStatus,
			Stage:         state.status,
			Start:         at,
			Owner:         cloneStringPointer(state.owner),
			Actor:         normalizedStringPointer(event.Actor),
			NeedsRevision: state.needsRevision,
			Stalled:       action == "issue.release" && strings.EqualFold(strings.TrimSpace(event.Actor), "system"),
		}
		if state.status == "closed" {
			span.End = timePointer(at)
		}
		spans = append(spans, span)
	}

	if len(spans) > 0 && (bounded || ordered[0].event.EventType != types.EventType("issue.create")) {
		spans[0].Approximate = true
		spans[0].UnknownStart = true
	}
	return spans
}

func assignmentFromCreation(event *types.Event) (*string, bool) {
	if assignee, present := event.Metadata["assignee"]; present {
		return normalizedStringPointer(assignee), true
	}
	if assignee, present := changeAfter(event, "assignee"); present {
		return normalizedStringPointer(assignee), true
	}
	return nil, true
}

func changeAfter(event *types.Event, field string) (string, bool) {
	for _, change := range event.Changes {
		if strings.EqualFold(strings.TrimSpace(change.Field), field) {
			return change.After, true
		}
	}
	return "", false
}

func labelSetAfter(event *types.Event, label string, current bool) bool {
	raw, changed := changeAfter(event, "labels")
	if !changed {
		return current
	}
	var labels []string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return current
	}
	for _, candidate := range labels {
		if strings.EqualFold(strings.TrimSpace(candidate), label) {
			return true
		}
	}
	return false
}

func normalizedStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func normalizedStringPointer(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func timePointer(value time.Time) *time.Time {
	clone := value
	return &clone
}

func readJourneyAgentWindows(eventsDir, taskID string) ([]JourneyAgentWindow, bool, string) {
	if strings.TrimSpace(eventsDir) == "" {
		return []JourneyAgentWindow{}, false, "events_dir_not_configured"
	}
	all, _, err := loomevents.ReadEventsFromJSONL(eventsDir, loomevents.EventReadOpts{
		Page:    1,
		PerPage: math.MaxInt,
	})
	if err != nil {
		return []JourneyAgentWindow{}, false, "events_read_failed"
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp.Before(all[j].Timestamp) })

	windows := make([]JourneyAgentWindow, 0)
	found := false
	for _, event := range all {
		if event.Type != loomevents.TaskClaimed && event.Type != loomevents.TaskCompleted && event.Type != loomevents.TaskFailed {
			continue
		}
		var data struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil || data.TaskID != taskID {
			continue
		}
		found = true
		switch event.Type {
		case loomevents.TaskClaimed:
			windows = append(windows, JourneyAgentWindow{
				TaskID:  taskID,
				Agent:   event.Agent,
				Start:   event.Timestamp.UTC(),
				Outcome: "running",
			})
		case loomevents.TaskCompleted, loomevents.TaskFailed:
			index := latestOpenAgentWindow(windows, event.Agent)
			if index < 0 {
				continue
			}
			windows[index].End = timePointer(event.Timestamp.UTC())
			if event.Type == loomevents.TaskCompleted {
				windows[index].Outcome = "completed"
			} else {
				windows[index].Outcome = "failed"
			}
		}
	}
	if !found {
		return windows, false, "no_task_lifecycle_events"
	}
	if len(windows) == 0 {
		return windows, false, "no_claimed_window"
	}
	return windows, true, ""
}

func latestOpenAgentWindow(windows []JourneyAgentWindow, agent string) int {
	fallback := -1
	for index := len(windows) - 1; index >= 0; index-- {
		if windows[index].End != nil {
			continue
		}
		if fallback < 0 {
			fallback = index
		}
		if windows[index].Agent == agent {
			return index
		}
	}
	return fallback
}

func decomposeJourneyLeadTime(spans []JourneySpan, windows []JourneyAgentWindow, windowsAvailable bool, now time.Time) JourneyLeadTime {
	if len(spans) == 0 {
		return JourneyLeadTime{}
	}
	start := spans[0].Start
	end := now
	if last := spans[len(spans)-1]; last.Stage == "closed" {
		end = last.Start
	}
	if !end.After(start) {
		return JourneyLeadTime{}
	}

	boundaries := []time.Time{start, end}
	for _, span := range spans {
		boundaries = append(boundaries, span.Start)
		if span.End != nil {
			boundaries = append(boundaries, *span.End)
		}
	}
	for _, window := range windows {
		boundaries = append(boundaries, window.Start)
		if window.End != nil {
			boundaries = append(boundaries, *window.End)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })

	result := JourneyLeadTime{TotalMS: end.Sub(start).Milliseconds()}
	for index := 0; index+1 < len(boundaries); index++ {
		left, right := boundaries[index], boundaries[index+1]
		if !right.After(left) || left.Before(start) || !left.Before(end) {
			continue
		}
		span := journeySpanAt(spans, left)
		if span == nil {
			continue
		}
		durationMS := right.Sub(left).Milliseconds()

		// The four requested buckets are mutually exclusive. Status-derived
		// operator waits and halts take precedence over a lifecycle window that
		// happens to remain open across those intervals; otherwise the same wall
		// time would be counted twice and cease to be a decomposition.
		switch {
		case span.Stage == "blocked":
			result.HaltedMS += durationMS
		case span.Stage == "review" || span.NeedsRevision:
			result.WaitingOnOperatorMS += durationMS
		case windowsAvailable && journeyAgentWorkingAt(windows, left):
			result.AgentWorkingMS += durationMS
		case !windowsAvailable && span.Stage == "in_progress":
			// Exact claim-to-terminal windows replace the status approximation as
			// soon as this host has any claimed window for the task. Otherwise a
			// failed attempt's gap before re-claim would be counted as work merely
			// because the issue remained in_progress. With no task-local window at
			// all (old run or another host), in_progress is the documented fallback.
			result.AgentWorkingMS += durationMS
		case span.Stage == "open" && span.Owner == nil:
			result.QueuedMS += durationMS
		}
	}
	return result
}

func journeySpanAt(spans []JourneySpan, at time.Time) *JourneySpan {
	for index := len(spans) - 1; index >= 0; index-- {
		span := &spans[index]
		if at.Before(span.Start) {
			continue
		}
		if span.End == nil || at.Before(*span.End) {
			return span
		}
	}
	return nil
}

func journeyAgentWorkingAt(windows []JourneyAgentWindow, at time.Time) bool {
	for _, window := range windows {
		if at.Before(window.Start) {
			continue
		}
		if window.End == nil || at.Before(*window.End) {
			return true
		}
	}
	return false
}
