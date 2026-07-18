package execution

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	DefaultReconciliationQueueLimit = 50
	MaxReconciliationQueueLimit     = 500
	ReconciliationClaimLease        = time.Minute

	reconciliationRetryInitial = time.Second
	reconciliationRetryMaximum = 5 * time.Minute
	reconciliationErrorLimit   = 1024
)

// AwaitEventNotificationAPI owns the leased queue state used by the
// always-on await-event coordinator. The coordinator may inspect event
// provenance and call other capabilities, but it cannot claim, complete, or
// reschedule Execution-owned rows without an exact system authority.
type AwaitEventNotificationAPI interface {
	ClaimAwaitEventNotifications(context.Context, authority.SystemAuthority, ClaimAwaitEventNotificationsCommand) ([]AwaitEventNotification, error)
	CompleteAwaitEventNotification(context.Context, authority.SystemAuthority, CompleteAwaitEventNotificationCommand) error
	RetryAwaitEventNotification(context.Context, authority.SystemAuthority, RetryAwaitEventNotificationCommand) error
}

// DriverRunOutcomeAPI is the corresponding terminal DriverRun outcome queue
// surface. It intentionally excludes Automation publication and await
// dispatch; those remain consumer-owned ports in the outside coordinator.
type DriverRunOutcomeAPI interface {
	ClaimDriverRunOutcomes(context.Context, authority.SystemAuthority, ClaimDriverRunOutcomesCommand) ([]DriverRunOutcome, error)
	CompleteDriverRunOutcome(context.Context, authority.SystemAuthority, CompleteDriverRunOutcomeCommand) error
	RetryDriverRunOutcome(context.Context, authority.SystemAuthority, RetryDriverRunOutcomeCommand) error
}

type ClaimAwaitEventNotificationsCommand struct {
	WorkspaceKey string
	ClaimID      string
	Before       time.Time
	Limit        int
}

type CompleteAwaitEventNotificationCommand struct {
	WorkspaceKey string
	EventID      string
	ClaimID      string
	CompletedAt  time.Time
}

type RetryAwaitEventNotificationCommand struct {
	WorkspaceKey string
	EventID      string
	ClaimID      string
	Attempt      int
	FailedAt     time.Time
	Cause        string
}

// AwaitEvent is the minimal immutable event envelope required by the
// Execution await coordinator. It deliberately does not alias Automation's
// model, keeping the module dependency direction one-way.
type AwaitEvent struct {
	WorkspaceKey  string
	EventID       string
	SourceEventID string
	EventType     string
	SubjectRef    string
	SourceKind    string
	Origin        string
	ActorRef      string
	Payload       json.RawMessage
}

func (event AwaitEvent) CanonicalEventID() (string, bool) {
	if event.EventID == "" || strings.TrimSpace(event.EventID) != event.EventID {
		return "", false
	}
	if event.SourceEventID != "" && strings.TrimSpace(event.SourceEventID) != event.SourceEventID {
		return "", false
	}
	if event.SourceEventID != "" {
		return event.SourceEventID, true
	}
	return event.EventID, true
}

type AwaitEventNotification struct {
	Event            AwaitEvent
	Attempt          int
	DurableEventID   string
	CanonicalEventID string
	PayloadOversized bool
	PayloadSize      int
}

type ClaimDriverRunOutcomesCommand struct {
	WorkspaceKey string
	ClaimID      string
	Before       time.Time
	Limit        int
}

type CompleteDriverRunOutcomeCommand struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	CompletedAt  time.Time
}

type RetryDriverRunOutcomeCommand struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	Attempt      int
	FailedAt     time.Time
	Cause        string
}

type DriverRunOutcome struct {
	WorkspaceKey  string
	RunID         string
	Status        DriverRunStatus
	Summary       string
	ErrorClass    string
	ParentRunID   string
	ParentEventID string
	EpicID        string
	OccurredAt    time.Time
	Attempt       int
}

// AwaitEventNotificationLease is intentionally distinct from the public claim
// command: Execution, not a caller, derives claim expiry.
type AwaitEventNotificationLease struct {
	WorkspaceKey string
	ClaimID      string
	Before       time.Time
	ClaimUntil   time.Time
	Limit        int
}

type AwaitEventNotificationCompletion struct {
	WorkspaceKey string
	EventID      string
	ClaimID      string
	CompletedAt  time.Time
}

type AwaitEventNotificationRetry struct {
	WorkspaceKey string
	EventID      string
	ClaimID      string
	AvailableAt  time.Time
	Error        string
}

type AwaitEventNotificationQueuePort interface {
	ClaimAwaitEventNotifications(context.Context, AwaitEventNotificationLease) ([]AwaitEventNotification, error)
	CompleteAwaitEventNotification(context.Context, AwaitEventNotificationCompletion) error
	RetryAwaitEventNotification(context.Context, AwaitEventNotificationRetry) error
}

type DriverRunOutcomeLease struct {
	WorkspaceKey string
	ClaimID      string
	Before       time.Time
	ClaimUntil   time.Time
	Limit        int
}

type DriverRunOutcomeCompletion struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	CompletedAt  time.Time
}

type DriverRunOutcomeRetry struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	AvailableAt  time.Time
	Error        string
}

type DriverRunOutcomeQueuePort interface {
	ClaimDriverRunOutcomes(context.Context, DriverRunOutcomeLease) ([]DriverRunOutcome, error)
	CompleteDriverRunOutcome(context.Context, DriverRunOutcomeCompletion) error
	RetryDriverRunOutcome(context.Context, DriverRunOutcomeRetry) error
}

func (service *Service) ClaimAwaitEventNotifications(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ClaimAwaitEventNotificationsCommand,
) ([]AwaitEventNotification, error) {
	if err := service.requireSystem(ActionClaimAwaitEventNotifications, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueClaim(command.ClaimID, command.Before, command.Limit) {
		return nil, ErrInvalid
	}
	if service.dependencies.AwaitEvents == nil {
		return nil, ErrUnavailable
	}
	values, err := service.dependencies.AwaitEvents.ClaimAwaitEventNotifications(ctx, AwaitEventNotificationLease{
		WorkspaceKey: command.WorkspaceKey,
		ClaimID:      command.ClaimID,
		Before:       command.Before.UTC(),
		ClaimUntil:   command.Before.UTC().Add(ReconciliationClaimLease),
		Limit:        command.Limit,
	})
	if err != nil {
		return nil, err
	}
	if len(values) > command.Limit {
		return nil, ErrConflict
	}
	return cloneAwaitEventNotifications(values), nil
}

func (service *Service) CompleteAwaitEventNotification(
	ctx context.Context,
	auth authority.SystemAuthority,
	command CompleteAwaitEventNotificationCommand,
) error {
	if err := service.requireSystem(ActionCompleteAwaitEventNotification, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueIdentity(command.EventID, command.ClaimID) || command.CompletedAt.IsZero() {
		return ErrInvalid
	}
	if service.dependencies.AwaitEvents == nil {
		return ErrUnavailable
	}
	return service.dependencies.AwaitEvents.CompleteAwaitEventNotification(ctx, AwaitEventNotificationCompletion{
		WorkspaceKey: command.WorkspaceKey,
		EventID:      command.EventID,
		ClaimID:      command.ClaimID,
		CompletedAt:  command.CompletedAt.UTC(),
	})
}

func (service *Service) RetryAwaitEventNotification(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RetryAwaitEventNotificationCommand,
) error {
	if err := service.requireSystem(ActionRetryAwaitEventNotification, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueRetry(command.EventID, command.ClaimID, command.Attempt, command.FailedAt, command.Cause) {
		return ErrInvalid
	}
	if service.dependencies.AwaitEvents == nil {
		return ErrUnavailable
	}
	return service.dependencies.AwaitEvents.RetryAwaitEventNotification(ctx, AwaitEventNotificationRetry{
		WorkspaceKey: command.WorkspaceKey,
		EventID:      command.EventID,
		ClaimID:      command.ClaimID,
		AvailableAt:  command.FailedAt.UTC().Add(reconciliationRetryDelay(command.Attempt)),
		Error:        boundedReconciliationError(command.Cause),
	})
}

func (service *Service) ClaimDriverRunOutcomes(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ClaimDriverRunOutcomesCommand,
) ([]DriverRunOutcome, error) {
	if err := service.requireSystem(ActionClaimDriverRunOutcomes, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueClaim(command.ClaimID, command.Before, command.Limit) {
		return nil, ErrInvalid
	}
	if service.dependencies.RunOutcomes == nil {
		return nil, ErrUnavailable
	}
	values, err := service.dependencies.RunOutcomes.ClaimDriverRunOutcomes(ctx, DriverRunOutcomeLease{
		WorkspaceKey: command.WorkspaceKey,
		ClaimID:      command.ClaimID,
		Before:       command.Before.UTC(),
		ClaimUntil:   command.Before.UTC().Add(ReconciliationClaimLease),
		Limit:        command.Limit,
	})
	if err != nil {
		return nil, err
	}
	if len(values) > command.Limit {
		return nil, ErrConflict
	}
	return append([]DriverRunOutcome(nil), values...), nil
}

func (service *Service) CompleteDriverRunOutcome(
	ctx context.Context,
	auth authority.SystemAuthority,
	command CompleteDriverRunOutcomeCommand,
) error {
	if err := service.requireSystem(ActionCompleteDriverRunOutcome, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueIdentity(command.RunID, command.ClaimID) || command.CompletedAt.IsZero() {
		return ErrInvalid
	}
	if service.dependencies.RunOutcomes == nil {
		return ErrUnavailable
	}
	return service.dependencies.RunOutcomes.CompleteDriverRunOutcome(ctx, DriverRunOutcomeCompletion{
		WorkspaceKey: command.WorkspaceKey,
		RunID:        command.RunID,
		ClaimID:      command.ClaimID,
		CompletedAt:  command.CompletedAt.UTC(),
	})
}

func (service *Service) RetryDriverRunOutcome(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RetryDriverRunOutcomeCommand,
) error {
	if err := service.requireSystem(ActionRetryDriverRunOutcome, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueRetry(command.RunID, command.ClaimID, command.Attempt, command.FailedAt, command.Cause) {
		return ErrInvalid
	}
	if service.dependencies.RunOutcomes == nil {
		return ErrUnavailable
	}
	return service.dependencies.RunOutcomes.RetryDriverRunOutcome(ctx, DriverRunOutcomeRetry{
		WorkspaceKey: command.WorkspaceKey,
		RunID:        command.RunID,
		ClaimID:      command.ClaimID,
		AvailableAt:  command.FailedAt.UTC().Add(reconciliationRetryDelay(command.Attempt)),
		Error:        boundedReconciliationError(command.Cause),
	})
}

func validQueueClaim(claimID string, before time.Time, limit int) bool {
	return strings.TrimSpace(claimID) != "" && strings.TrimSpace(claimID) == claimID &&
		!before.IsZero() && limit > 0 && limit <= MaxReconciliationQueueLimit
}

func validQueueWorkspace(workspace string) bool {
	return strings.TrimSpace(workspace) != "" && strings.TrimSpace(workspace) == workspace
}

func validQueueIdentity(resourceID, claimID string) bool {
	// DriverRun IDs are opaque and may legitimately contain surrounding
	// whitespace. Await-event poison rows also must remain completable for
	// quarantine, so only an all-whitespace identity is forbidden here.
	return strings.TrimSpace(resourceID) != "" &&
		strings.TrimSpace(claimID) != "" && strings.TrimSpace(claimID) == claimID
}

func validQueueRetry(resourceID, claimID string, attempt int, failedAt time.Time, cause string) bool {
	return validQueueIdentity(resourceID, claimID) && attempt > 0 && !failedAt.IsZero() && strings.TrimSpace(cause) != ""
}

func reconciliationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := reconciliationRetryInitial
	for current := 1; current < attempt && delay < reconciliationRetryMaximum; current++ {
		if delay > reconciliationRetryMaximum/2 {
			return reconciliationRetryMaximum
		}
		delay *= 2
	}
	if delay > reconciliationRetryMaximum {
		return reconciliationRetryMaximum
	}
	return delay
}

func boundedReconciliationError(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= reconciliationErrorLimit {
		return value
	}
	end := reconciliationErrorLimit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func cloneAwaitEventNotifications(values []AwaitEventNotification) []AwaitEventNotification {
	out := make([]AwaitEventNotification, len(values))
	for index, value := range values {
		out[index] = value
		out[index].Event.Payload = append(json.RawMessage(nil), value.Event.Payload...)
	}
	return out
}

var (
	_ AwaitEventNotificationAPI = (*Service)(nil)
	_ DriverRunOutcomeAPI       = (*Service)(nil)
)
