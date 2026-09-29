package browsers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// ErrOperatorNotGranted is returned when no grant covers the requested
// workspace, subject, target and operation.
var ErrOperatorNotGranted = errors.New("no browser operator grant for this workspace and target")

// OperatorGrant gives remote user subjects browser operator access in exactly
// one canonical workspace. An empty Agents list covers every interactive
// target in that workspace; otherwise only the named agents are covered.
type OperatorGrant struct {
	Workspace string   `json:"workspace"`
	Subjects  []string `json:"subjects"`
	Agents    []string `json:"agents,omitempty"`
}

type grantKey struct{ workspace, subject string }

// grantTable maps (workspace, subject) to the agents it covers; a nil agent
// set covers every agent in the workspace.
type grantTable map[grantKey]map[string]struct{}

func (t grantTable) add(key grantKey, agents map[string]struct{}) {
	existing, ok := t[key]
	switch {
	case ok && existing == nil: // already covers the whole workspace
	case len(agents) == 0:
		t[key] = nil
	case existing == nil:
		t[key] = agents
	default:
		for a := range agents {
			existing[a] = struct{}{}
		}
	}
}

func (t grantTable) allows(key grantKey, target string) bool {
	agents, ok := t[key]
	if !ok {
		return false
	}
	if agents == nil {
		return true
	}
	_, ok = agents[target]
	return ok
}

// explicitSet trims values and rejects empty or wildcard entries.
func explicitSet(values []string, what string, index int) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || v == "*" {
			return nil, fmt.Errorf("browser operator grant %d: %s must be explicit", index, what)
		}
		out[v] = struct{}{}
	}
	return out, nil
}

func buildGrantTable(grants []OperatorGrant) (grantTable, error) {
	table := grantTable{}
	for i, g := range grants {
		ws := strings.TrimSpace(g.Workspace)
		if ws == "" || ws == "*" {
			return nil, fmt.Errorf("browser operator grant %d: workspace must name one canonical workspace", i)
		}
		if len(g.Subjects) == 0 {
			return nil, fmt.Errorf("browser operator grant %d: subjects is empty", i)
		}
		subjects, err := explicitSet(g.Subjects, "subjects", i)
		if err != nil {
			return nil, err
		}
		agents, err := explicitSet(g.Agents, "agent names", i)
		if err != nil {
			return nil, err
		}
		for s := range subjects {
			// Each subject gets its own copy so later merges stay independent.
			var own map[string]struct{}
			if len(agents) > 0 {
				own = make(map[string]struct{}, len(agents))
				for a := range agents {
					own[a] = struct{}{}
				}
			}
			table.add(grantKey{ws, s}, own)
		}
	}
	return table, nil
}

// ParseOperatorGrants parses a JSON array of OperatorGrant and returns a
// PermissionResolver that allows list/get/select only when a grant names the
// requested workspace and the caller's validated subject (and, if the grant
// lists agents, the target). Create is never granted to an operator. Empty
// input returns a nil resolver, so every remote request is denied. Malformed
// input returns an error; callers must fail closed. The count is the number of
// distinct (workspace, subject) grants.
func ParseOperatorGrants(raw string) (PermissionResolver, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, 0, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var grants []OperatorGrant
	if err := dec.Decode(&grants); err != nil {
		return nil, 0, fmt.Errorf("parse browser operator grants: %w", err)
	}
	// Exactly one JSON value: trailing values or bytes are malformed input.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, 0, errors.New("parse browser operator grants: unexpected data after grant array")
	}
	table, err := buildGrantTable(grants)
	if err != nil {
		return nil, 0, err
	}
	if len(table) == 0 {
		return nil, 0, nil
	}
	return func(_ context.Context, workspace string, identity middleware.UserIdentity, target, op string) error {
		switch op {
		case domain.BrowserOpList, domain.BrowserOpGet, domain.BrowserOpSelect:
		default:
			return ErrOperatorNotGranted
		}
		key := grantKey{strings.TrimSpace(workspace), strings.TrimSpace(identity.UserID)}
		if key.workspace == "" || key.subject == "" || !table.allows(key, strings.TrimSpace(target)) {
			return ErrOperatorNotGranted
		}
		return nil
	}, len(table), nil
}
