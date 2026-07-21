package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	SessionLifecycleErrDescriptorConflict = "session_descriptor_conflict"
	SessionLifecycleErrOutcomeConflict    = "session_outcome_conflict"
	SessionLifecycleErrAttemptMismatch    = "session_attempt_mismatch"
	SessionLifecycleErrTaskRunTerminal    = "task_run_terminal"
	SessionLifecycleErrContention         = "session_lifecycle_contention"
)

const (
	SessionMetadataBackend               = "backend"
	SessionMetadataModel                 = "model"
	SessionMetadataFencingToken          = "fencing_token"
	SessionMetadataDriverRunID           = "driver_run_id"
	SessionMetadataDriverStepID          = "driver_step_id"
	SessionMetadataDriverRunnerSessionID = "driver_runner_session_id"
	SessionMetadataTranscriptRef         = "transcript_ref"
	SessionMetadataUsageTokens           = "usage_tokens"
	SessionMetadataUsageCostUSD          = "usage_cost_usd"
	SessionMetadataDescriptorFingerprint = "session_descriptor_fingerprint"
	SessionMetadataOutcomeFingerprint    = "session_outcome_fingerprint"
)

var invocationKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

var serverSessionMetadataKeys = map[string]struct{}{
	SessionMetadataBackend: {}, SessionMetadataModel: {}, SessionMetadataFencingToken: {},
	SessionMetadataDriverRunID: {}, SessionMetadataDriverStepID: {},
	SessionMetadataDriverRunnerSessionID: {}, SessionMetadataTranscriptRef: {},
	SessionMetadataUsageTokens: {}, SessionMetadataUsageCostUSD: {},
	SessionMetadataDescriptorFingerprint: {}, SessionMetadataOutcomeFingerprint: {},
}

// SessionLifecycleError is a machine-readable, non-retryable lifecycle error.
type SessionLifecycleError struct {
	Code string
	Err  error
}

func (e *SessionLifecycleError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *SessionLifecycleError) Unwrap() error { return e.Err }

// Retryable marks descriptor/outcome conflicts as caller bugs, not transient failures.
func (e *SessionLifecycleError) Retryable() bool { return false }

// SessionLifecycleTransientError is a machine-readable retryable store failure.
type SessionLifecycleTransientError struct {
	Code string
	Err  error
}

func (e *SessionLifecycleTransientError) Error() string   { return e.Code + ": " + e.Err.Error() }
func (e *SessionLifecycleTransientError) Unwrap() error   { return e.Err }
func (e *SessionLifecycleTransientError) Retryable() bool { return true }

// SessionDescriptor is the wire-equivalent description of one agent invocation.
type SessionDescriptor struct {
	InvocationKey   string
	Backend         string
	Model           string
	ParentSessionID string
	Kind            domain.AgentSessionKind
	Tags            []string
	Metadata        map[string]string
}

// SessionRunContext is server-owned task-run evidence for one invocation.
type SessionRunContext struct {
	WorkspaceKey string
	TaskRunID    string
	Attempt      int
	FencingToken int64
	DriverRunID  string
	DriverStepID string
}

// SessionRef identifies an opened invocation session.
type SessionRef struct {
	WorkspaceKey string
	SessionID    string
	Attempt      int
}

// SessionUsage carries optional usage values; nil means unknown rather than zero.
type SessionUsage struct {
	Tokens  *int64
	CostUSD *float64
}

// SessionOutcome is the terminal result supplied by an invocation owner.
type SessionOutcome struct {
	Status                domain.AgentSessionStatus
	ExitCode              *int
	Summary               string
	ErrorClass            string
	TranscriptRef         string
	DriverRunnerSessionID string
	Usage                 SessionUsage
	Metadata              map[string]string
}

// SessionID composes the stable per-invocation AgentSession identifier.
func SessionID(taskRunID string, attempt int, invocationKey string) string {
	return fmt.Sprintf("%s-a%d-%s", taskRunID, attempt, invocationKey)
}

// TranscriptArtifactID composes the stable per-invocation transcript artifact ID.
func TranscriptArtifactID(taskRunID string, attempt int, invocationKey string) string {
	return fmt.Sprintf("transcript-%s-a%d-%s", taskRunID, attempt, invocationKey)
}

// TaskRunClaimAttempt returns the one-based ordinal of the run's current
// claim. scheduler_attempt records completed/requeued attempts, so a live
// claim is always that persisted zero-based count plus one.
func TaskRunClaimAttempt(run *domain.TaskRun) int {
	if run == nil {
		return 1
	}
	attempt, err := strconv.Atoi(strings.TrimSpace(run.RuntimeMetadata["scheduler_attempt"]))
	if err != nil || attempt < 0 {
		attempt = 0
	}
	return attempt + 1
}

// ValidateSessionDescriptor validates the descriptor before it reaches persistence.
func ValidateSessionDescriptor(d SessionDescriptor) error {
	if !invocationKeyPattern.MatchString(d.InvocationKey) {
		return fmt.Errorf("invocation_key %q must match [a-z0-9][a-z0-9-]{0,63}: %w", d.InvocationKey, domain.ErrInvalid)
	}
	if strings.TrimSpace(d.Backend) == "" || strings.TrimSpace(d.Model) == "" {
		return fmt.Errorf("backend and model are required: %w", domain.ErrInvalid)
	}
	if d.Kind != "" && !validSessionKind(d.Kind) {
		return fmt.Errorf("session kind %q is invalid: %w", d.Kind, domain.ErrInvalid)
	}
	for key := range d.Metadata {
		if _, reserved := serverSessionMetadataKeys[key]; reserved {
			return fmt.Errorf("metadata key %q is server-stamped: %w", key, domain.ErrInvalid)
		}
	}
	return nil
}

func validSessionKind(kind domain.AgentSessionKind) bool {
	switch kind {
	case domain.AgentSessionKindTask, domain.AgentSessionKindOrchestration, domain.AgentSessionKindTerminal,
		domain.AgentSessionKindMaintenance, domain.AgentSessionKindAdHoc, domain.AgentSessionKindJudge:
		return true
	default:
		return false
	}
}

// NormalizedSessionDescriptor applies defaulted and order-insensitive fields.
func NormalizedSessionDescriptor(d SessionDescriptor) SessionDescriptor {
	d.Kind = defaultSessionKind(d.Kind)
	d.Tags = append([]string(nil), d.Tags...)
	sort.Strings(d.Tags)
	d.Metadata = cloneSessionMetadata(d.Metadata)
	if len(d.Tags) == 0 {
		d.Tags = nil
	}
	if len(d.Metadata) == 0 {
		d.Metadata = nil
	}
	return d
}

// SessionDescriptorFingerprint preserves immutable descriptor identity after close metadata is added.
func SessionDescriptorFingerprint(descriptor SessionDescriptor) string {
	descriptor = NormalizedSessionDescriptor(descriptor)
	payload, _ := json.Marshal(descriptor)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum)
}

// ValidateSessionOutcome validates terminal-only and server-owned outcome fields.
func ValidateSessionOutcome(outcome SessionOutcome) error {
	if !outcome.Status.IsTerminal() {
		return fmt.Errorf("session outcome must be terminal: %w", domain.ErrInvalid)
	}
	if outcome.Usage.CostUSD != nil && (math.IsNaN(*outcome.Usage.CostUSD) || math.IsInf(*outcome.Usage.CostUSD, 0)) {
		return fmt.Errorf("session usage cost must be finite: %w", domain.ErrInvalid)
	}
	for key := range outcome.Metadata {
		if _, reserved := serverSessionMetadataKeys[key]; reserved {
			return fmt.Errorf("metadata key %q is server-stamped: %w", key, domain.ErrInvalid)
		}
	}
	return nil
}

// SessionOutcomeFingerprint identifies the exact terminal outcome for replay checks.
func SessionOutcomeFingerprint(outcome SessionOutcome) string {
	if len(outcome.Metadata) == 0 {
		outcome.Metadata = nil
	} else {
		outcome.Metadata = cloneSessionMetadata(outcome.Metadata)
	}
	payload, _ := json.Marshal(outcome)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum)
}

func defaultSessionKind(kind domain.AgentSessionKind) domain.AgentSessionKind {
	if kind == "" {
		return domain.AgentSessionKindTask
	}
	return kind
}

func cloneSessionMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

// SessionOutcomeMatches reports whether outcome is the already-settled outcome.
func SessionOutcomeMatches(session *domain.AgentSession, outcome SessionOutcome) bool {
	if session == nil {
		return false
	}
	if fingerprint := session.Metadata[SessionMetadataOutcomeFingerprint]; fingerprint != "" {
		return fingerprint == SessionOutcomeFingerprint(outcome)
	}
	if session.Status != outcome.Status || session.Summary != outcome.Summary || session.ErrorClass != outcome.ErrorClass {
		return false
	}
	if !sameIntPointer(session.ExitCode, outcome.ExitCode) {
		return false
	}
	if outcome.TranscriptRef != "" && session.Metadata[SessionMetadataTranscriptRef] != outcome.TranscriptRef {
		return false
	}
	if outcome.DriverRunnerSessionID != "" && session.Metadata[SessionMetadataDriverRunnerSessionID] != outcome.DriverRunnerSessionID {
		return false
	}
	return sessionMetadataIncludes(session.Metadata, outcome.Metadata) && usageMatches(session.Metadata, outcome.Usage)
}

func sameIntPointer(left, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sessionMetadataIncludes(actual, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func usageMatches(metadata map[string]string, usage SessionUsage) bool {
	if usage.Tokens != nil && metadata[SessionMetadataUsageTokens] != strconv.FormatInt(*usage.Tokens, 10) {
		return false
	}
	if usage.CostUSD != nil && metadata[SessionMetadataUsageCostUSD] != strconv.FormatFloat(*usage.CostUSD, 'f', -1, 64) {
		return false
	}
	return true
}

// ApplySessionOutcome stamps outcome onto a non-terminal AgentSession.
func ApplySessionOutcome(session *domain.AgentSession, outcome SessionOutcome, now time.Time) {
	metadata := maps.Clone(session.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	for key, value := range outcome.Metadata {
		metadata[key] = value
	}
	if outcome.TranscriptRef != "" {
		metadata[SessionMetadataTranscriptRef] = outcome.TranscriptRef
	}
	if outcome.DriverRunnerSessionID != "" {
		metadata[SessionMetadataDriverRunnerSessionID] = outcome.DriverRunnerSessionID
	}
	if outcome.Usage.Tokens != nil {
		metadata[SessionMetadataUsageTokens] = strconv.FormatInt(*outcome.Usage.Tokens, 10)
	}
	if outcome.Usage.CostUSD != nil {
		metadata[SessionMetadataUsageCostUSD] = strconv.FormatFloat(*outcome.Usage.CostUSD, 'f', -1, 64)
	}
	metadata[SessionMetadataOutcomeFingerprint] = SessionOutcomeFingerprint(outcome)
	session.Status = outcome.Status
	session.ExitCode = cloneOutcomeExitCode(outcome.ExitCode)
	session.Summary = outcome.Summary
	session.ErrorClass = outcome.ErrorClass
	session.Metadata = metadata
	session.FinishedAt = &now
	session.UpdatedAt = now
}

func cloneOutcomeExitCode(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// AgentSessionUpdateMatches reports whether every supplied PATCH field is already stored.
func AgentSessionUpdateMatches(session *domain.AgentSession, update AgentSessionUpdate) bool {
	return matchesString(update.NodeID, session.NodeID) && matchesString(update.TaskID, session.TaskID) &&
		matchesStatus(update.Status, session.Status) && matchesString(update.Phase, session.Phase) &&
		matchesTime(update.LastHeartbeat, session.LastHeartbeat) && matchesTimePointer(update.FinishedAt, session.FinishedAt) &&
		matchesString(update.Summary, session.Summary) && matchesString(update.ErrorClass, session.ErrorClass) &&
		matchesIntPointer(update.ExitCode, session.ExitCode) && matchesMetadata(update.Metadata, session.Metadata)
}

// ProtectAgentSessionTerminalUpdate scopes generic-update CAS to lifecycle-managed terminal sessions.
func ProtectAgentSessionTerminalUpdate(session *domain.AgentSession) bool {
	return session != nil && session.InvocationKey != "" && session.Status.IsTerminal()
}

// AgentSessionUpdateTouchesOutcome reports whether a PATCH carries settled outcome fields.
func AgentSessionUpdateTouchesOutcome(update AgentSessionUpdate) bool {
	return update.Status != nil || update.Summary != nil || update.ErrorClass != nil ||
		update.ExitCode != nil || update.FinishedAt != nil
}

func matchesString(expected *string, actual string) bool {
	return expected == nil || *expected == actual
}

func matchesStatus(expected *domain.AgentSessionStatus, actual domain.AgentSessionStatus) bool {
	return expected == nil || *expected == actual
}

func matchesTime(expected *time.Time, actual time.Time) bool {
	return expected == nil || expected.Equal(actual)
}

func matchesTimePointer(expected **time.Time, actual *time.Time) bool {
	return expected == nil || (*expected == nil && actual == nil) || (*expected != nil && actual != nil && (*expected).Equal(*actual))
}

func matchesIntPointer(expected **int, actual *int) bool {
	return expected == nil || sameIntPointer(*expected, actual)
}

func matchesMetadata(expected *map[string]string, actual map[string]string) bool {
	return expected == nil || maps.Equal(SessionMetadataUpdate(actual, *expected), actual)
}

// SessionMetadataUpdate applies leaf metadata while retaining server-owned lifecycle stamps.
func SessionMetadataUpdate(existing, replacement map[string]string) map[string]string {
	out := cloneSessionMetadata(replacement)
	if out == nil {
		out = map[string]string{}
	}
	for key := range serverSessionMetadataKeys {
		if value, ok := existing[key]; ok {
			out[key] = value
		}
	}
	return out
}
