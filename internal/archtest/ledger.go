package archtest

import (
	"errors"
	"fmt"
	"slices"
)

// MutationLedger is the machine-readable ownership, authority, and durability
// contract for commands selected for migration. Planned commands may name tests
// that become mandatory in their implementation slice; the ledger itself is a
// Phase 1 gate and cannot omit any correctness dimension.
type MutationLedger struct {
	SchemaVersion int               `yaml:"schema_version"`
	Status        string            `yaml:"status"`
	Commands      []MutationCommand `yaml:"commands"`
}

type MutationCommand struct {
	ID                     string   `yaml:"id"`
	Phase                  string   `yaml:"phase"`
	AggregateOwner         string   `yaml:"aggregate_owner"`
	CoordinatingOwner      string   `yaml:"coordinating_owner"`
	MechanismDiscriminator string   `yaml:"mechanism_discriminator"`
	InstanceOwner          string   `yaml:"instance_owner"`
	AcceptedAuthority      []string `yaml:"accepted_authority"`
	DurableCommands        []string `yaml:"durable_commands"`
	Boundary               string   `yaml:"transaction_or_saga_boundary"`
	IdempotencyKey         string   `yaml:"idempotency_key"`
	SemanticEffects        []string `yaml:"semantic_events_or_read_models"`
	RetryRestartBehavior   string   `yaml:"retry_restart_behavior"`
	NegativeTests          []string `yaml:"negative_tests"`
	FaultInjectionTests    []string `yaml:"fault_injection_tests"`
}

var requiredMutationCommandIDs = []string{
	"artifacts.declare",
	"artifacts.finalize",
	"artifacts.reference",
	"artifacts.upload",
	"automation.admit-event",
	"automation.create-binding",
	"automation.create-managed-binding",
	"automation.delete-binding",
	"automation.delete-managed-binding",
	"automation.disable-binding",
	"automation.disable-managed-binding",
	"automation.dispatch-binding",
	"automation.enable-binding",
	"automation.enable-managed-binding",
	"automation.retry-deliveries",
	"automation.sweep-cron",
	"automation.update-binding",
	"automation.update-managed-binding",
	"execution.append-log",
	"execution.await-driver-run",
	"execution.cascade-child-driver-runs",
	"execution.claim-await-event-notifications",
	"execution.claim-driver-run",
	"execution.claim-driver-run-outcomes",
	"execution.claim-driver-run-work-item",
	"execution.claim-task-run",
	"execution.claim-terminal-driver-run-work-recoveries",
	"execution.complete-await-event-notification",
	"execution.complete-driver-run-outcome",
	"execution.complete-terminal-driver-run-work-recovery",
	"execution.converge-task-run",
	"execution.create-worker-profile",
	"execution.delete-worker-profile",
	"execution.exhaust-task-run-retries",
	"execution.finalize",
	"execution.finalize-driver-run",
	"execution.heartbeat",
	"execution.heartbeat-driver-run",
	"execution.heartbeat-worker-node",
	"execution.recover-child-driver-run-cascade",
	"execution.recover-driver-runs",
	"execution.recover-stale-child-task-runs",
	"execution.recover-terminal-driver-run-work",
	"execution.register-worker-node",
	"execution.release-driver-run-work-item",
	"execution.repair-terminal-driver-step",
	"execution.request-task-run",
	"execution.requeue-task-run",
	"execution.resolve-driver-await",
	"execution.retry-await-event-notification",
	"execution.retry-driver-run-outcome",
	"execution.retry-terminal-driver-run-work-recovery",
	"execution.set-worker-node-drain",
	"execution.start-child-driver-run",
	"execution.submit-driver-run",
	"execution.update-worker-profile",
	"workflowcatalog.activate-version",
	"workflowcatalog.approve-version",
	"workflowcatalog.unapprove-version",
}

func LoadMutationLedger(path string) (MutationLedger, error) {
	var value MutationLedger
	if err := decodeYAML(path, &value); err != nil {
		return MutationLedger{}, fmt.Errorf("decode mutation ledger: %w", err)
	}
	if err := value.Validate(); err != nil {
		return MutationLedger{}, err
	}
	return value, nil
}

func (l MutationLedger) Validate() error {
	if l.SchemaVersion != SchemaVersion {
		return fmt.Errorf("mutation ledger schema_version: got %d, want %d", l.SchemaVersion, SchemaVersion)
	}
	if l.Status != "reviewed" {
		return fmt.Errorf("mutation ledger status %q is unsupported", l.Status)
	}
	if len(l.Commands) == 0 {
		return errors.New("mutation ledger must declare at least one command")
	}
	return validateMutationCommands(l.Commands)
}

func validateMutationCommands(commands []MutationCommand) error {
	ids := make([]string, 0, len(commands))
	for _, command := range commands {
		if err := validateMutationCommand(command); err != nil {
			return err
		}
		ids = append(ids, command.ID)
	}
	if !slices.IsSorted(ids) {
		return errors.New("mutation commands must be sorted by id")
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return fmt.Errorf("duplicate mutation command %q", ids[i])
		}
	}
	for _, required := range requiredMutationCommandIDs {
		if !slices.Contains(ids, required) {
			return fmt.Errorf("mutation ledger is missing required migrated command %s", required)
		}
	}
	return nil
}

func validateMutationCommand(command MutationCommand) error {
	if command.ID == "" || command.Phase == "" || command.AggregateOwner == "" ||
		command.CoordinatingOwner == "" || command.MechanismDiscriminator == "" ||
		command.InstanceOwner == "" || command.Boundary == "" || command.IdempotencyKey == "" ||
		command.RetryRestartBehavior == "" {
		return fmt.Errorf("mutation command %q omits a required ownership or durability field", command.ID)
	}
	for label, values := range map[string][]string{
		"accepted_authority":             command.AcceptedAuthority,
		"durable_commands":               command.DurableCommands,
		"semantic_events_or_read_models": command.SemanticEffects,
		"negative_tests":                 command.NegativeTests,
		"fault_injection_tests":          command.FaultInjectionTests,
	} {
		if len(values) == 0 {
			return fmt.Errorf("mutation command %s has no %s", command.ID, label)
		}
		if err := validateSortedUnique(command.ID+" "+label, values); err != nil {
			return err
		}
	}
	return nil
}
