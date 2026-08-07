package trigger

import (
	"errors"
	"fmt"
	"strings"
)

// Subject key rendering (locked Phase B decision), kept in lockstep with
// fleet-db's internal/routing/subject_key.go: templates substitute
// {{subject_ref}}, {{event_type}} and {{attrs.<name>}} only — they never read
// the raw event payload. Adapters own enriching payload data into Attrs. When
// a binding has no template the default key is "<binding_id>|<subject_ref>",
// the implicit per-binding subject scope.
var (
	// ErrInvalidSubjectTemplate is the sentinel wrapped by template parse
	// failures: unterminated or unknown tokens.
	ErrInvalidSubjectTemplate = errors.New("trigger: invalid subject key template")
	// ErrMissingSubjectAttr is wrapped when a template references an
	// attrs.<name> token that the event's enriched attrs do not carry. An
	// attr present with an empty value renders as empty, only an absent key
	// is an error.
	ErrMissingSubjectAttr = errors.New("trigger: missing subject attr")
	// ErrEmptySubjectKey is wrapped when a template renders to an
	// empty/blank key, which would collapse unrelated deliveries into one
	// concurrency subject.
	ErrEmptySubjectKey = errors.New("trigger: subject key rendered empty")
)

// SubjectInputs carries the per-event values a subject key template may
// substitute. WorkspaceKey and ActorRef ride along for adapters; the locked
// template grammar does not expose them as tokens.
type SubjectInputs struct {
	WorkspaceKey string
	BindingID    string
	EventType    string
	SubjectRef   string
	ActorRef     string
	// Attrs is the adapter-enriched subject attribute map ({{attrs.<name>}}
	// tokens). Templates never read the raw payload.
	Attrs map[string]string
}

// RenderSubjectKey renders a binding's subject key template against one
// event's inputs. An empty template selects the default key
// "<binding_id>|<subject_ref>"; when the event also has no subject_ref the
// default is the empty string, meaning the delivery has no concurrency
// subject. Errors wrap ErrInvalidSubjectTemplate, ErrMissingSubjectAttr or
// ErrEmptySubjectKey.
func RenderSubjectKey(template string, in SubjectInputs) (string, error) {
	if strings.TrimSpace(template) == "" {
		return defaultSubjectKey(in), nil
	}
	var rendered strings.Builder
	rest := template
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			rendered.WriteString(rest)
			break
		}
		rendered.WriteString(rest[:start])
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			return "", fmt.Errorf("%w: unterminated token near %q", ErrInvalidSubjectTemplate, rest[start:])
		}
		token := strings.TrimSpace(rest[start+2 : start+end])
		value, err := resolveSubjectToken(token, in)
		if err != nil {
			return "", err
		}
		rendered.WriteString(value)
		rest = rest[start+end+2:]
	}
	key := rendered.String()
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w: template %q", ErrEmptySubjectKey, template)
	}
	return key, nil
}

// resolveSubjectToken maps one {{token}} to its event value. The allowed
// token set mirrors fleet-db's models.ValidateSubjectKeyTemplate — keep in
// sync.
func resolveSubjectToken(token string, in SubjectInputs) (string, error) {
	switch token {
	case "subject_ref":
		return in.SubjectRef, nil
	case "event_type":
		return in.EventType, nil
	}
	if name := strings.TrimPrefix(token, "attrs."); name != token && strings.TrimSpace(name) != "" {
		value, ok := in.Attrs[name]
		if !ok {
			return "", fmt.Errorf("%w: attrs.%s", ErrMissingSubjectAttr, name)
		}
		return value, nil
	}
	return "", fmt.Errorf("%w: unknown token %q (allowed tokens are subject_ref, event_type, attrs.<name>)", ErrInvalidSubjectTemplate, token)
}

func defaultSubjectKey(in SubjectInputs) string {
	subject := strings.TrimSpace(in.SubjectRef)
	if subject == "" {
		return ""
	}
	return in.BindingID + "|" + subject
}
