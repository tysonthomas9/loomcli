package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MetadataInheritsFrom      = "loom.inherits_from"
	MetadataIntegrationInputs = "loom.integration_inputs"
)

// TaskLineageSpec describes Git inputs independently of scheduling edges.
type TaskLineageSpec struct {
	InheritsFrom      string   `json:"inherits_from,omitempty"`
	IntegrationInputs []string `json:"integration_inputs,omitempty"`
}

func ParseTaskLineage(metadata map[string]string) (TaskLineageSpec, error) {
	spec := TaskLineageSpec{InheritsFrom: strings.TrimSpace(metadata[MetadataInheritsFrom])}
	if raw := strings.TrimSpace(metadata[MetadataIntegrationInputs]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &spec.IntegrationInputs); err != nil {
			return TaskLineageSpec{}, fmt.Errorf("decode %s: %w", MetadataIntegrationInputs, err)
		}
	}
	if err := spec.Validate(""); err != nil {
		return TaskLineageSpec{}, err
	}
	return spec, nil
}

func (s TaskLineageSpec) Validate(taskID string) error {
	base := strings.TrimSpace(s.InheritsFrom)
	seen := map[string]struct{}{}
	for _, raw := range s.IntegrationInputs {
		id := strings.TrimSpace(raw)
		if id == "" {
			return fmt.Errorf("integration input task ID is empty")
		}
		if id == taskID || id == base {
			return fmt.Errorf("integration input %q duplicates the task or its base", id)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("integration input %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	if base == taskID && taskID != "" {
		return fmt.Errorf("task %q cannot inherit from itself", taskID)
	}
	if len(s.IntegrationInputs) > 0 && base == "" {
		return fmt.Errorf("integration inputs require inherits_from")
	}
	return nil
}

func (s TaskLineageSpec) Metadata() (map[string]string, error) {
	if err := s.Validate(""); err != nil {
		return nil, err
	}
	metadata := map[string]string{}
	if base := strings.TrimSpace(s.InheritsFrom); base != "" {
		metadata[MetadataInheritsFrom] = base
	}
	if len(s.IntegrationInputs) > 0 {
		encoded, err := json.Marshal(s.IntegrationInputs)
		if err != nil {
			return nil, err
		}
		metadata[MetadataIntegrationInputs] = string(encoded)
	}
	return metadata, nil
}

func (s TaskLineageSpec) SchedulingDependencies(explicit []string) []string {
	out := make([]string, 0, len(explicit)+1+len(s.IntegrationInputs))
	seen := map[string]struct{}{}
	add := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range explicit {
		add(id)
	}
	add(s.InheritsFrom)
	for _, id := range s.IntegrationInputs {
		add(id)
	}
	return out
}

// ValidateTaskLineageInputs verifies authoring-time references before the
// issue is created. The supported create paths separately normalize every
// code input into the new task's direct blocks dependencies.
func ValidateTaskLineageInputs(
	ctx context.Context,
	spec TaskLineageSpec,
	sourceRepo string,
	get func(context.Context, string) (*IssueDetailData, error),
) error {
	for _, inputID := range append([]string{spec.InheritsFrom}, spec.IntegrationInputs...) {
		if inputID == "" {
			continue
		}
		input, err := get(ctx, inputID)
		if err != nil {
			return fmt.Errorf("read code input %q: %w", inputID, err)
		}
		if input == nil {
			return fmt.Errorf("code input %q does not exist", inputID)
		}
		if sourceRepo != input.SourceRepo {
			return fmt.Errorf("code input %q belongs to source repo %q, task belongs to %q", inputID, input.SourceRepo, sourceRepo)
		}
	}
	return nil
}
