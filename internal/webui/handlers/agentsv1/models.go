package agentsv1

import (
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// ModelCatalog is GET /harnesses/{harness}/models: the harness's connected
// providers and their models, with capabilities in T3 Code's generic
// option-descriptor shape.
type ModelCatalog struct {
	Harness   string          `json:"harness"`
	Providers []ModelProvider `json:"providers"`
}

// ModelProvider is one connected provider and its models.
type ModelProvider struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Models []Model `json:"models"`
}

// Model is one model: the id PATCH /agents/{id} takes as model, its context
// limit in tokens (0 when unknown), input types (text, image, pdf), whether
// it is the harness default, and the options it takes.
type Model struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	ContextLimit      int64              `json:"context_limit"`
	Input             []string           `json:"input"`
	IsDefault         bool               `json:"is_default"`
	OptionDescriptors []OptionDescriptor `json:"option_descriptors"`
	Source            string             `json:"source"` // harness, or custom: a workspace custom model id (MCS3)
}

// CustomModels is GET and PUT /harnesses/{harness}/custom: the model ids this
// workspace adds to the harness's catalog (MCS3). A PUT replaces them.
type CustomModels struct {
	Harness string   `json:"harness"`
	Models  []string `json:"models"`
}

// OptionDescriptor is one option a model takes: a select with its options,
// or a boolean. current_value is the value used when none is set.
type OptionDescriptor struct {
	ID           string         `json:"id"`
	Label        string         `json:"label"`
	Description  string         `json:"description,omitempty"`
	Type         string         `json:"type"`
	Options      []OptionChoice `json:"options,omitempty"`
	CurrentValue any            `json:"current_value,omitempty"`
}

// OptionChoice is one value of a select option.
type OptionChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	IsDefault   bool   `json:"is_default,omitempty"`
}

// OptionValue is one chosen option: a string, or a boolean for a boolean option.
type OptionValue struct {
	ID    string `json:"id"`
	Value any    `json:"value"`
}

func (h *Handler) listModels(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	harness := r.PathValue("harness")
	ms, err := s.Models(r.Context(), harness)
	if err != nil {
		return 0, nil, err
	}
	out := ModelCatalog{Harness: harness, Providers: []ModelProvider{}}
	at := map[string]int{}
	for _, m := range ms {
		i, ok := at[m.Provider]
		if !ok {
			i, at[m.Provider] = len(out.Providers), len(out.Providers)
			out.Providers = append(out.Providers, ModelProvider{ID: m.Provider, Name: m.ProviderName, Models: []Model{}})
		}
		out.Providers[i].Models = append(out.Providers[i].Models, modelOut(m))
	}
	return http.StatusOK, out, nil
}

func getCustomModels(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	harness := r.PathValue("harness")
	ids, err := s.CustomModels(r.Context(), harness)
	return http.StatusOK, CustomModels{Harness: harness, Models: ids}, err
}

func putCustomModels(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	var body CustomModels
	if _, err := envelope(w, r, &body); err != nil {
		return 0, nil, err
	}
	harness := r.PathValue("harness")
	ids, err := s.SetCustomModels(r.Context(), harness, body.Models)
	return http.StatusOK, CustomModels{Harness: harness, Models: ids}, err
}

// LimitResume is GET and PUT /settings/limit-resume: whether this workspace
// auto-resumes agents after a usage limit (OR7). A PUT sets it.
type LimitResume struct {
	Enabled bool `json:"enabled"`
}

func getLimitResume(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	on, err := s.LimitResume(r.Context())
	return http.StatusOK, LimitResume{Enabled: on}, err
}

func putLimitResume(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	var body LimitResume
	if _, err := envelope(w, r, &body); err != nil {
		return 0, nil, err
	}
	return http.StatusOK, body, s.SetLimitResume(r.Context(), body.Enabled)
}

func modelOut(m loomharness.Model) Model {
	out := Model{ID: m.ID, Name: m.Name, ContextLimit: m.ContextLimit, Input: m.Input, IsDefault: m.Default,
		OptionDescriptors: []OptionDescriptor{}, Source: "harness"}
	if m.Custom {
		out.Source = "custom"
	}
	if out.Input == nil {
		out.Input = []string{}
	}
	for _, d := range m.Options {
		od := OptionDescriptor{ID: d.ID, Label: d.Label, Description: d.Description, Type: d.Type}
		for _, c := range d.Choices {
			od.Options = append(od.Options, OptionChoice{c.ID, c.Label, c.Description, c.Default})
		}
		switch {
		case d.Current == "":
		case d.Type == loomharness.OptionBoolean:
			od.CurrentValue = d.Current == "true"
		default:
			od.CurrentValue = d.Current
		}
		out.OptionDescriptors = append(out.OptionDescriptors, od)
	}
	return out
}

// options converts a PATCH's options; a value is a string or a boolean.
func options(in []OptionValue) ([]loomharness.Option, error) {
	var out []loomharness.Option
	for _, o := range in {
		switch v := o.Value.(type) {
		case string:
			out = append(out, loomharness.Option{ID: o.ID, Value: v})
		case bool:
			out = append(out, loomharness.Option{ID: o.ID, Value: strconv.FormatBool(v)})
		default:
			return nil, service.ErrValidation("option " + o.ID + ": value must be a string or a boolean")
		}
	}
	return out, nil
}
