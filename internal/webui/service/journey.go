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
	// Mirrors taskfilter.NeedsRevisionLabel (package cli). service cannot
	// import internal/cli because cli already depends on this package.
	needsRevisionLabel = "needs-revision"
)

// JourneyServiceConfig supplies host-local data and deterministic fold knobs.
// HistoryPageCap is primarily a safety bound; zero selects the production
// default. Now is injectable so open spans never read wall-clock time directly.
type JourneyServiceConfig struct {
	EventsDir              string
	EventsDirForWorkspace  func(string) string
	WorkspaceIDFromContext func(context.Context) string
	Now                    func() time.Time
	HistoryPageCap         int
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
	if len(history) == 0 {
		if _, err := s.GetIssue(ctx, issueID); err != nil {
			return nil, err
		}
	}
	now := s.journey.Now().UTC()
	spans := foldJourneySpans(history, honesty.Bounded)
	windows, available, windowReason := readJourneyAgentWindows(s.journey.eventsDirFor(ctx), issueID)
	honesty.AgentWindowsAvailable = available
	honesty.AgentWindowsReason = windowReason

	return &Journey{
		Spans:        spans,
		AgentWindows: windows,
		LeadTime:     decomposeJourneyLeadTime(spans, windows, available, now),
		Honesty:      honesty,
	}, nil
}

func (config JourneyServiceConfig) eventsDirFor(ctx context.Context) string {
	if config.EventsDirForWorkspace != nil && config.WorkspaceIDFromContext != nil {
		if eventsDir := config.EventsDirForWorkspace(config.WorkspaceIDFromContext(ctx)); eventsDir != "" {
			return eventsDir
		}
	}
	return config.EventsDir
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
	if hasKnownCompleteJourneyHistory(tail.Events, tail.HasMore, tail.TotalEvents) {
		return tail.Events, completeJourneyHonesty(tail.Events, tail.TotalEvents), nil
	}
	return s.walkJourneyHistory(ctx, issueID, tail)
}

type journeyHistoryWalkState struct {
	cursor      string
	events      []*types.Event
	totalEvents int
}

type journeyHistoryWalkResult struct {
	events  []*types.Event
	honesty JourneyHonesty
}

func (s *issueServiceImpl) walkJourneyHistory(ctx context.Context, issueID string, tail *EventListResult) ([]*types.Event, JourneyHonesty, error) {
	state := journeyHistoryWalkState{
		events:      make([]*types.Event, 0, max(len(tail.Events), tail.TotalEvents)),
		totalEvents: tail.TotalEvents,
	}
	for page := 0; page < s.journey.HistoryPageCap; page++ {
		outcome := s.readJourneyHistoryPage(ctx, issueID, tail, &state)
		if outcome != nil {
			return outcome.events, outcome.honesty, nil
		}
	}

	boundedEvents, boundedHonesty := boundedJourneyHistory(tail, "history_page_cap_reached")
	return boundedEvents, boundedHonesty, nil
}

func (s *issueServiceImpl) readJourneyHistoryPage(ctx context.Context, issueID string, tail *EventListResult, state *journeyHistoryWalkState) *journeyHistoryWalkResult {
	result, err := s.ListEventHistory(ctx, EventListParams{IssueID: issueID, Limit: journeyHistoryPageLimit, Since: &state.cursor})
	if err != nil {
		var serviceErr *ServiceError
		if errors.As(err, &serviceErr) && serviceErr.Kind == KindValidation {
			return boundedJourneyHistoryResult(tail, "history_paging_unavailable")
		}
		return boundedJourneyHistoryResult(tail, "history_paging_failed")
	}
	if result == nil {
		return boundedJourneyHistoryResult(tail, "history_page_missing")
	}

	state.events = append(state.events, result.Events...)
	if result.TotalEvents > 0 {
		state.totalEvents = result.TotalEvents
	}
	if !result.HasMore {
		if state.totalEvents > 0 && len(state.events) != state.totalEvents {
			outcome := boundedJourneyHistoryResult(tail, "history_count_mismatch")
			outcome.honesty.TotalEvents = state.totalEvents
			return outcome
		}
		return &journeyHistoryWalkResult{events: state.events, honesty: completeJourneyHonesty(state.events, state.totalEvents)}
	}
	if result.Cursor == "" || result.Cursor == state.cursor {
		return boundedJourneyHistoryResult(tail, "history_cursor_stalled")
	}
	state.cursor = result.Cursor
	return nil
}

func hasKnownCompleteJourneyHistory(events []*types.Event, hasMore bool, totalEvents int) bool {
	return !hasMore && totalEvents > 0 && len(events) == totalEvents
}

func completeJourneyHonesty(events []*types.Event, totalEvents int) JourneyHonesty {
	return JourneyHonesty{CompleteHistory: true, EventsSeen: len(events), TotalEvents: totalEvents}
}

func boundedJourneyHistoryResult(tail *EventListResult, reason string) *journeyHistoryWalkResult {
	events, honesty := boundedJourneyHistory(tail, reason)
	return &journeyHistoryWalkResult{events: events, honesty: honesty}
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
		action, forceBoundary := applyJourneyEvent(&state, event)
		if !journeyFoldStateChanged(before, state) && !forceBoundary {
			continue
		}
		spans = appendJourneySpan(spans, state, action, event)
	}

	if len(spans) > 0 && (bounded || normalizedJourneyAction(ordered[0].event) != "issue.create") {
		spans[0].Approximate = true
		spans[0].UnknownStart = true
	}
	return spans
}

func applyJourneyEvent(state *journeyFoldState, event *types.Event) (string, bool) {
	action := normalizedJourneyAction(event)
	hadStatus := state.status != ""
	forceBoundary := false
	switch action {
	case "issue.create":
		state.status = "open"
		// fleet-db issue.create has neither metadata nor changes, so a
		// pre-assigned owner is not detectable and its wait remains queued.
		state.owner, state.ownerKnown = nil, true
	case "issue.claim":
		state.status = "in_progress"
		state.owner, state.ownerKnown = normalizedStringPointer(event.Actor), true
		forceBoundary = hadStatus
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
	case "label.add", "label.remove":
		applyJourneyLabelEvent(state, action, event)
	case "issue.update":
		if status, ok := changeAfter(event, "status"); ok {
			state.status = normalizedStatus(status)
		}
	}
	return action, forceBoundary
}

func normalizedJourneyAction(event *types.Event) string {
	return strings.TrimSpace(strings.ToLower(string(event.EventType)))
}

func applyJourneyLabelEvent(state *journeyFoldState, action string, event *types.Event) {
	if !strings.EqualFold(strings.TrimSpace(event.Metadata["label"]), needsRevisionLabel) {
		return
	}
	state.needsRevision = action == "label.add"
}

func journeyFoldStateChanged(before, after journeyFoldState) bool {
	return after.status != "" && (before.status != after.status || before.ownerKnown != after.ownerKnown ||
		!equalOptionalString(before.owner, after.owner) || before.needsRevision != after.needsRevision)
}

func appendJourneySpan(spans []JourneySpan, state journeyFoldState, action string, event *types.Event) []JourneySpan {
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
	return append(spans, span)
}

func changeAfter(event *types.Event, field string) (string, bool) {
	for _, change := range event.Changes {
		if strings.EqualFold(strings.TrimSpace(change.Field), field) {
			return change.After, true
		}
	}
	return "", false
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
	all, unavailableReason := readJourneyLifecycleEvents(eventsDir)
	if unavailableReason != "" {
		return []JourneyAgentWindow{}, false, unavailableReason
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp.Before(all[j].Timestamp) })
	return scanJourneyAgentWindows(all, taskID)
}

func readJourneyLifecycleEvents(eventsDir string) ([]loomevents.Event, string) {
	if strings.TrimSpace(eventsDir) == "" {
		return nil, "events_dir_not_configured"
	}
	all, _, err := loomevents.ReadEventsFromJSONL(eventsDir, loomevents.EventReadOpts{Page: 1, PerPage: math.MaxInt})
	if err != nil {
		return nil, "events_read_failed"
	}
	return all, ""
}

func scanJourneyAgentWindows(all []loomevents.Event, taskID string) ([]JourneyAgentWindow, bool, string) {
	windows := make([]JourneyAgentWindow, 0)
	found := false
	for _, event := range all {
		if !isJourneyLifecycleEvent(event) || journeyLifecycleTaskID(event) != taskID {
			continue
		}
		found = true
		windows = applyJourneyLifecycleEvent(windows, taskID, event)
	}
	if !found {
		return windows, false, "no_task_lifecycle_events"
	}
	if len(windows) == 0 {
		return windows, false, "no_claimed_window"
	}
	return windows, true, ""
}

func isJourneyLifecycleEvent(event loomevents.Event) bool {
	return event.Type == loomevents.TaskClaimed || event.Type == loomevents.TaskCompleted || event.Type == loomevents.TaskFailed
}

func journeyLifecycleTaskID(event loomevents.Event) string {
	var data struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return ""
	}
	return data.TaskID
}

func applyJourneyLifecycleEvent(windows []JourneyAgentWindow, taskID string, event loomevents.Event) []JourneyAgentWindow {
	if event.Type == loomevents.TaskClaimed {
		if index := latestOpenAgentWindow(windows, event.Agent); index >= 0 {
			windows[index].End = timePointer(event.Timestamp.UTC())
			windows[index].Outcome = "superseded"
		}
		return append(windows, JourneyAgentWindow{TaskID: taskID, Agent: event.Agent, Start: event.Timestamp.UTC(), Outcome: "running"})
	}
	index := latestOpenAgentWindow(windows, event.Agent)
	if index < 0 {
		return windows
	}
	windows[index].End = timePointer(event.Timestamp.UTC())
	if event.Type == loomevents.TaskCompleted {
		windows[index].Outcome = "completed"
	} else {
		windows[index].Outcome = "failed"
	}
	return windows
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
	start, end, ok := journeyLeadTimeRange(spans, now)
	if !ok {
		return JourneyLeadTime{}
	}

	result := JourneyLeadTime{TotalMS: end.Sub(start).Milliseconds()}
	boundaries := journeyLeadTimeBoundaries(spans, windows, start, end)
	for index := 0; index+1 < len(boundaries); index++ {
		addJourneyLeadTimeInterval(&result, spans, windows, windowsAvailable, start, end, boundaries[index], boundaries[index+1])
	}
	return result
}

func journeyLeadTimeRange(spans []JourneySpan, now time.Time) (time.Time, time.Time, bool) {
	if len(spans) == 0 {
		return time.Time{}, time.Time{}, false
	}
	start, end := spans[0].Start, now
	for _, span := range spans {
		if span.Stage == "closed" {
			end = span.Start
			break
		}
	}
	return start, end, end.After(start)
}

func journeyLeadTimeBoundaries(spans []JourneySpan, windows []JourneyAgentWindow, start, end time.Time) []time.Time {
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
	return boundaries
}

func addJourneyLeadTimeInterval(result *JourneyLeadTime, spans []JourneySpan, windows []JourneyAgentWindow, windowsAvailable bool, start, end, left, right time.Time) {
	if !right.After(left) || left.Before(start) || !left.Before(end) {
		return
	}
	span := journeySpanAt(spans, left)
	if span == nil {
		return
	}
	addJourneyLeadTimeBucket(result, span, windows, windowsAvailable, left, right.Sub(left).Milliseconds())
}

func addJourneyLeadTimeBucket(result *JourneyLeadTime, span *JourneySpan, windows []JourneyAgentWindow, windowsAvailable bool, at time.Time, durationMS int64) {
	// The four requested buckets are mutually exclusive. Status-derived
	// operator waits and halts take precedence over a lifecycle window that
	// happens to remain open across those intervals; otherwise the same wall
	// time would be counted twice and cease to be a decomposition.
	switch {
	case span.Stage == "blocked":
		result.HaltedMS += durationMS
	case span.Stage == "review" || span.NeedsRevision:
		result.WaitingOnOperatorMS += durationMS
	case windowsAvailable && span.Stage == "in_progress" && journeyAgentWorkingAt(windows, at):
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
