package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
)

const (
	agentRecordKindPrompt     = "prompt"
	agentRecordKindScripted   = "scripted"
	agentRecordKindBinding    = "binding"
	agentRecordKindSupervised = "supervised"
	agentArchiveMetadataKey   = "archived_at"
)

type supervisedAgentDTO struct {
	*domain.Agent
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Repos      []string `json:"repos"`
	RepoGroups []string `json:"repo_groups"`
	CrossRepo  bool     `json:"cross_repo"`
}

// newSupervisedAgentDTO keeps the unified agent response compatible with the
// workspace contract. Repos and repo_groups are required collection fields in
// the frontend model; domain.Agent omits nil slices, which made a freshly
// created cross-repo or no-group agent crash consumers that iterate them before
// the next workspace refresh.
func newSupervisedAgentDTO(agent *domain.Agent) supervisedAgentDTO {
	if agent == nil {
		return supervisedAgentDTO{
			Kind:       agentRecordKindSupervised,
			Repos:      []string{},
			RepoGroups: []string{},
			CrossRepo:  false,
		}
	}
	clone := *agent
	return supervisedAgentDTO{
		Agent:      &clone,
		ID:         clone.Name,
		Kind:       agentRecordKindSupervised,
		Repos:      append([]string{}, clone.Repos...),
		RepoGroups: append([]string{}, clone.RepoGroups...),
		CrossRepo:  clone.CrossRepo,
	}
}

type agentRecordDTO struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	Kind                string             `json:"kind"`
	Enabled             bool               `json:"enabled"`
	Behavior            agentBehaviorDTO   `json:"behavior"`
	BudgetPolicy        string             `json:"budget_policy,omitempty"`
	WorkspaceKey        string             `json:"workspace_key"`
	Bindings            []recordBindingDTO `json:"bindings,omitempty"`
	LastRunStatus       string             `json:"last_run_status,omitempty"`
	ConsecutiveFailures int                `json:"consecutive_failures,omitempty"`
	NextFireAt          *time.Time         `json:"next_fire_at,omitempty"`
	Metadata            map[string]string  `json:"metadata,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

type agentBehaviorDTO struct {
	RoleName        string `json:"role_name,omitempty"`
	DriverID        string `json:"driver_id,omitempty"`
	DriverVersionID string `json:"driver_version_id,omitempty"`
}

type recordBindingDTO struct {
	*domain.TriggerBinding
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastRunStatus       string     `json:"last_run_status,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
}

type legacyBindingAgentDTO struct {
	*domain.TriggerBinding
	ID                  string     `json:"id"`
	Kind                string     `json:"kind"`
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastRunStatus       string     `json:"last_run_status,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
}

type createAgentKindProbe struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type createPromptAgentRequest struct {
	Kind         string                    `json:"kind"`
	Name         string                    `json:"name"`
	Backend      string                    `json:"backend,omitempty"`
	Behavior     promptAgentBehaviorCreate `json:"behavior"`
	Trigger      promptAgentTriggerRequest `json:"trigger,omitempty"`
	Grants       []promptAgentGrantRequest `json:"grants,omitempty"`
	Enabled      *bool                     `json:"enabled,omitempty"`
	BudgetPolicy string                    `json:"budget_policy,omitempty"`
}

type promptAgentBehaviorCreate struct {
	RoleName   string                 `json:"role_name"`
	RoleCreate *promptRoleCreateInput `json:"role_create,omitempty"`
}

type promptRoleCreateInput struct {
	Prompt         string   `json:"prompt,omitempty"`
	PromptFilename string   `json:"prompt_filename,omitempty"`
	Description    string   `json:"description,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Model          string   `json:"model,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	Skills         []string `json:"skills,omitempty"`
}

type promptAgentTriggerRequest struct {
	SourceKind        string   `json:"source_kind,omitempty"`
	RouteKey          string   `json:"route_key,omitempty"`
	BindingID         string   `json:"binding_id,omitempty"`
	EventTypePatterns []string `json:"event_type_patterns,omitempty"`
	Schedule          string   `json:"schedule,omitempty"`
	ScheduleTimezone  string   `json:"schedule_timezone,omitempty"`
	Entrypoint        string   `json:"entrypoint,omitempty"`
}

type promptAgentGrantRequest struct {
	ConnectorID     string `json:"connector_id"`
	Action          string `json:"action"`
	ResourcePattern string `json:"resource_pattern"`
	GrantID         string `json:"grant_id,omitempty"`
}

type patchAgentRecordRequest struct {
	Name         *string                   `json:"name,omitempty"`
	Behavior     *patchAgentBehaviorRecord `json:"behavior,omitempty"`
	BudgetPolicy *string                   `json:"budget_policy,omitempty"`
}

type patchAgentBehaviorRecord struct {
	RoleName *string `json:"role_name,omitempty"`
}

func (m *Module) agentRecordDTO(ctx context.Context, ws string, record *domain.AgentService, now time.Time) (agentRecordDTO, error) {
	if m.bindings == nil {
		return newAgentRecordDTO(record), automation.ErrUnavailable
	}
	bindings, err := m.bindings.ListBindings(ctx, ws, automation.BindingFilter{TargetAgentServiceID: record.ServiceID})
	if err != nil {
		return newAgentRecordDTO(record), err
	}
	return m.agentRecordDTOWithBindings(ctx, ws, record, bindings, now), nil
}

func newAgentRecordDTO(record *domain.AgentService) agentRecordDTO {
	return agentRecordDTO{
		ID:      record.ServiceID,
		Name:    record.Name,
		Kind:    deriveAgentRecordKind(record),
		Enabled: record.DesiredState == domain.AgentServiceDesiredRunning,
		Behavior: agentBehaviorDTO{
			RoleName:        record.RoleName,
			DriverID:        record.DriverID,
			DriverVersionID: record.DriverVersionID,
		},
		BudgetPolicy: record.BudgetPolicy,
		WorkspaceKey: record.WorkspaceKey,
		Metadata:     cloneStringMap(record.Metadata),
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
	}
}

func (m *Module) agentRecordDTOWithBindings(
	ctx context.Context,
	ws string,
	record *domain.AgentService,
	bindings []*automation.Binding,
	now time.Time,
) agentRecordDTO {
	out := newAgentRecordDTO(record)
	decorators := make([]triggerbindings.BindingDecorators, 0, len(bindings))
	out.Bindings = make([]recordBindingDTO, 0, len(bindings))
	for _, b := range bindings {
		if b == nil {
			continue
		}
		dec := triggerbindings.DecorateBinding(ctx, m.store, ws, b, now)
		decorators = append(decorators, dec)
		out.Bindings = append(out.Bindings, recordBindingDTO{
			TriggerBinding:      b,
			NextFireAt:          dec.NextFireAt,
			LastRunStatus:       dec.LastRunStatus,
			ConsecutiveFailures: dec.ConsecutiveFailures,
		})
	}
	out.LastRunStatus, out.ConsecutiveFailures, out.NextFireAt = aggregateBindingDecorators(decorators)
	return out
}

func legacyBindingDTO(ctx context.Context, st store.Store, ws string, b *domain.TriggerBinding, now time.Time) legacyBindingAgentDTO {
	dec := triggerbindings.DecorateBinding(ctx, st, ws, b, now)
	return legacyBindingAgentDTO{
		TriggerBinding:      b,
		ID:                  b.BindingID,
		Kind:                agentRecordKindBinding,
		NextFireAt:          dec.NextFireAt,
		LastRunStatus:       dec.LastRunStatus,
		ConsecutiveFailures: dec.ConsecutiveFailures,
	}
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
	switch domain.DriverRunStatus(status) {
	case domain.DriverRunFailed:
		return 50
	case domain.DriverRunNeedsReview, domain.DriverRunCancelled:
		return 40
	case domain.DriverRunRunning, domain.DriverRunQueued, domain.DriverRunSuspendedAwaitingEvent:
		return 30
	case domain.DriverRunCompleted:
		return 10
	default:
		return 0
	}
}

func deriveAgentRecordKind(record *domain.AgentService) string {
	if strings.TrimSpace(record.RoleName) != "" {
		return agentRecordKindPrompt
	}
	return agentRecordKindScripted
}

func isAgentRecordArchived(record *domain.AgentService) bool {
	if record == nil {
		return false
	}
	// deleted_at is the archive signal (Wave B); the metadata marker survives
	// only for records archived before the switch.
	return record.DeletedAt != nil || strings.TrimSpace(record.Metadata[agentArchiveMetadataKey]) != ""
}

func agentServiceKindForSource(sourceKind string) domain.AgentServiceKind {
	if sourceKind == store.CronSourceKind {
		return domain.AgentServiceKindCron
	}
	return domain.AgentServiceKindEvent
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

func (m *Module) updateAttachedBindingRole(
	ctx context.Context,
	auth authority.OperatorAuthority,
	ws, agentID, roleName string,
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
		ref := sourceConfigRefWithRole(b.SourceConfigRef, roleName)
		if _, err := m.bindings.UpdateManagedBinding(ctx, auth, automation.UpdateManagedBindingCommand{
			WorkspaceKey: ws, BindingID: b.BindingID, AgentServiceID: agentID,
			Patch: automation.BindingPatch{SourceConfigRef: &ref},
		}); err != nil {
			return err
		}
	}
	return nil
}

func sourceConfigRefWithRole(ref, roleName string) string {
	obj := map[string]json.RawMessage{}
	if strings.TrimSpace(ref) != "" {
		_ = json.Unmarshal([]byte(ref), &obj)
	}
	raw, _ := json.Marshal(roleName)
	obj["roleName"] = raw
	data, err := json.Marshal(obj)
	if err != nil {
		return `{"roleName":` + strconvQuote(roleName) + `}`
	}
	return string(data)
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

func strconvQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}
