package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
)

const (
	agentRecordKindPrompt      = "prompt"
	agentRecordKindScripted    = "scripted"
	agentRecordKindInteractive = "interactive"
	agentArchiveMetadataKey    = "archived_at"
)

type promptAgentCreateInput struct {
	Name         string
	Backend      string
	Behavior     promptAgentBehaviorCreate
	Trigger      promptAgentTriggerRequest
	Grants       []promptAgentGrantRequest
	Enabled      *bool
	BudgetPolicy string
}

type promptAgentBehaviorCreate struct {
	RoleName   string
	RoleCreate *promptRoleCreateInput
}

type promptRoleCreateInput struct {
	Prompt         string
	PromptFilename string
	Description    string
	TaskFilter     string
	Model          string
	Backend        string
	Effort         string
	ReadOnly       bool
	AllowedTools   []string
	DeniedTools    []string
	Skills         []string
}

type promptAgentTriggerRequest struct {
	SourceKind        string
	RouteKey          string
	BindingID         string
	EventTypePatterns []string
	Schedule          string
	ScheduleTimezone  string
	Entrypoint        string
}

type promptAgentGrantRequest struct {
	ConnectorID     string
	Action          string
	ResourcePattern string
	GrantID         string
}

func (m *Module) agentRecordDTO(ctx context.Context, ws string, record *agentsmodule.AgentServiceRecord, now time.Time) (loomapi.UnifiedAgent, error) {
	kind, err := m.canonicalAgentRecordKind(ctx, ws, record)
	if err != nil {
		out, mapErr := newAgentRecordDTO(record, deriveAgentRecordKind(record), nil, nil)
		if mapErr != nil {
			return loomapi.UnifiedAgent{}, mapErr
		}
		return out, err
	}
	if m.bindings == nil {
		out, mapErr := newAgentRecordDTO(record, kind, nil, nil)
		if mapErr != nil {
			return loomapi.UnifiedAgent{}, mapErr
		}
		return out, automation.ErrUnavailable
	}
	bindings, err := m.bindings.ListBindings(ctx, ws, automation.BindingFilter{TargetAgentServiceID: record.ServiceID})
	if err != nil {
		out, mapErr := newAgentRecordDTO(record, kind, nil, nil)
		if mapErr != nil {
			return loomapi.UnifiedAgent{}, mapErr
		}
		return out, err
	}
	return m.agentRecordDTOWithBindingsAndKind(ctx, ws, record, kind, bindings, now)
}

func (m *Module) canonicalAgentRecordKind(
	ctx context.Context,
	workspace string,
	record *agentsmodule.AgentServiceRecord,
) (string, error) {
	if record == nil {
		return "", agentsmodule.ErrInvalidPersistedState
	}
	runtime, err := agentsmodule.ParseRuntimeMetadata(record.Metadata)
	if err != nil {
		return "", err
	}
	if runtime.RoleKind == string(agentsmodule.RoleKindInteractive) {
		return agentRecordKindInteractive, nil
	}
	if runtime.RoleKind == string(agentsmodule.RoleKindWorker) {
		return agentRecordKindPrompt, nil
	}
	if strings.TrimSpace(record.RoleName) == "" {
		return agentRecordKindScripted, nil
	}
	if m == nil || m.agentRoleQueries == nil {
		return "", agentsmodule.ErrUnavailable
	}
	role, err := m.agentRoleQueries.GetRole(ctx, workspace, record.RoleName)
	if err != nil {
		return "", err
	}
	if role == nil || role.WorkspaceKey != workspace || role.Name != record.RoleName {
		return "", agentsmodule.ErrInvalidPersistedState
	}
	if strings.TrimSpace(role.Kind) == string(agentsmodule.RoleKindInteractive) {
		return agentRecordKindInteractive, nil
	}
	return agentRecordKindPrompt, nil
}

func (m *Module) agentRecordDTOWithBindings(
	ctx context.Context,
	ws string,
	record *agentsmodule.AgentServiceRecord,
	bindings []*automation.Binding,
	now time.Time,
) (loomapi.UnifiedAgent, error) {
	kind, err := m.canonicalAgentRecordKind(ctx, ws, record)
	if err != nil {
		return loomapi.UnifiedAgent{}, err
	}
	return m.agentRecordDTOWithBindingsAndKind(ctx, ws, record, kind, bindings, now)
}

func (m *Module) agentRecordDTOWithBindingsAndKind(
	ctx context.Context,
	ws string,
	record *agentsmodule.AgentServiceRecord,
	kind string,
	bindings []*automation.Binding,
	now time.Time,
) (loomapi.UnifiedAgent, error) {
	decorators := make([]triggerbindings.BindingDecorators, 0, len(bindings))
	transportBindings := make([]loomapi.AgentRecordBinding, 0, len(bindings))
	for _, b := range bindings {
		if b == nil {
			continue
		}
		dec := triggerbindings.DecorateBinding(ctx, m.bindingRuns, ws, b, now)
		decorators = append(decorators, dec)
		transportBindings = append(transportBindings, agentRecordBindingDTO(b, dec))
	}
	lastStatus, failures, next := aggregateBindingDecorators(decorators)
	return newAgentRecordDTO(record, kind, transportBindings, &agentRecordDecorators{
		lastRunStatus: lastStatus, consecutiveFailures: failures, nextFireAt: next,
	})
}

type agentRecordDecorators struct {
	lastRunStatus       string
	consecutiveFailures int
	nextFireAt          *time.Time
}

func newAgentRecordDTO(
	record *agentsmodule.AgentServiceRecord,
	kind string,
	bindings []loomapi.AgentRecordBinding,
	decorators *agentRecordDecorators,
) (loomapi.UnifiedAgent, error) {
	fields := agentRecordFields(record, bindings, decorators)
	var out loomapi.UnifiedAgent
	switch kind {
	case agentRecordKindInteractive:
		err := out.FromInteractiveAgentRecord(loomapi.InteractiveAgentRecord{
			Behavior: loomapi.PromptAgentBehavior{RoleName: record.RoleName},
			Bindings: fields.bindings, BudgetPolicy: fields.budgetPolicy,
			ConsecutiveFailures: fields.consecutiveFailures, CreatedAt: record.CreatedAt,
			Enabled: fields.enabled, Id: record.ServiceID,
			Kind:          loomapi.InteractiveAgentRecordKindInteractive,
			LastRunStatus: fields.lastRunStatus, Metadata: fields.metadata,
			Name: record.Name, NextFireAt: fields.nextFireAt,
			UpdatedAt: record.UpdatedAt, WorkspaceKey: record.WorkspaceKey,
		})
		return out, err
	case agentRecordKindScripted:
		err := out.FromScriptedAgentRecord(loomapi.ScriptedAgentRecord{
			Behavior: loomapi.ScriptedAgentBehavior{
				DriverId: record.DriverID, DriverVersionId: record.DriverVersionID,
			},
			Bindings: fields.bindings, BudgetPolicy: fields.budgetPolicy,
			ConsecutiveFailures: fields.consecutiveFailures, CreatedAt: record.CreatedAt,
			Enabled: fields.enabled, Id: record.ServiceID, Kind: loomapi.Scripted,
			LastRunStatus: fields.lastRunStatus, Metadata: fields.metadata,
			Name: record.Name, NextFireAt: fields.nextFireAt,
			UpdatedAt: record.UpdatedAt, WorkspaceKey: record.WorkspaceKey,
		})
		return out, err
	default:
		err := out.FromPromptAgentRecord(loomapi.PromptAgentRecord{
			Behavior: loomapi.PromptAgentBehavior{RoleName: record.RoleName},
			Bindings: fields.bindings, BudgetPolicy: fields.budgetPolicy,
			ConsecutiveFailures: fields.consecutiveFailures, CreatedAt: record.CreatedAt,
			Enabled: fields.enabled, Id: record.ServiceID,
			Kind:          loomapi.PromptAgentRecordKindPrompt,
			LastRunStatus: fields.lastRunStatus, Metadata: fields.metadata,
			Name: record.Name, NextFireAt: fields.nextFireAt,
			UpdatedAt: record.UpdatedAt, WorkspaceKey: record.WorkspaceKey,
		})
		return out, err
	}
}

type agentRecordTransportFields struct {
	bindings            *[]loomapi.AgentRecordBinding
	budgetPolicy        *string
	consecutiveFailures *int
	enabled             bool
	lastRunStatus       *string
	metadata            *map[string]string
	nextFireAt          *time.Time
}

func agentRecordFields(
	record *agentsmodule.AgentServiceRecord,
	bindings []loomapi.AgentRecordBinding,
	decorators *agentRecordDecorators,
) agentRecordTransportFields {
	fields := agentRecordTransportFields{
		bindings:     optionalAgentRecordBindings(bindings),
		budgetPolicy: optionalAgentRecordString(record.BudgetPolicy),
		enabled:      record.DesiredState == agentsmodule.DesiredRunning,
		metadata:     optionalAgentRecordMap(cloneStringMap(record.Metadata)),
	}
	if decorators != nil {
		fields.consecutiveFailures = optionalAgentRecordInt(decorators.consecutiveFailures)
		fields.lastRunStatus = optionalAgentRecordString(decorators.lastRunStatus)
		fields.nextFireAt = decorators.nextFireAt
	}
	return fields
}

func agentRecordBindingDTO(binding *automation.Binding, decorators triggerbindings.BindingDecorators) loomapi.AgentRecordBinding {
	return loomapi.AgentRecordBinding{
		ActorFilter:          agentRecordActorFilter(binding.ActorFilter),
		AuthPolicy:           optionalAgentRecordString(binding.AuthPolicy),
		BindingId:            binding.BindingID,
		ConcurrencyPolicy:    string(binding.ConcurrencyPolicy),
		ConsecutiveFailures:  optionalAgentRecordInt(decorators.ConsecutiveFailures),
		CreatedAt:            binding.CreatedAt,
		DriverId:             binding.DriverID,
		DriverVersionId:      binding.DriverVersionID,
		Enabled:              binding.Enabled,
		EventTypePatterns:    optionalAgentRecordStrings(binding.EventTypePatterns),
		FilterRef:            optionalAgentRecordString(binding.FilterRef),
		IdempotencyPolicy:    optionalAgentRecordString(binding.IdempotencyPolicy),
		LastRunStatus:        optionalAgentRecordString(decorators.LastRunStatus),
		Method:               optionalAgentRecordString(binding.Method),
		Name:                 binding.Name,
		NextFireAt:           decorators.NextFireAt,
		PathTemplate:         optionalAgentRecordString(binding.PathTemplate),
		Permissions:          optionalAgentRecordStrings(binding.Permissions),
		RetryBackoffSeconds:  optionalAgentRecordInt(binding.RetryBackoffSeconds),
		RetryMaxAttempts:     optionalAgentRecordInt(binding.RetryMaxAttempts),
		RouteKey:             optionalAgentRecordString(binding.RouteKey),
		Schedule:             optionalAgentRecordString(binding.Schedule),
		ScheduleTimezone:     optionalAgentRecordString(binding.ScheduleTimezone),
		SourceConfigRef:      optionalAgentRecordString(binding.SourceConfigRef),
		SourceKind:           binding.SourceKind,
		SourceRef:            optionalAgentRecordString(binding.SourceRef),
		SubjectKeyTemplate:   optionalAgentRecordString(binding.SubjectKeyTemplate),
		TargetAgentServiceId: optionalAgentRecordString(binding.TargetAgentServiceID),
		TargetEntrypoint:     optionalAgentRecordString(binding.TargetEntrypoint),
		Topic:                optionalAgentRecordString(binding.Topic),
		UpdatedAt:            binding.UpdatedAt,
		WorkspaceKey:         binding.WorkspaceKey,
	}
}

func agentRecordActorFilter(filter *automation.ActorFilter) *loomapi.TriggerActorFilter {
	if filter == nil || filter.IsZero() {
		return nil
	}
	return &loomapi.TriggerActorFilter{
		AllowActors:       optionalAgentRecordStrings(filter.AllowActors),
		ExcludeActorKinds: optionalAgentRecordStrings(filter.ExcludeActorKinds),
	}
}

func optionalAgentRecordString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalAgentRecordInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func optionalAgentRecordStrings(value []string) *[]string {
	if len(value) == 0 {
		return nil
	}
	copyOfValue := append([]string(nil), value...)
	return &copyOfValue
}

func optionalAgentRecordBindings(value []loomapi.AgentRecordBinding) *[]loomapi.AgentRecordBinding {
	if len(value) == 0 {
		return nil
	}
	copyOfValue := append([]loomapi.AgentRecordBinding(nil), value...)
	return &copyOfValue
}

func optionalAgentRecordMap(value map[string]string) *map[string]string {
	if len(value) == 0 {
		return nil
	}
	return &value
}

func optionalAgentRecordStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalAgentRecordStringsValue(value *[]string) []string {
	if value == nil {
		return nil
	}
	return append([]string(nil), (*value)...)
}

func optionalAgentRecordBoolValue(value *bool) bool {
	return value != nil && *value
}

func stringValueFromInteractiveKind(value *loomapi.CreateInteractiveAgentRequestKind) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func aggregateBindingDecorators(decorators []triggerbindings.BindingDecorators) (string, int, *time.Time) {
	lastStatus := ""
	lastRank := -1
	failures := 0
	var next *time.Time
	for _, dec := range decorators {
		if dec.ConsecutiveFailures > failures {
			failures = dec.ConsecutiveFailures
		}
		if dec.LastRunStatus != "" {
			if rank := runStatusRank(dec.LastRunStatus); rank > lastRank {
				lastRank = rank
				lastStatus = dec.LastRunStatus
			}
		}
		if dec.NextFireAt != nil && (next == nil || dec.NextFireAt.Before(*next)) {
			t := *dec.NextFireAt
			next = &t
		}
	}
	return lastStatus, failures, next
}

func runStatusRank(status string) int {
	switch execution.DriverRunStatus(status) {
	case execution.DriverRunFailed:
		return 50
	case execution.DriverRunNeedsReview, execution.DriverRunCancelled:
		return 40
	case execution.DriverRunRunning, execution.DriverRunQueued, execution.DriverRunSuspendedAwait:
		return 30
	case execution.DriverRunCompleted:
		return 10
	default:
		return 0
	}
}

func deriveAgentRecordKind(record *agentsmodule.AgentServiceRecord) string {
	if record != nil && record.Metadata[agentsmodule.MetadataRoleKind] == string(agentsmodule.RoleKindInteractive) {
		return agentRecordKindInteractive
	}
	if strings.TrimSpace(record.RoleName) != "" {
		return agentRecordKindPrompt
	}
	return agentRecordKindScripted
}

func isAgentRecordArchived(record *agentsmodule.AgentServiceRecord) bool {
	if record == nil {
		return false
	}
	// deleted_at is the archive signal (Wave B); the metadata marker survives
	// only for records archived before the switch.
	return record.DeletedAt != nil || strings.TrimSpace(record.Metadata[agentArchiveMetadataKey]) != ""
}

func agentServiceKindForSource(sourceKind string) agentsmodule.AgentKind {
	if sourceKind == promptAgentSourceCron {
		return agentsmodule.AgentKindCron
	}
	return agentsmodule.AgentKindEvent
}

func mintAgentRecordID(name string) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return "agt-" + slugForAgentID(name) + "-" + hex.EncodeToString(suffix), nil
}

func slugForAgentID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "agent"
	}
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	return slug
}

func promptAgentSourceConfigRef(roleName, backend string) (string, error) {
	runInput := map[string]string{"roleName": roleName}
	if backend = strings.TrimSpace(backend); backend != "" {
		runInput["backend"] = backend
	}
	data, err := json.Marshal(runInput)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Module) setAttachedBindingsEnabled(
	ctx context.Context,
	auth authority.OperatorAuthority,
	ws, agentID string,
	enabled bool,
) error {
	if m.bindings == nil {
		return automation.ErrUnavailable
	}
	bindings, err := m.bindings.ListBindings(ctx, ws, automation.BindingFilter{TargetAgentServiceID: agentID})
	if err != nil {
		return err
	}
	for _, b := range bindings {
		if b == nil {
			continue
		}
		command := automation.ManagedBindingCommand{WorkspaceKey: ws, BindingID: b.BindingID, AgentServiceID: agentID}
		var err error
		if enabled {
			_, err = m.bindings.EnableManagedBinding(ctx, auth, command)
		} else {
			_, err = m.bindings.DisableManagedBinding(ctx, auth, command)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func agentRouteValue(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.PathValue(name)); value != "" {
			return value
		}
	}
	return ""
}
