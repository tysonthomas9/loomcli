package fleet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
)

const recoveryManifest = "fleet.issue-workspace.v2"
const recoveryBodyLimit = 16 << 20
const recoveryOp = "ReadIssueRecovery"

var _ backend.IssueRecoveryBackend = (*FleetBackend)(nil)

func (b *FleetBackend) ReadIssueRecovery(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
	b.mu.RLock()
	auth := fleethttp.Auth{BearerToken: b.authToken, APIKey: b.apiKey, Actor: b.actor}
	b.mu.RUnlock()
	req, err := fleethttp.BuildJSONRequest(ctx, http.MethodPost, b.baseWorkspaceV2+"/issues/recovery-snapshot", auth, nil)
	if err != nil {
		return backend.IssueRecoverySnapshot{}, classifyTransportError(recoveryOp, err)
	}
	client := *b.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return backend.IssueRecoverySnapshot{}, classifyTransportError(recoveryOp, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return backend.IssueRecoverySnapshot{}, backend.ErrUnavailable(recoveryOp, fmt.Sprintf("recovery HTTP status %d", resp.StatusCode), nil)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return backend.IssueRecoverySnapshot{}, backend.ErrInternal(recoveryOp, "recovery response is not JSON", err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, recoveryBodyLimit+1))
	if err != nil {
		return backend.IssueRecoverySnapshot{}, classifyTransportError(recoveryOp, err)
	}
	if len(data) > recoveryBodyLimit {
		return backend.IssueRecoverySnapshot{}, backend.ErrInternal(recoveryOp, "recovery response exceeds size limit", nil)
	}
	result, err := validateRecoveryDocument(data, b.workspaceID)
	if err != nil {
		return backend.IssueRecoverySnapshot{}, backend.ErrInternal(recoveryOp, "invalid recovery manifest", err)
	}
	if err := ctx.Err(); err != nil {
		return backend.IssueRecoverySnapshot{}, classifyTransportError(recoveryOp, err)
	}
	return result, nil
}

type recoveryWire struct {
	Manifest  string             `json:"manifest"`
	Workspace string             `json:"workspace"`
	Through   string             `json:"through"`
	Issues    *[]json.RawMessage `json:"issues"`
	Total     *int64             `json:"total"`
	Ready     *[]json.RawMessage `json:"ready"`
	Blocked   *[]json.RawMessage `json:"blocked"`
	Deferred  *[]json.RawMessage `json:"deferred"`
}

func validateRecoveryDocument(data []byte, ws string) (backend.IssueRecoverySnapshot, error) {
	if err := validateRecoveryJSON(data); err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	wire, err := decodeRecoveryManifest(data, ws)
	if err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	issues := map[string]map[string]json.RawMessage{}
	for _, raw := range *wire.Issues {
		id, fields, err := validateRecoveryIssue(raw, ws)
		if err != nil {
			return backend.IssueRecoverySnapshot{}, err
		}
		if _, exists := issues[id]; exists {
			return backend.IssueRecoverySnapshot{}, fmt.Errorf("duplicate issue")
		}
		issues[id] = fields
	}
	for _, group := range [][]json.RawMessage{*wire.Ready, *wire.Deferred} {
		seen := map[string]bool{}
		for _, raw := range group {
			if err := validateRecoveryDerived(raw, ws, issues, seen); err != nil {
				return backend.IssueRecoverySnapshot{}, err
			}
		}
	}
	if err := validateRecoveryBlocked(*wire.Blocked, ws, issues); err != nil {
		return backend.IssueRecoverySnapshot{}, err
	}
	return backend.IssueRecoverySnapshot{Manifest: wire.Manifest, Workspace: wire.Workspace, Through: wire.Through, Document: append(json.RawMessage(nil), data...)}, nil
}
func recoveryString(fields map[string]json.RawMessage, key string, nonempty bool) (string, error) {
	var value *string
	if err := json.Unmarshal(fields[key], &value); err != nil || value == nil || (nonempty && *value == "") {
		return "", fmt.Errorf("invalid %s", key)
	}
	return *value, nil
}
func validateRecoveryIssue(raw json.RawMessage, ws string) (string, map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", nil, err
	}
	for _, key := range []string{"workspace", "id", "title", "status", "type", "created_by", "created_at", "updated_at"} {
		if _, err := recoveryString(fields, key, key != "created_by"); err != nil {
			return "", nil, err
		}
	}
	workspace, _ := recoveryString(fields, "workspace", true)
	id, _ := recoveryString(fields, "id", true)
	if workspace != ws {
		return "", nil, fmt.Errorf("foreign workspace")
	}
	var priority *int
	if err := json.Unmarshal(fields["priority"], &priority); err != nil || priority == nil || *priority < 0 || *priority > 4 {
		return "", nil, fmt.Errorf("invalid priority")
	}
	for _, key := range []string{"created_at", "updated_at"} {
		value, _ := recoveryString(fields, key, true)
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return "", nil, err
		}
	}
	if err := validateRecoveryAliases(fields); err != nil {
		return "", nil, err
	}
	if err := validateRecoveryOptionalFields(fields); err != nil {
		return "", nil, err
	}
	return id, fields, nil
}
func validateRecoveryDerived(raw json.RawMessage, ws string, issues map[string]map[string]json.RawMessage, seen map[string]bool) error {
	id, fields, err := validateRecoveryIssue(raw, ws)
	if err != nil {
		return err
	}
	original, exists := issues[id]
	if !exists || seen[id] || !sameRecoveryFields(fields, original) {
		return fmt.Errorf("derived issue differs from manifest")
	}
	seen[id] = true
	return nil
}
func validateRecoveryBlocked(rows []json.RawMessage, ws string, issues map[string]map[string]json.RawMessage) error {
	seen := map[string]bool{}
	for _, raw := range rows {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wrapper); err != nil {
			return err
		}
		if len(wrapper) != 2 || wrapper["issue"] == nil || wrapper["blockers"] == nil {
			return fmt.Errorf("invalid blocked wrapper")
		}
		var row struct {
			Issue    json.RawMessage    `json:"issue"`
			Blockers *[]json.RawMessage `json:"blockers"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		if err := validateRecoveryDerived(row.Issue, ws, issues, seen); err != nil {
			return err
		}
		if row.Blockers == nil || len(*row.Blockers) == 0 {
			return fmt.Errorf("missing blockers")
		}
		blockerIDs := map[string]bool{}
		for _, rawBlocker := range *row.Blockers {
			var reason struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(rawBlocker, &reason)
			if reason.Reason == "parent-blocked" && len(*row.Blockers) != 1 {
				return fmt.Errorf("mixed parent sentinel")
			}
			if err := validateRecoveryBlocker(rawBlocker, issues, blockerIDs); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateRecoveryBlocker(raw json.RawMessage, issues map[string]map[string]json.RawMessage, seen map[string]bool) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 6 {
		return fmt.Errorf("invalid blocker fields")
	}
	for _, key := range []string{"id", "title", "status", "dep_type", "reason"} {
		if _, err := recoveryString(fields, key, false); err != nil {
			return err
		}
	}
	var priority *int
	if err := json.Unmarshal(fields["priority"], &priority); err != nil || priority == nil {
		return fmt.Errorf("invalid blocker priority")
	}
	reason, _ := recoveryString(fields, "reason", true)
	dep, _ := recoveryString(fields, "dep_type", true)
	id, _ := recoveryString(fields, "id", false)
	title, _ := recoveryString(fields, "title", false)
	status, _ := recoveryString(fields, "status", false)
	if reason == "parent-blocked" {
		if dep != "parent-child" || id != "" || title != "" || status != "" || *priority != 0 {
			return fmt.Errorf("invalid parent sentinel")
		}
		return nil
	}
	if reason != "direct" || dep != "blocks" {
		return fmt.Errorf("invalid blocker reason")
	}
	original, exists := issues[id]
	if !exists || seen[id] {
		return fmt.Errorf("invalid blocker identity")
	}
	seen[id] = true
	for _, key := range []string{"title", "status", "priority"} {
		if !reflect.DeepEqual(fields[key], original[key]) {
			return fmt.Errorf("blocker differs from manifest")
		}
	}
	return nil
}

func sameRecoveryFields(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for key, raw := range left {
		other, ok := right[key]
		if !ok {
			return false
		}
		var a, b any
		first := json.NewDecoder(bytes.NewReader(raw))
		first.UseNumber()
		second := json.NewDecoder(bytes.NewReader(other))
		second.UseNumber()
		if first.Decode(&a) != nil || second.Decode(&b) != nil || !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}

// Native optional fields retain their complete values; none pass through the
// compatibility issue converter. Unknown future fields remain opaque.
func validateRecoveryOptionalFields(fields map[string]json.RawMessage) error {
	for _, key := range []string{"description", "design", "acceptance_criteria", "notes", "design_format", "assignee", "owner", "external_ref", "parent_id", "repo", "close_reason"} {
		if _, ok := fields[key]; ok {
			if _, err := recoveryString(fields, key, false); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"defer_until", "due_at", "closed_at"} {
		if raw, ok := fields[key]; ok && string(raw) != "null" {
			value, err := recoveryString(fields, key, true)
			if err != nil {
				return err
			}
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return err
			}
		}
	}
	return validateRecoveryCollections(fields)
}

func validateRecoveryCollections(fields map[string]json.RawMessage) error {
	if _, ok := fields["labels"]; !ok {
		return fmt.Errorf("missing labels")
	}
	if _, ok := fields["metadata"]; !ok {
		return fmt.Errorf("missing metadata")
	}
	if raw, ok := fields["labels"]; ok {
		var labels []*string
		if err := json.Unmarshal(raw, &labels); err != nil {
			return err
		}
		if labels == nil {
			return fmt.Errorf("null labels")
		}
		for _, label := range labels {
			if label == nil {
				return fmt.Errorf("null label")
			}
		}
	}
	if raw, ok := fields["metadata"]; ok {
		var metadata map[string]*string
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return err
		}
		if metadata == nil {
			return fmt.Errorf("null metadata")
		}
		for _, value := range metadata {
			if value == nil {
				return fmt.Errorf("null metadata value")
			}
		}
	}
	if raw, ok := fields["estimated_minutes"]; ok {
		var estimated *int
		if err := json.Unmarshal(raw, &estimated); err != nil {
			return err
		}
	}
	return nil
}

func validateRecoveryAliases(fields map[string]json.RawMessage) error {
	for alias, native := range map[string]string{"source_repo": "repo", "issue_type": "type", "parent": "parent_id"} {
		if _, exists := fields[alias]; !exists {
			continue
		}
		legacy, err := recoveryString(fields, alias, false)
		if err != nil {
			return err
		}
		canonical, err := recoveryString(fields, native, false)
		if err != nil {
			return err
		}
		if legacy != canonical {
			return fmt.Errorf("inconsistent alias %s", alias)
		}
	}
	return nil
}

func decodeRecoveryManifest(data []byte, ws string) (recoveryWire, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(data, &shape); err != nil {
		return recoveryWire{}, err
	}
	for _, key := range []string{"manifest", "workspace", "through", "issues", "total", "ready", "blocked", "deferred"} {
		if _, ok := shape[key]; !ok {
			return recoveryWire{}, fmt.Errorf("missing %s", key)
		}
	}
	if len(shape) != 8 {
		return recoveryWire{}, fmt.Errorf("unexpected manifest fields")
	}
	var wire recoveryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return recoveryWire{}, err
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(wire.Through, fleetOpaqueCursorPrefix))
	if wire.Manifest != recoveryManifest || wire.Workspace != ws || !strings.HasPrefix(wire.Through, fleetOpaqueCursorPrefix) || !isFixedFleetCursor(wire.Through) || string(decoded) == "0" || wire.Issues == nil || wire.Total == nil || wire.Ready == nil || wire.Blocked == nil || wire.Deferred == nil || *wire.Total != int64(len(*wire.Issues)) {
		return recoveryWire{}, fmt.Errorf("incomplete manifest")
	}

	return wire, nil
}
