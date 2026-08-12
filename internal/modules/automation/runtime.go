package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	CronEventType = "cron.tick"

	DefaultRuntimeSweepLimit = 50
	MaxRuntimeSweepLimit     = 500

	CronSchedulerComponentID   platformruntime.ComponentID = "serve-trigger-cron-scheduler"
	DeliverySweeperComponentID platformruntime.ComponentID = "serve-trigger-delivery-sweeper"

	DefaultCronSchedulerCadence = 30 * time.Second
	DefaultDeliverySweepCadence = 15 * time.Second
	CronOccurrenceClaimLease    = time.Minute
	DeliveryRetryClaimLease     = time.Minute
	deliveryRetryBackoffCap     = time.Hour

	cronAdmissionErrorClass    = "cron_admission_failed"
	deliveryInvalidResultClass = "execution_dispatch_invalid"
)

var _ RuntimeCommands = (*Service)(nil)

var (
	_ platformruntime.Component = (*cronSchedulerComponent)(nil)
	_ platformruntime.Component = (*deliverySweeperComponent)(nil)
)

type cronOutcome uint8

type deliveryRetryBatch struct {
	workspace  string
	now        time.Time
	claimUntil time.Time
	candidates []RetryCandidate
}

const (
	cronFailed cronOutcome = iota
	cronAdmitted
	cronDropped
)

// RuntimeAuthorityProvider issues a fresh, exact-action SystemAuthority for a
// registered Automation component pass. Runtime components receive neither an
// Issuer nor a reusable ambient system credential.
type RuntimeAuthorityProvider interface {
	AuthorityForAutomationRuntime(
		ctx context.Context,
		componentID platformruntime.ComponentID,
		workspace string,
		action authority.Action,
	) (authority.SystemAuthority, error)
}

// WorkspaceLister enumerates the current workspace keys for an unscoped serve
// runtime. Components call it on every pass so workspaces created after host
// startup are included without granting cross-workspace authority.
type WorkspaceLister interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
}

// AwaitEventNotification is Automation's adapter-neutral view of one
// durably admitted event that may satisfy an execution await. EventID is the
// canonical source event ID rather than the persistence-assigned event row ID,
// so direct dispatch and registration-time catch-up converge on one winner.
type AwaitEventNotification struct {
	WorkspaceKey string
	EventID      string
	EventType    string
	SourceKind   string
	Origin       EventOrigin
	SubjectRef   string
	ActorRef     string
	Payload      json.RawMessage
}

// AwaitEventNotifier bridges Automation's durable admission boundary to the
// execution await matcher. Implementations must be idempotent by workspace and
// EventID: a failed notification keeps the cron occurrence retryable, and the
// next admission replay deliberately invokes NotifyAwaitEvent again.
type AwaitEventNotifier interface {
	NotifyAwaitEvent(context.Context, AwaitEventNotification) error
}

// WithAwaitEventNotifier wires the post-admission await bridge used by cron.
// A missing notifier fails cron completion closed, leaving the occurrence
// eligible for retry rather than advancing the schedule past a lost wakeup.
func WithAwaitEventNotifier(notifier AwaitEventNotifier) Option {
	return func(service *Service) {
		service.awaits = notifier
	}
}

// RuntimeConfig contains the product scope and bounded page sizes for the two
// Automation runtime components. Cadence remains fixed in the registrations so
// lifecycle policy has one owner.
type RuntimeConfig struct {
	WorkspaceKey    string
	WorkspaceLister WorkspaceLister
	CronLimit       int
	DeliveryLimit   int
}

// RuntimeRegistrations constructs the two inert Automation components with
// their retained audit IDs and shipped lifecycle policies. Registration alone
// starts no goroutine; platform/runtime.Host owns launch and repetition.
func RuntimeRegistrations(commands RuntimeCommands, authorityProvider RuntimeAuthorityProvider, config RuntimeConfig) ([]platformruntime.Registration, error) {
	if commands == nil {
		return nil, fmt.Errorf("automation runtime commands are required: %w", ErrUnavailable)
	}
	if authorityProvider == nil {
		return nil, fmt.Errorf("automation runtime authority provider is required: %w", ErrUnavailable)
	}
	workspaces, err := newRuntimeWorkspaceScope(config.WorkspaceKey, config.WorkspaceLister)
	if err != nil {
		return nil, err
	}
	cronLimit, err := normalizeRuntimeLimit(config.CronLimit)
	if err != nil {
		return nil, fmt.Errorf("cron sweep limit: %w", err)
	}
	deliveryLimit, err := normalizeRuntimeLimit(config.DeliveryLimit)
	if err != nil {
		return nil, fmt.Errorf("delivery sweep limit: %w", err)
	}
	return []platformruntime.Registration{
		{
			Component: &cronSchedulerComponent{
				commands: commands, authority: authorityProvider,
				workspaces: workspaces, limit: cronLimit,
			},
			Policy: platformruntime.Policy{Cadence: DefaultCronSchedulerCadence, Immediate: true},
		},
		{
			Component: &deliverySweeperComponent{
				commands: commands, authority: authorityProvider,
				workspaces: workspaces, limit: deliveryLimit,
			},
			Policy: platformruntime.Policy{Cadence: DefaultDeliverySweepCadence, Immediate: true},
		},
	}, nil
}

type cronSchedulerComponent struct {
	commands   RuntimeCommands
	authority  RuntimeAuthorityProvider
	workspaces runtimeWorkspaceScope
	limit      int
}

func (*cronSchedulerComponent) ID() platformruntime.ComponentID { return CronSchedulerComponentID }

func (component *cronSchedulerComponent) RunOnce(ctx context.Context, _ time.Time) error {
	if component == nil || component.commands == nil || component.authority == nil {
		return ErrUnavailable
	}
	return runForRuntimeWorkspaces(ctx, component.workspaces, func(workspace string) error {
		auth, err := component.authority.AuthorityForAutomationRuntime(ctx, component.ID(), workspace, ActionSweepCron)
		if err != nil {
			return fmt.Errorf("derive cron scheduler authority for %q: %w", workspace, err)
		}
		_, err = component.commands.SweepCron(ctx, auth, SweepCronCommand{WorkspaceKey: workspace, Limit: component.limit})
		if err != nil {
			return fmt.Errorf("sweep cron in %q: %w", workspace, err)
		}
		return nil
	})
}

type deliverySweeperComponent struct {
	commands   RuntimeCommands
	authority  RuntimeAuthorityProvider
	workspaces runtimeWorkspaceScope
	limit      int
}

func (*deliverySweeperComponent) ID() platformruntime.ComponentID { return DeliverySweeperComponentID }

func (component *deliverySweeperComponent) RunOnce(ctx context.Context, _ time.Time) error {
	if component == nil || component.commands == nil || component.authority == nil {
		return ErrUnavailable
	}
	return runForRuntimeWorkspaces(ctx, component.workspaces, func(workspace string) error {
		auth, err := component.authority.AuthorityForAutomationRuntime(ctx, component.ID(), workspace, ActionRetryDeliveries)
		if err != nil {
			return fmt.Errorf("derive delivery sweeper authority for %q: %w", workspace, err)
		}
		_, err = component.commands.RetryDeliveries(ctx, auth, RetryDeliveriesCommand{WorkspaceKey: workspace, Limit: component.limit})
		if err != nil {
			return fmt.Errorf("retry deliveries in %q: %w", workspace, err)
		}
		return nil
	})
}

type runtimeWorkspaceScope struct {
	fixed  string
	lister WorkspaceLister
}

func newRuntimeWorkspaceScope(workspace string, lister WorkspaceLister) (runtimeWorkspaceScope, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" && lister == nil {
		return runtimeWorkspaceScope{}, fmt.Errorf("workspace lister is required for unscoped automation runtime: %w", ErrUnavailable)
	}
	return runtimeWorkspaceScope{fixed: workspace, lister: lister}, nil
}

func runForRuntimeWorkspaces(ctx context.Context, scope runtimeWorkspaceScope, run func(string) error) error {
	workspaces, err := scope.list(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := run(workspace); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (scope runtimeWorkspaceScope) list(ctx context.Context) ([]string, error) {
	if scope.fixed != "" {
		return []string{scope.fixed}, nil
	}
	workspaces, err := scope.lister.ListWorkspaceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list automation runtime workspaces: %w", err)
	}
	seen := make(map[string]struct{}, len(workspaces))
	for _, workspace := range workspaces {
		if strings.TrimSpace(workspace) == "" || workspace != strings.TrimSpace(workspace) {
			return nil, ErrInvalidPersistedState
		}
		if _, duplicate := seen[workspace]; duplicate {
			return nil, ErrInvalidPersistedState
		}
		seen[workspace] = struct{}{}
	}
	out := append([]string(nil), workspaces...)
	sort.Strings(out)
	return out, nil
}

// SweepCron claims durable schedule occurrences and feeds each one through the
// same authorized admission core used by webhook and execution-origin events.
// The runtime action is checked once up front; no cross-action authority is
// minted or reused.
func (s *Service) SweepCron(ctx context.Context, auth authority.SystemAuthority, command SweepCronCommand) (*SweepCronResult, error) {
	workspace, occurrences, err := s.claimCronOccurrences(ctx, auth, command)
	if err != nil {
		return nil, err
	}
	result := &SweepCronResult{Claimed: len(occurrences)}
	seen := make(map[string]struct{}, len(occurrences))
	var errs []error
	for _, item := range occurrences {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		occurrence, occurrenceErr := validateCronOccurrence(item, workspace)
		if occurrenceErr != nil {
			result.Failed++
			errs = append(errs, occurrenceErr)
			continue
		}
		key := occurrence.WorkspaceKey + "\x00" + occurrence.OccurrenceID
		if _, duplicate := seen[key]; duplicate {
			result.Failed++
			errs = append(errs, fmt.Errorf("duplicate cron occurrence %q: %w", occurrence.OccurrenceID, ErrInvalidPersistedState))
			continue
		}
		seen[key] = struct{}{}

		completion, outcome, admissionErr := s.admitCronOccurrence(ctx, auth, occurrence)
		recordCronOutcome(result, outcome)
		if admissionErr != nil {
			errs = append(errs, admissionErr)
		}
		if completionErr := s.cron.CompleteCron(ctx, completion); completionErr != nil {
			errs = append(errs, fmt.Errorf("complete cron occurrence %q: %w", occurrence.OccurrenceID, completionErr))
		}
	}
	return result, errors.Join(errs...)
}

func (s *Service) claimCronOccurrences(ctx context.Context, auth authority.SystemAuthority, command SweepCronCommand) (string, []CronOccurrence, error) {
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return "", nil, err
	}
	if s == nil || s.authority == nil {
		return "", nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireSystem(ActionSweepCron, workspace, auth); err != nil {
		return "", nil, err
	}
	if s.cron == nil {
		return "", nil, ErrUnavailable
	}
	limit, err := normalizeRuntimeLimit(command.Limit)
	if err != nil {
		return "", nil, err
	}
	now := s.now().UTC()
	claimUntil := now.Add(CronOccurrenceClaimLease)
	claim := CronClaim{
		WorkspaceKey: workspace, Before: now, ClaimUntil: claimUntil,
		IdempotencyKey: cronClaimIdempotencyKey(workspace, now, claimUntil, limit), Limit: limit,
	}
	occurrences, err := s.cron.ClaimDueCron(ctx, claim)
	if err != nil {
		return "", nil, fmt.Errorf("claim due cron occurrences: %w", err)
	}
	if len(occurrences) > limit {
		return "", nil, ErrInvalidPersistedState
	}
	return workspace, occurrences, nil
}

func (s *Service) admitCronOccurrence(ctx context.Context, auth authority.SystemAuthority, occurrence CronOccurrence) (CronCompletion, cronOutcome, error) {
	completion := CronCompletion{
		WorkspaceKey: occurrence.WorkspaceKey, BindingID: occurrence.BindingID,
		OccurrenceID: occurrence.OccurrenceID, Status: CronCompletionFailed,
	}
	payload, err := json.Marshal(struct {
		Tick string `json:"tick"`
	}{Tick: occurrence.OccurredAt.UTC().Format(time.RFC3339)})
	if err != nil {
		completion.ErrorClass = cronAdmissionErrorClass
		return completion, cronFailed, fmt.Errorf("encode cron occurrence %q: %w", occurrence.OccurrenceID, err)
	}
	admission, admissionErr := s.admitEventAuthorized(ctx, NewSystemEventAuthority(auth), cronAdmissionCommand(occurrence, payload))
	if admission == nil {
		completion.ErrorClass = cronAdmissionErrorClass
		if admissionErr == nil {
			admissionErr = ErrInvalidPersistedState
		}
		return completion, cronFailed, fmt.Errorf("admit cron occurrence %q: %w", occurrence.OccurrenceID, admissionErr)
	}
	if admission.Dropped {
		if admissionErr != nil {
			completion.ErrorClass = cronAdmissionErrorClass
			return completion, cronFailed, fmt.Errorf("admit cron occurrence %q: %w", occurrence.OccurrenceID, admissionErr)
		}
		completion.Status = CronCompletionDropped
		completion.ErrorClass = firstNonEmpty(strings.TrimSpace(admission.DropReason), DropReasonHopDepthExceeded)
		return completion, cronDropped, nil
	}
	notifyErr := s.notifyCronAwait(ctx, occurrence, admission)
	if admissionErr != nil || notifyErr != nil {
		completion.ErrorClass = cronAdmissionErrorClass
		return completion, cronFailed, errors.Join(
			wrapCronError("admit", occurrence.OccurrenceID, admissionErr),
			wrapCronError("notify await for", occurrence.OccurrenceID, notifyErr),
		)
	}
	completion.Status = CronCompletionAdmitted
	return completion, cronAdmitted, nil
}

func (s *Service) notifyCronAwait(ctx context.Context, occurrence CronOccurrence, admission *AdmissionResult) error {
	if s == nil || s.awaits == nil {
		return ErrUnavailable
	}
	if admission == nil || admission.Event == nil {
		return ErrInvalidPersistedState
	}
	event := admission.Event
	if event.WorkspaceKey != occurrence.WorkspaceKey || event.SourceKind != SourceKindCron ||
		event.SourceEventID != occurrence.OccurrenceID || event.EventType != CronEventType ||
		event.SubjectRef != occurrence.BindingID || event.Origin != EventOriginSystem ||
		strings.TrimSpace(event.ActorRef) == "" {
		return ErrInvalidPersistedState
	}
	notification := AwaitEventNotification{
		WorkspaceKey: event.WorkspaceKey,
		EventID:      event.SourceEventID,
		EventType:    event.EventType,
		SourceKind:   event.SourceKind,
		Origin:       event.Origin,
		SubjectRef:   event.SubjectRef,
		ActorRef:     event.ActorRef,
		Payload:      cloneRawMessage(event.Payload),
	}
	if err := s.awaits.NotifyAwaitEvent(ctx, notification); err != nil {
		return err
	}
	return nil
}

func wrapCronError(action, occurrenceID string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s cron occurrence %q: %w", action, occurrenceID, err)
}

func cronAdmissionCommand(occurrence CronOccurrence, payload json.RawMessage) AdmitEventCommand {
	return AdmitEventCommand{
		WorkspaceKey: occurrence.WorkspaceKey, SourceKind: SourceKindCron,
		SourceRef: occurrence.BindingID, RouteKey: occurrence.RouteKey,
		SourceEventID: occurrence.OccurrenceID, EventType: CronEventType,
		SubjectRef: occurrence.BindingID, OccurredAt: occurrence.OccurredAt, Payload: payload,
	}
}

func recordCronOutcome(result *SweepCronResult, outcome cronOutcome) {
	switch outcome {
	case cronAdmitted:
		result.Admitted++
	case cronDropped:
		result.Dropped++
	default:
		result.Failed++
	}
}

// RetryDeliveries claims due durable delivery records and replays the immutable
// dispatch snapshot. It never re-matches bindings or resolves a new Workflow
// Catalog version after admission.
func (s *Service) RetryDeliveries(ctx context.Context, auth authority.SystemAuthority, command RetryDeliveriesCommand) (*RetryDeliveriesResult, error) {
	batch, err := s.claimDeliveryRetries(ctx, auth, command)
	if err != nil {
		return nil, err
	}
	result := &RetryDeliveriesResult{Claimed: len(batch.candidates)}
	seen := make(map[string]struct{}, len(batch.candidates))
	var errs []error
	for _, candidate := range batch.candidates {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := validateRetryCandidate(candidate, batch.workspace, batch.now, batch.claimUntil); err != nil {
			result.Failed++
			errs = append(errs, err)
			continue
		}
		key := candidate.Delivery.WorkspaceKey + "\x00" + candidate.Delivery.DeliveryID
		if _, duplicate := seen[key]; duplicate {
			result.Failed++
			errs = append(errs, fmt.Errorf("duplicate retry candidate %q: %w", candidate.Delivery.DeliveryID, ErrInvalidPersistedState))
			continue
		}
		seen[key] = struct{}{}

		updated, retryErr := s.retryDelivery(ctx, candidate, batch.now)
		if retryErr != nil {
			result.Failed++
			errs = append(errs, retryErr)
			continue
		}
		classifyRetryResult(result, updated)
	}
	return result, errors.Join(errs...)
}

func (s *Service) claimDeliveryRetries(ctx context.Context, auth authority.SystemAuthority, command RetryDeliveriesCommand) (*deliveryRetryBatch, error) {
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireSystem(ActionRetryDeliveries, workspace, auth); err != nil {
		return nil, err
	}
	if s.retries == nil || s.admissions == nil || s.execution == nil {
		return nil, ErrUnavailable
	}
	limit, err := normalizeRuntimeLimit(command.Limit)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	claimUntil := now.Add(DeliveryRetryClaimLease)
	candidates, err := s.retries.ClaimDueDeliveries(ctx, workspace, now, claimUntil, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due deliveries: %w", err)
	}
	if len(candidates) > limit {
		return nil, ErrInvalidPersistedState
	}
	return &deliveryRetryBatch{workspace: workspace, now: now, claimUntil: claimUntil, candidates: candidates}, nil
}

func (s *Service) retryDelivery(ctx context.Context, candidate RetryCandidate, now time.Time) (*Delivery, error) {
	request := retryExecutionRequest(candidate)
	if err := validateExecutionDispatchRequest(request); err != nil {
		return nil, err
	}
	dispatch, err := s.execution.Dispatch(ctx, request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return s.recordDeliveryRetry(ctx, candidate, now, retryStatus(candidate.Delivery), DeliveryErrorDispatchFailed)
	}
	return finishDeliveryRetry(candidate, dispatch)
}

func retryExecutionRequest(candidate RetryCandidate) ExecutionDispatchRequest {
	payload := candidate.Payload
	if payload == nil {
		payload = candidate.Event.Payload
	}
	attrs := candidate.SubjectAttrs
	if attrs == nil {
		attrs = candidate.Event.SubjectAttrs
	}
	epicID := strings.TrimSpace(candidate.EpicID)
	if epicID == "" {
		epicID = strings.TrimSpace(candidate.Event.EpicID)
	}
	return ExecutionDispatchRequest{
		WorkspaceKey:            candidate.Event.WorkspaceKey,
		IdempotencyKey:          candidate.Event.IdempotencyKey + "#" + candidate.Delivery.TriggerBindingID,
		ExpectedDeliveryStatus:  candidate.Delivery.Status,
		ExpectedDeliveryAttempt: candidate.Delivery.Attempt,
		DriverID:                candidate.Target.DriverID,
		DriverVersionID:         candidate.Target.DriverVersionID,
		DriverRevision:          candidate.Target.DriverRevision,
		SourceDigest:            candidate.Target.SourceDigest,
		BundleDigest:            candidate.Target.BundleDigest,
		Entrypoint:              candidate.Target.Entrypoint,
		TargetAgentServiceID:    candidate.Target.TargetAgentServiceID,
		DeliveryID:              candidate.Delivery.DeliveryID,
		SourceKind:              candidate.Target.SourceKind,
		SourceRef:               candidate.Event.EventID,
		SubjectRef:              candidate.Event.SubjectRef,
		TriggerBindingID:        candidate.Delivery.TriggerBindingID,
		SubjectKey:              candidate.Delivery.SubjectKey,
		ConcurrencyPolicy:       candidate.Target.ConcurrencyPolicy,
		EpicID:                  epicID,
		ActorRef:                candidate.Event.ActorRef,
		RawPayloadRef:           candidate.Event.RawPayloadRef,
		Payload:                 cloneRawMessage(payload),
		SubjectAttrs:            cloneStringMap(attrs),
	}
}

func cronClaimIdempotencyKey(workspace string, before, claimUntil time.Time, limit int) string {
	stable := fmt.Sprintf("%s\x00%s\x00%s\x00%d", workspace,
		before.UTC().Format(time.RFC3339Nano), claimUntil.UTC().Format(time.RFC3339Nano), limit)
	sum := sha256.Sum256([]byte(stable))
	return "automation-cron-claim:" + hex.EncodeToString(sum[:])
}

func finishDeliveryRetry(candidate RetryCandidate, dispatch *ExecutionDispatchResult) (*Delivery, error) {
	return validateCommittedDispatchResult(retryExecutionRequest(candidate), dispatch, candidate.Target)
}

func (s *Service) recordDeliveryRetry(ctx context.Context, candidate RetryCandidate, now time.Time, status DeliveryStatus, errorClass string) (*Delivery, error) {
	attempt := candidate.Delivery.Attempt
	maxAttempts := candidate.Target.RetryMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultRetryMaxAttempts
	}
	transition := DeliveryTransition{
		WorkspaceKey:    candidate.Delivery.WorkspaceKey,
		DeliveryID:      candidate.Delivery.DeliveryID,
		ExpectedStatus:  candidate.Delivery.Status,
		ExpectedAttempt: candidate.Delivery.Attempt,
		Status:          status,
		Attempt:         attempt,
		ErrorClass:      errorClass,
	}
	if attempt >= maxAttempts {
		transition.Status = DeliveryFailed
		transition.ErrorClass = DeliveryErrorRetriesExhausted
	} else {
		next := now.Add(deliveryRetryBackoff(candidate.Target.RetryBackoff, attempt))
		transition.NextRetryAt = &next
	}
	transition.IdempotencyKey = deliveryTransitionKey(transition)
	return s.transitionDelivery(ctx, transition, candidate.Target)
}

func validateCronOccurrence(occurrence CronOccurrence, workspace string) (CronOccurrence, error) {
	if err := validateWorkspace(occurrence.WorkspaceKey, workspace); err != nil {
		return CronOccurrence{}, err
	}
	bindingID, err := requireCanonical("binding id", occurrence.BindingID)
	if err != nil {
		return CronOccurrence{}, errors.Join(ErrInvalidPersistedState, err)
	}
	occurrenceID, err := requireCanonical("occurrence id", occurrence.OccurrenceID)
	if err != nil || !strings.HasPrefix(occurrenceID, "cron:") {
		return CronOccurrence{}, errors.Join(ErrInvalidPersistedState, err)
	}
	if occurrence.OccurredAt.IsZero() {
		return CronOccurrence{}, ErrInvalidPersistedState
	}
	routeKey := strings.TrimSpace(occurrence.RouteKey)
	if routeKey == "" {
		routeKey = "cron:" + bindingID
	} else if routeKey != occurrence.RouteKey {
		return CronOccurrence{}, ErrInvalidPersistedState
	}
	return CronOccurrence{
		WorkspaceKey: workspace,
		BindingID:    bindingID,
		RouteKey:     routeKey,
		OccurrenceID: occurrenceID,
		OccurredAt:   occurrence.OccurredAt.UTC(),
	}, nil
}

func validateRetryCandidate(candidate RetryCandidate, workspace string, now, claimUntil time.Time) error {
	if err := validatePersistedDelivery(candidate.Delivery, workspace, "", ""); err != nil {
		return err
	}
	if err := validatePersistedEvent(candidate.Event, workspace, candidate.Delivery.TriggerEventID); err != nil {
		return err
	}
	if candidate.Target == nil {
		return ErrInvalidPersistedState
	}
	if !deliveryMatchesTarget(candidate.Delivery, candidate.Target) {
		return ErrInvalidPersistedState
	}
	if err := validateRetryTarget(candidate.Target, candidate.Delivery); err != nil {
		return err
	}
	if err := validateClaimedDelivery(candidate.Delivery, now, claimUntil); err != nil {
		return err
	}
	if strings.TrimSpace(candidate.Event.IdempotencyKey) == "" || candidate.Event.IdempotencyKey != strings.TrimSpace(candidate.Event.IdempotencyKey) {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateRetryTarget(target *DispatchTarget, delivery *Delivery) error {
	if target == nil || delivery == nil {
		return ErrInvalidPersistedState
	}
	if target.BindingID != delivery.TriggerBindingID ||
		strings.TrimSpace(target.DriverID) == "" || target.DriverID != strings.TrimSpace(target.DriverID) ||
		strings.TrimSpace(target.DriverVersionID) == "" || target.DriverVersionID != strings.TrimSpace(target.DriverVersionID) ||
		target.RetryMaxAttempts < 0 || target.RetryBackoff < 0 {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateClaimedDelivery(delivery *Delivery, now, claimUntil time.Time) error {
	if delivery == nil || delivery.Attempt < 2 || delivery.NextRetryAt == nil ||
		!delivery.NextRetryAt.After(now) || delivery.NextRetryAt.After(claimUntil) {
		return ErrInvalidPersistedState
	}
	switch delivery.Status {
	case DeliveryHeld:
	case DeliveryFailed:
		if delivery.ErrorClass == DeliveryErrorRetriesExhausted {
			return ErrInvalidPersistedState
		}
	default:
		return ErrInvalidPersistedState
	}
	return nil
}

func normalizeRuntimeLimit(limit int) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("limit cannot be negative: %w", ErrInvalid)
	}
	if limit == 0 {
		return DefaultRuntimeSweepLimit, nil
	}
	if limit > MaxRuntimeSweepLimit {
		return MaxRuntimeSweepLimit, nil
	}
	return limit, nil
}

func deliveryRetryBackoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = time.Duration(DefaultRetryBackoffSeconds) * time.Second
	}
	if base >= deliveryRetryBackoffCap {
		return deliveryRetryBackoffCap
	}
	delay := base
	for index := 1; index < attempt; index++ {
		if delay >= deliveryRetryBackoffCap/2 {
			return deliveryRetryBackoffCap
		}
		delay *= 2
	}
	return delay
}

func retryStatus(delivery *Delivery) DeliveryStatus {
	if delivery != nil && delivery.Status == DeliveryHeld {
		return DeliveryHeld
	}
	return DeliveryFailed
}

func classifyRetryResult(result *RetryDeliveriesResult, delivery *Delivery) {
	if result == nil || delivery == nil {
		return
	}
	switch delivery.Status {
	case DeliveryDispatched:
		result.Dispatched++
	case DeliveryHeld:
		result.Held++
	case DeliveryFailed:
		if delivery.ErrorClass == DeliveryErrorRetriesExhausted {
			result.Exhausted++
		} else {
			result.Failed++
		}
	default:
		result.Failed++
	}
}
