package automation

import (
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Service struct {
	bindings          BindingStore
	unmanagedBindings UnmanagedBindingStore
	managedBindings   ManagedBindingStore
	matcher           BindingMatcher
	events            EventReader
	approvalEvents    ApprovalEventStore
	deliveries        DeliveryReader
	admissions        AdmissionStore
	execution         ExecutionPort
	catalog           workflowcatalog.EffectiveVersionResolver
	catalogAuthority  EffectiveVersionAuthorityProvider
	cron              CronSweepPort
	retries           DeliveryRetryPort
	awaits            AwaitEventNotifier
	eventTrustPolicy  EventTrustPolicy
	authority         *authority.Admission
	now               func() time.Time
	hopDepthCap       int
}

var (
	_ BindingCommands             = (*Service)(nil)
	_ ManagedBindingCommands      = (*Service)(nil)
	_ ProvisioningBindingCommands = (*Service)(nil)
	_ BindingQueries              = (*Service)(nil)
	_ EventQueries                = (*Service)(nil)
	_ DeliveryQueries             = (*Service)(nil)
	_ EventAdmission              = (*Service)(nil)
	_ ApprovalJournal             = (*Service)(nil)
	_ ManualDispatch              = (*Service)(nil)
)

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func WithEventHopDepthCap(cap int) Option {
	return func(service *Service) {
		if cap > 0 {
			service.hopDepthCap = cap
		}
	}
}

// WithEventTrustPolicy supplies the required provenance gate for event
// admission. A service without this port remains usable for read-only and
// binding-management operations, but event admission fails closed.
func WithEventTrustPolicy(policy EventTrustPolicy) Option {
	return func(service *Service) {
		service.eventTrustPolicy = policy
	}
}

// WithApprovalEventStore supplies the journal-only persistence seam used by
// authenticated approval decisions. It is deliberately separate from event
// admission because approvals must remain durable even without a matching
// trigger binding.
func WithApprovalEventStore(events ApprovalEventStore) Option {
	return func(service *Service) {
		service.approvalEvents = events
	}
}

// WithRuntimePorts wires the Automation-owned persistence seams used by the
// separately implemented cron and delivery-retry runtime commands.
func WithRuntimePorts(cron CronSweepPort, retries DeliveryRetryPort) Option {
	return func(service *Service) {
		service.cron = cron
		service.retries = retries
	}
}

// New constructs the Automation core. Dependencies are checked at the method
// that uses them so read-only seams can be composed before command adapters.
// Missing command dependencies fail closed as ErrUnavailable.
func New(
	bindings BindingStore,
	unmanagedBindings UnmanagedBindingStore,
	managedBindings ManagedBindingStore,
	matcher BindingMatcher,
	events EventReader,
	deliveries DeliveryReader,
	admissions AdmissionStore,
	execution ExecutionPort,
	catalog workflowcatalog.EffectiveVersionResolver,
	catalogAuthority EffectiveVersionAuthorityProvider,
	authorityAdmission *authority.Admission,
	options ...Option,
) *Service {
	service := &Service{
		bindings:          bindings,
		unmanagedBindings: unmanagedBindings,
		managedBindings:   managedBindings,
		matcher:           matcher,
		events:            events,
		deliveries:        deliveries,
		admissions:        admissions,
		execution:         execution,
		catalog:           catalog,
		catalogAuthority:  catalogAuthority,
		authority:         authorityAdmission,
		now:               time.Now,
		hopDepthCap:       DefaultEventHopDepthCap,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func normalizeRequired(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required: %w", label, ErrInvalid)
	}
	return value, nil
}

func requireCanonical(label, value string) (string, error) {
	trimmed, err := normalizeRequired(label, value)
	if err != nil {
		return "", err
	}
	if trimmed != value {
		return "", fmt.Errorf("%s must not contain surrounding whitespace: %w", label, ErrInvalid)
	}
	return trimmed, nil
}

func normalizeBindingCommand(command BindingCommand) (BindingCommand, error) {
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return BindingCommand{}, err
	}
	bindingID, err := requireCanonical("binding id", command.BindingID)
	if err != nil {
		return BindingCommand{}, err
	}
	return BindingCommand{WorkspaceKey: workspace, BindingID: bindingID}, nil
}

func validateWorkspace(actual, expected string) error {
	if strings.TrimSpace(actual) != expected || actual != strings.TrimSpace(actual) {
		return ErrWrongWorkspace
	}
	return nil
}

func validConcurrencyPolicy(policy BindingConcurrencyPolicy) bool {
	switch policy {
	case ConcurrencyAllow, ConcurrencyForbid, ConcurrencyReplace,
		ConcurrencyQueue, ConcurrencyOneActivePerEpic:
		return true
	default:
		return false
	}
}
