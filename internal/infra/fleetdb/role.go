package fleetdb

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type roleStore struct{ client *Client }

var _ store.RoleStore = (*roleStore)(nil)

// roleWire mirrors fleet-db's models.Role JSON shape.
//
// input_policy carries domain.RoleInputPolicy directly rather than a local
// mirror struct: its JSON tags already match fleet-db's models.RoleInputPolicy
// field for field, and a second copy of a type whose whole point is a
// deny-by-default rule is a second place for that rule to be got wrong.
type roleWire struct {
	WorkspaceKey   string                  `json:"workspace_key"`
	Name           string                  `json:"name"`
	Kind           string                  `json:"kind,omitempty"`
	Description    string                  `json:"description,omitempty"`
	Prompt         string                  `json:"prompt,omitempty"`
	PromptFile     string                  `json:"prompt_file,omitempty"`
	Model          string                  `json:"model,omitempty"`
	TaskFilter     string                  `json:"task_filter,omitempty"`
	Backend        string                  `json:"backend,omitempty"`
	Effort         string                  `json:"effort,omitempty"`
	PathPatterns   []string                `json:"path_patterns,omitempty"`
	Skills         []string                `json:"skills,omitempty"`
	Labels         []string                `json:"labels,omitempty"`
	ExcludeLabels  []string                `json:"exclude_labels,omitempty"`
	InputPolicy    *domain.RoleInputPolicy `json:"input_policy,omitempty"`
	MaxPriority    *int                    `json:"max_priority,omitempty"`
	MaxConcurrency *int                    `json:"max_concurrency,omitempty"`
	ReadOnly       bool                    `json:"read_only,omitempty"`
	AllowedTools   []string                `json:"allowed_tools,omitempty"`
	DeniedTools    []string                `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64                `json:"max_budget_usd,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

func (r roleWire) toDomain() *domain.Role {
	return &domain.Role{
		WorkspaceKey:   r.WorkspaceKey,
		Name:           r.Name,
		Kind:           domain.RoleKind(r.Kind),
		Description:    r.Description,
		Prompt:         r.Prompt,
		PromptFile:     r.PromptFile,
		Model:          r.Model,
		TaskFilter:     r.TaskFilter,
		Backend:        r.Backend,
		Effort:         r.Effort,
		PathPatterns:   r.PathPatterns,
		Skills:         r.Skills,
		Labels:         r.Labels,
		ExcludeLabels:  r.ExcludeLabels,
		InputPolicy:    r.InputPolicy.Clone(),
		MaxPriority:    r.MaxPriority,
		MaxConcurrency: r.MaxConcurrency,
		ReadOnly:       r.ReadOnly,
		AllowedTools:   r.AllowedTools,
		DeniedTools:    r.DeniedTools,
		MaxBudgetUSD:   r.MaxBudgetUSD,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func (s *roleStore) Create(ctx context.Context, in store.RoleCreate) (*domain.Role, error) {
	body := struct {
		Name           string                  `json:"name"`
		Kind           string                  `json:"kind,omitempty"`
		Description    string                  `json:"description,omitempty"`
		Prompt         string                  `json:"prompt,omitempty"`
		PromptFile     string                  `json:"prompt_file,omitempty"`
		Model          string                  `json:"model,omitempty"`
		TaskFilter     string                  `json:"task_filter,omitempty"`
		Backend        string                  `json:"backend,omitempty"`
		Effort         string                  `json:"effort,omitempty"`
		PathPatterns   []string                `json:"path_patterns,omitempty"`
		Skills         []string                `json:"skills,omitempty"`
		Labels         []string                `json:"labels,omitempty"`
		ExcludeLabels  []string                `json:"exclude_labels,omitempty"`
		InputPolicy    *domain.RoleInputPolicy `json:"input_policy,omitempty"`
		MaxPriority    *int                    `json:"max_priority,omitempty"`
		MaxConcurrency *int                    `json:"max_concurrency,omitempty"`
		ReadOnly       bool                    `json:"read_only,omitempty"`
		AllowedTools   []string                `json:"allowed_tools,omitempty"`
		DeniedTools    []string                `json:"denied_tools,omitempty"`
		MaxBudgetUSD   *float64                `json:"max_budget_usd,omitempty"`
	}{
		Name:           in.Name,
		Kind:           in.Kind,
		Description:    in.Description,
		Prompt:         in.Prompt,
		PromptFile:     in.PromptFile,
		Model:          in.Model,
		TaskFilter:     in.TaskFilter,
		Backend:        in.Backend,
		Effort:         in.Effort,
		PathPatterns:   in.PathPatterns,
		Skills:         in.Skills,
		Labels:         in.Labels,
		ExcludeLabels:  in.ExcludeLabels,
		InputPolicy:    in.InputPolicy,
		MaxPriority:    in.MaxPriority,
		MaxConcurrency: in.MaxConcurrency,
		ReadOnly:       in.ReadOnly,
		AllowedTools:   in.AllowedTools,
		DeniedTools:    in.DeniedTools,
		MaxBudgetUSD:   in.MaxBudgetUSD,
	}
	var resp roleWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/roles", body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *roleStore) Get(ctx context.Context, ws, name string) (*domain.Role, error) {
	var resp roleWire
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/roles/"+pathEscape(name), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *roleStore) List(ctx context.Context, ws string) ([]*domain.Role, error) {
	var resp struct {
		Roles []roleWire `json:"roles"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/roles", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.Role, 0, len(resp.Roles))
	for _, r := range resp.Roles {
		out = append(out, r.toDomain())
	}
	return out, nil
}

//nolint:funlen // Patch serialization mirrors the store.RoleUpdate surface area.
func (s *roleStore) Update(ctx context.Context, ws, name string, patch store.RoleUpdate) (*domain.Role, error) {
	// Fleet-db's PATCH role contract uses single-pointer fields plus
	// explicit Clear* booleans for the *int / *float64 optional fields.
	// Translate the store's RoleUpdate (where **int = &nil signals
	// "clear", **int = &&value signals "set", and nil signals "leave
	// alone") into that wire shape.
	body := struct {
		Description       *string                 `json:"description,omitempty"`
		Kind              *string                 `json:"kind,omitempty"`
		Prompt            *string                 `json:"prompt,omitempty"`
		PromptFile        *string                 `json:"prompt_file,omitempty"`
		Model             *string                 `json:"model,omitempty"`
		TaskFilter        *string                 `json:"task_filter,omitempty"`
		Backend           *string                 `json:"backend,omitempty"`
		Effort            *string                 `json:"effort,omitempty"`
		PathPatterns      *[]string               `json:"path_patterns,omitempty"`
		Skills            *[]string               `json:"skills,omitempty"`
		Labels            *[]string               `json:"labels,omitempty"`
		ExcludeLabels     *[]string               `json:"exclude_labels,omitempty"`
		InputPolicy       *domain.RoleInputPolicy `json:"input_policy,omitempty"`
		ClearInputPolicy  bool                    `json:"clear_input_policy,omitempty"`
		MaxPriority       *int                    `json:"max_priority,omitempty"`
		ClearMaxPriority  bool                    `json:"clear_max_priority,omitempty"`
		MaxConcurrency    *int                    `json:"max_concurrency,omitempty"`
		ClearConcurrency  bool                    `json:"clear_concurrency,omitempty"`
		ReadOnly          *bool                   `json:"read_only,omitempty"`
		AllowedTools      *[]string               `json:"allowed_tools,omitempty"`
		DeniedTools       *[]string               `json:"denied_tools,omitempty"`
		MaxBudgetUSD      *float64                `json:"max_budget_usd,omitempty"`
		ClearMaxBudgetUSD bool                    `json:"clear_max_budget_usd,omitempty"`
	}{
		Description:   patch.Description,
		Kind:          patch.Kind,
		Prompt:        patch.Prompt,
		PromptFile:    patch.PromptFile,
		Model:         patch.Model,
		TaskFilter:    patch.TaskFilter,
		Backend:       patch.Backend,
		Effort:        patch.Effort,
		PathPatterns:  patch.PathPatterns,
		Skills:        patch.Skills,
		Labels:        patch.Labels,
		ExcludeLabels: patch.ExcludeLabels,
		ReadOnly:      patch.ReadOnly,
		AllowedTools:  patch.AllowedTools,
		DeniedTools:   patch.DeniedTools,
	}
	// input_policy is `omitempty` on a pointer, so a &nil patch would serialize
	// to nothing at all and read on the server as "leave it alone" — silently
	// keeping a policy the operator asked to remove. The explicit
	// clear_input_policy flag is what makes "clear" distinguishable from
	// "absent", exactly as it is for the numeric caps below.
	if patch.InputPolicy != nil {
		if *patch.InputPolicy == nil {
			body.ClearInputPolicy = true
		} else {
			body.InputPolicy = *patch.InputPolicy
		}
	}
	if patch.MaxPriority != nil {
		if *patch.MaxPriority == nil {
			body.ClearMaxPriority = true
		} else {
			body.MaxPriority = *patch.MaxPriority
		}
	}
	if patch.MaxConcurrency != nil {
		if *patch.MaxConcurrency == nil {
			body.ClearConcurrency = true
		} else {
			body.MaxConcurrency = *patch.MaxConcurrency
		}
	}
	if patch.MaxBudgetUSD != nil {
		if *patch.MaxBudgetUSD == nil {
			body.ClearMaxBudgetUSD = true
		} else {
			body.MaxBudgetUSD = *patch.MaxBudgetUSD
		}
	}
	var resp roleWire
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/roles/"+pathEscape(name), body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *roleStore) Delete(ctx context.Context, ws, name string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/roles/"+pathEscape(name), nil, nil)
}
