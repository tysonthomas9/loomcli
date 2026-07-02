package memstore

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func cloneDriver(d *domain.Driver) *domain.Driver {
	out := *d
	out.Metadata = cloneMap(d.Metadata)
	return &out
}

func cloneDriverVersion(v *domain.DriverVersion) *domain.DriverVersion {
	out := *v
	out.Manifest = cloneMap(v.Manifest)
	return &out
}

func cloneTriggerBinding(b *domain.TriggerBinding) *domain.TriggerBinding {
	out := *b
	out.EventTypePatterns = append([]string(nil), b.EventTypePatterns...)
	out.ActorFilter = b.ActorFilter.Clone()
	out.Permissions = append([]string(nil), b.Permissions...)
	return &out
}

// normalizedActorFilterMem deep-copies an actor filter, normalizing a filter
// with no constraints to nil the way fleet-db's write path does.
func normalizedActorFilterMem(f *domain.TriggerActorFilter) *domain.TriggerActorFilter {
	if f.IsZero() {
		return nil
	}
	return f.Clone()
}

// defaultRetryFieldMem mirrors fleet-db's write-time retry defaulting: zero
// means "unset, use the default" (negatives are rejected upstream).
func defaultRetryFieldMem(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// redactedTriggerBinding clones b with the webhook signing secret cleared,
// mirroring the fleet-db API which never returns the secret on read/list/create
// responses. The secret is reachable only via ResolveWebhookSecret.
func redactedTriggerBinding(b *domain.TriggerBinding) *domain.TriggerBinding {
	out := cloneTriggerBinding(b)
	out.WebhookSecret = ""
	return out
}

func cloneDriverRun(r *domain.DriverRun) *domain.DriverRun {
	out := *r
	out.Payload = cloneJSON(r.Payload)
	out.Output = cloneMap(r.Output)
	out.FinishedAt = clonePtr(r.FinishedAt)
	out.SuspendedAt = clonePtr(r.SuspendedAt)
	out.CancelRequestedAt = clonePtr(r.CancelRequestedAt)
	return &out
}

func cloneJSON(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage(`{}`)
	}
	out := make(json.RawMessage, len(payload))
	copy(out, payload)
	return out
}

// cloneRawMessage deep-copies an optional raw payload, preserving nil so that
// omitempty fields round-trip unchanged for runs created without one.
func cloneRawMessage(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(payload))
	copy(out, payload)
	return out
}

func cloneDriverStep(s *domain.DriverStep) *domain.DriverStep {
	if s == nil {
		return nil
	}
	out := *s
	out.EndedAt = clonePtr(s.EndedAt)
	return &out
}

func cloneTaskRun(r *domain.TaskRun) *domain.TaskRun {
	out := *r
	out.ExitCode = clonePtr(r.ExitCode)
	out.RunnerPlacement = cloneTaskRunPlacement(r.RunnerPlacement)
	out.SandboxPlacement = cloneTaskRunPlacement(r.SandboxPlacement)
	out.RuntimeMetadata = cloneMap(r.RuntimeMetadata)
	out.Input = cloneRawMessage(r.Input)
	out.FinishedAt = clonePtr(r.FinishedAt)
	return &out
}

func cloneTaskRunPlacement(p domain.TaskRunPlacement) domain.TaskRunPlacement {
	out := p
	out.RetainedUntil = clonePtr(p.RetainedUntil)
	return out
}

func cloneTaskRunLogEntry(entry *domain.TaskRunLogEntry) *domain.TaskRunLogEntry {
	if entry == nil {
		return nil
	}
	out := *entry
	return &out
}

func mergeStringMapMem(base, patch map[string]string) map[string]string {
	if len(base) == 0 && len(patch) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}

func driverMatchesMem(d *domain.Driver, f store.DriverFilter) bool {
	return (f.Name == "" || d.Name == f.Name) && (f.Status == "" || d.Status == f.Status)
}

func driverVersionMatchesMem(v *domain.DriverVersion, f store.DriverVersionFilter) bool {
	return (f.DriverID == "" || v.DriverID == f.DriverID) && (f.ValidationStatus == "" || v.ValidationStatus == f.ValidationStatus)
}

func triggerBindingMatchesMem(b *domain.TriggerBinding, f store.TriggerBindingFilter) bool {
	return (f.SourceKind == "" || b.SourceKind == f.SourceKind) &&
		(f.RouteKey == "" || b.RouteKey == f.RouteKey) &&
		(f.DriverID == "" || b.DriverID == f.DriverID) &&
		(f.TargetAgentServiceID == "" || b.TargetAgentServiceID == f.TargetAgentServiceID) &&
		(f.Enabled == nil || b.Enabled == *f.Enabled)
}

func driverRunMatchesMem(r *domain.DriverRun, f store.DriverRunFilter) bool {
	return (f.DriverID == "" || r.DriverID == f.DriverID) &&
		(f.DriverVersionID == "" || r.DriverVersionID == f.DriverVersionID) &&
		(f.EpicID == "" || r.EpicID == f.EpicID) &&
		(f.NodeID == "" || r.NodeID == f.NodeID) &&
		(f.BindingID == "" || r.TriggerBindingID == f.BindingID) &&
		(f.Status == "" || r.Status == f.Status)
}

func driverStepMatchesMem(s *domain.DriverStep, f store.DriverStepFilter) bool {
	return (f.DriverRunID == "" || s.DriverRunID == f.DriverRunID) &&
		(f.TaskRunID == "" || s.TaskRunID == f.TaskRunID) &&
		(f.ActionLedgerID == "" || s.ActionLedgerID == f.ActionLedgerID) &&
		(f.StepKind == "" || s.StepKind == f.StepKind) &&
		(f.Status == "" || s.Status == f.Status)
}

func taskRunMatchesMem(r *domain.TaskRun, f store.TaskRunFilter) bool {
	return (f.DriverRunID == "" || r.DriverRunID == f.DriverRunID) &&
		(f.DriverStepID == "" || r.DriverStepID == f.DriverStepID) &&
		(f.TaskID == "" || r.TaskID == f.TaskID) &&
		(f.WorkerProfileID == "" || r.WorkerProfileID == f.WorkerProfileID) &&
		(f.Status == "" || r.Status == f.Status)
}

func claimCandidatesMem(runs map[string]*domain.TaskRun, taskRunID string) []*domain.TaskRun {
	out := make([]*domain.TaskRun, 0, len(runs))
	for _, run := range runs {
		if taskRunID != "" && run.TaskRunID != taskRunID {
			continue
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func taskRunMatchesClaimMem(run *domain.TaskRun, profile *domain.WorkerProfile, claim store.TaskRunClaim, now time.Time) bool {
	if run == nil || run.Status != domain.TaskRunQueued {
		return false
	}
	// Retry backoff: a zero NextEligibleAt keeps the run immediately claimable.
	if !run.NextEligibleAt.IsZero() && run.NextEligibleAt.After(now) {
		return false
	}
	if claim.TaskRunID != "" && run.TaskRunID != claim.TaskRunID {
		return false
	}
	if run.NodeID != "" && run.NodeID != claim.NodeID {
		return false
	}
	if run.WorkerProfileID != "" {
		if !stringListEmptyOrContainsMem(claim.WorkerProfileIDs, run.WorkerProfileID) {
			return false
		}
		if profile == nil || !profile.Enabled {
			return false
		}
		if profile.Backend != "" && !stringListEmptyOrContainsMem(claim.SupportedProviders, profile.Backend) {
			return false
		}
		if !stringListContainsAllMem(claim.Capabilities, profile.Capabilities) {
			return false
		}
	}
	if runHasNamedRunnerIdentityMem(run) {
		return true
	}
	provider := run.SandboxPlacement.Provider
	if provider == "" {
		provider = run.ProviderProfile
	}
	return provider == "" || stringListEmptyOrContainsMem(claim.SupportedProviders, provider)
}

func runHasNamedRunnerIdentityMem(run *domain.TaskRun) bool {
	if run == nil {
		return false
	}
	return strings.TrimSpace(run.Runner) != "" ||
		strings.TrimSpace(run.RunnerKind) != "" ||
		strings.TrimSpace(run.RunnerEntrypoint) != "" ||
		strings.TrimSpace(run.RunnerRef) != ""
}

func stringListEmptyOrContainsMem(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" || len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func stringListContainsAllMem(have, required []string) bool {
	for _, want := range required {
		if !stringListEmptyOrContainsMem(have, want) {
			return false
		}
	}
	return true
}

func nodeAdvertisedProvidersMem(node *domain.Node) []string {
	if node == nil {
		return nil
	}
	values := []string{string(node.RuntimeProvider)}
	values = append(values, node.Capabilities...)
	return normalizeStringListMem(values)
}

func normalizeStringListMem(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringListContainsAllStrictMem(have, required []string) bool {
	have = normalizeStringListMem(have)
	required = normalizeStringListMem(required)
	if len(required) == 0 {
		return true
	}
	values := map[string]struct{}{}
	for _, value := range have {
		values[value] = struct{}{}
	}
	for _, want := range required {
		if _, ok := values[want]; !ok {
			return false
		}
	}
	return true
}

func applyTriggerBindingUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
	applyTriggerBindingSourceUpdateMem(b, patch)
	applyTriggerBindingTargetUpdateMem(b, patch)
	applyTriggerBindingPolicyUpdateMem(b, patch)
	applyTriggerBindingRouterUpdateMem(b, patch)
}

func applyTriggerBindingSourceUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
	if patch.Name != nil {
		b.Name = *patch.Name
	}
	if patch.SourceKind != nil {
		b.SourceKind = *patch.SourceKind
	}
	if patch.SourceRef != nil {
		b.SourceRef = *patch.SourceRef
	}
	if patch.SourceConfigRef != nil {
		b.SourceConfigRef = *patch.SourceConfigRef
	}
	if patch.RouteKey != nil {
		b.RouteKey = *patch.RouteKey
	}
	if patch.Method != nil {
		b.Method = *patch.Method
	}
	if patch.PathTemplate != nil {
		b.PathTemplate = *patch.PathTemplate
	}
	if patch.Topic != nil {
		b.Topic = *patch.Topic
	}
	if patch.EventTypePatterns != nil {
		b.EventTypePatterns = append([]string(nil), (*patch.EventTypePatterns)...)
	}
	if patch.FilterRef != nil {
		b.FilterRef = *patch.FilterRef
	}
}

func applyTriggerBindingTargetUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
	if patch.DriverID != nil {
		b.DriverID = *patch.DriverID
	}
	if patch.DriverVersionID != nil {
		b.DriverVersionID = *patch.DriverVersionID
	}
	if patch.TargetEntrypoint != nil {
		b.TargetEntrypoint = *patch.TargetEntrypoint
	}
	if patch.TargetAgentServiceID != nil {
		b.TargetAgentServiceID = *patch.TargetAgentServiceID
	}
}

func applyTriggerBindingPolicyUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
	if patch.ConcurrencyPolicy != nil {
		b.ConcurrencyPolicy = *patch.ConcurrencyPolicy
	}
	if patch.IdempotencyPolicy != nil {
		b.IdempotencyPolicy = *patch.IdempotencyPolicy
	}
	if patch.AuthPolicy != nil {
		b.AuthPolicy = *patch.AuthPolicy
	}
	if patch.WebhookSecret != nil {
		b.WebhookSecret = *patch.WebhookSecret
	}
	if patch.Permissions != nil {
		b.Permissions = append([]string(nil), (*patch.Permissions)...)
	}
	if patch.Enabled != nil {
		b.Enabled = *patch.Enabled
	}
}

// applyTriggerBindingRouterUpdateMem applies the Router v2 binding fields.
// ActorFilter is replace-whole: a zero-valued filter clears it (normalized to
// nil), mirroring fleet-db's patch semantics.
func applyTriggerBindingRouterUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
	if patch.SubjectKeyTemplate != nil {
		b.SubjectKeyTemplate = *patch.SubjectKeyTemplate
	}
	if patch.ActorFilter != nil {
		b.ActorFilter = normalizedActorFilterMem(patch.ActorFilter)
	}
	if patch.RetryMaxAttempts != nil {
		b.RetryMaxAttempts = *patch.RetryMaxAttempts
	}
	if patch.RetryBackoffSeconds != nil {
		b.RetryBackoffSeconds = *patch.RetryBackoffSeconds
	}
	if patch.Schedule != nil {
		b.Schedule = *patch.Schedule
	}
	if patch.ScheduleTimezone != nil {
		b.ScheduleTimezone = *patch.ScheduleTimezone
	}
}
