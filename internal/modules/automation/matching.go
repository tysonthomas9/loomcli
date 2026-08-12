package automation

import (
	"errors"
	"fmt"
	"strings"
)

var (
	errInvalidPattern         = errors.New("automation: invalid route-key pattern")
	errInvalidSubjectTemplate = errors.New("automation: invalid subject key template")
	errMissingSubjectAttr     = errors.New("automation: missing subject attr")
	errEmptySubjectKey        = errors.New("automation: subject key rendered empty")
)

type patternSegment struct {
	wildcard bool
	literal  string
	alts     []string
}

func (segment patternSegment) matches(value string) bool {
	if segment.wildcard {
		return true
	}
	if segment.alts != nil {
		for _, alternative := range segment.alts {
			if alternative == value {
				return true
			}
		}
		return false
	}
	return segment.literal == value
}

func validatePattern(pattern string) error {
	_, err := parsePattern(pattern)
	if err != nil {
		return fmt.Errorf("%w: %v: %w", ErrInvalid, err, errInvalidPattern)
	}
	return nil
}

func matchAny(patterns []string, routeKey string) bool {
	for _, pattern := range patterns {
		segments, err := parsePattern(pattern)
		if err != nil {
			continue
		}
		parts := strings.Split(routeKey, ".")
		if len(parts) != len(segments) {
			continue
		}
		matched := true
		for index, segment := range segments {
			if !segment.matches(parts[index]) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func parsePattern(pattern string) ([]patternSegment, error) {
	if pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	raw := strings.Split(pattern, ".")
	segments := make([]patternSegment, 0, len(raw))
	for index, value := range raw {
		segment, err := parsePatternSegment(value)
		if err != nil {
			return nil, fmt.Errorf("segment %d (%q): %w", index, value, err)
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func parsePatternSegment(value string) (patternSegment, error) {
	if value == "" {
		return patternSegment{}, errors.New("empty segment")
	}
	if value == "*" {
		return patternSegment{wildcard: true}, nil
	}
	if strings.HasPrefix(value, "{") || strings.HasSuffix(value, "}") {
		if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
			return patternSegment{}, errors.New("alternation braces must enclose the whole segment")
		}
		inner := value[1 : len(value)-1]
		if inner == "" {
			return patternSegment{}, errors.New("alternation has no alternatives")
		}
		alternatives := strings.Split(inner, ",")
		for _, alternative := range alternatives {
			if alternative == "" || strings.ContainsAny(alternative, "*{}") {
				return patternSegment{}, errors.New("alternation alternatives must be non-empty literal segments")
			}
		}
		return patternSegment{alts: alternatives}, nil
	}
	if strings.ContainsAny(value, "*{}") {
		return patternSegment{}, errors.New("wildcards and alternatives must occupy a whole segment")
	}
	return patternSegment{literal: value}, nil
}

type subjectInputs struct {
	bindingID  string
	eventType  string
	subjectRef string
	actorRef   string
	attrs      map[string]string
}

func validateSubjectTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return nil
	}
	rest := template
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			return nil
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			return fmt.Errorf("unterminated subject token: %w", ErrInvalid)
		}
		token := strings.TrimSpace(rest[start+2 : start+end])
		if token != "subject_ref" && token != "event_type" {
			name := strings.TrimPrefix(token, "attrs.")
			if name == token || strings.TrimSpace(name) == "" {
				return fmt.Errorf("unknown subject token %q: %w", token, ErrInvalid)
			}
		}
		rest = rest[start+end+2:]
	}
}

func renderSubjectKey(template string, in subjectInputs) (string, error) {
	if strings.TrimSpace(template) == "" {
		return defaultSubjectKey(in.bindingID, in.subjectRef), nil
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
			return "", errInvalidSubjectTemplate
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
		return "", errEmptySubjectKey
	}
	return key, nil
}

func resolveSubjectToken(token string, in subjectInputs) (string, error) {
	switch token {
	case "subject_ref":
		return in.subjectRef, nil
	case "event_type":
		return in.eventType, nil
	}
	if name := strings.TrimPrefix(token, "attrs."); name != token && strings.TrimSpace(name) != "" {
		value, ok := in.attrs[name]
		if !ok {
			return "", fmt.Errorf("%w: attrs.%s", errMissingSubjectAttr, name)
		}
		return value, nil
	}
	return "", errInvalidSubjectTemplate
}

func defaultSubjectKey(bindingID, subjectRef string) string {
	subjectRef = strings.TrimSpace(subjectRef)
	if subjectRef == "" {
		return ""
	}
	return bindingID + "|" + subjectRef
}

var internalEventVerbNormalization = map[string]string{
	"create": "created", "update": "updated", "delete": "deleted",
	"open": "opened", "close": "closed", "block": "blocked",
	"start": "started", "complete": "completed", "finish": "finished",
	"fail": "failed", "cancel": "cancelled", "claim": "claimed",
	"release": "released", "assign": "assigned",
}

func normalizeInternalEventType(raw string) (string, error) {
	eventType := strings.ToLower(strings.TrimSpace(raw))
	if eventType == "" || strings.ContainsAny(eventType, " \t\r\n") {
		return "", fmt.Errorf("internal event type %q must be a non-empty dotted identifier: %w", raw, ErrInvalid)
	}
	verb := eventType
	prefix := ""
	if index := strings.LastIndex(eventType, "."); index >= 0 {
		prefix, verb = eventType[:index+1], eventType[index+1:]
	}
	if normalized, ok := internalEventVerbNormalization[verb]; ok {
		return prefix + normalized, nil
	}
	return eventType, nil
}
