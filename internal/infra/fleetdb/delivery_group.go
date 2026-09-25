package fleetdb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// deliveryGroupStore talks to FleetDB's DeliveryGroup REST API. Group writes
// never call GitHub; membership identity is validated by FleetDB against
// registered repositories.
//
// Deploy dependency: honest List cursor pagination (`limit`/`cursor`/
// `has_more`/`next_cursor`) requires FleetDB PR #367 @
// 502b365f313014c2c921f9b5f1089849151d23b5. Against FleetDB #365 alone those
// query params are ignored and missing has_more decodes as false — do not
// claim bounded group pages until #367 is deployed with this facade.
type deliveryGroupStore struct{ client *Client }

var _ store.DeliveryGroupStore = (*deliveryGroupStore)(nil)

// DeliveryGroups returns the delivery-group sub-store. The Client also
// satisfies store.OptionalDeliveryGroups.
func (c *Client) DeliveryGroups() store.DeliveryGroupStore {
	if c == nil {
		return nil
	}
	return &deliveryGroupStore{client: c}
}

type deliveryGroupWire struct {
	WorkspaceKey string                    `json:"workspace_key"`
	ID           string                    `json:"id"`
	Title        string                    `json:"title"`
	EpicID       string                    `json:"epic_id,omitempty"`
	Owner        string                    `json:"owner,omitempty"`
	State        string                    `json:"state"`
	Revision     int64                     `json:"revision"`
	Members      []deliveryGroupMemberWire `json:"members"`
	LastOpID     string                    `json:"last_op_id"`
	LastOpDigest string                    `json:"last_op_digest,omitempty"`
	CreatedAt    time.Time                 `json:"created_at"`
	UpdatedAt    time.Time                 `json:"updated_at"`
	CreatedBy    string                    `json:"created_by,omitempty"`
	UpdatedBy    string                    `json:"updated_by,omitempty"`
	Integrity    string                    `json:"integrity,omitempty"`
	Inconsistent bool                      `json:"inconsistent,omitempty"`
}

type deliveryGroupMemberWire struct {
	PRKey          string    `json:"pr_key"`
	RepoName       string    `json:"repo_name"`
	PRNumber       int       `json:"pr_number"`
	GitHubNodeID   string    `json:"github_node_id,omitempty"`
	Source         string    `json:"source"`
	TaskID         string    `json:"task_id,omitempty"`
	LineageStackID string    `json:"lineage_stack_id,omitempty"`
	AddedAt        time.Time `json:"added_at"`
	AddedBy        string    `json:"added_by,omitempty"`
}

func (w deliveryGroupWire) toDomain() *domain.DeliveryGroup {
	members := make([]domain.DeliveryGroupMember, 0, len(w.Members))
	for _, m := range w.Members {
		members = append(members, domain.DeliveryGroupMember{
			PRKey:          m.PRKey,
			RepoName:       m.RepoName,
			PRNumber:       m.PRNumber,
			GitHubNodeID:   m.GitHubNodeID,
			Source:         domain.DeliveryGroupMemberSource(m.Source),
			TaskID:         m.TaskID,
			LineageStackID: m.LineageStackID,
			AddedAt:        m.AddedAt,
			AddedBy:        m.AddedBy,
		})
	}
	return &domain.DeliveryGroup{
		WorkspaceKey: w.WorkspaceKey,
		ID:           w.ID,
		Title:        w.Title,
		EpicID:       w.EpicID,
		Owner:        w.Owner,
		State:        domain.DeliveryGroupState(w.State),
		Revision:     w.Revision,
		Members:      members,
		LastOpID:     w.LastOpID,
		LastOpDigest: w.LastOpDigest,
		CreatedAt:    w.CreatedAt,
		UpdatedAt:    w.UpdatedAt,
		CreatedBy:    w.CreatedBy,
		UpdatedBy:    w.UpdatedBy,
		Integrity:    w.Integrity,
		Inconsistent: w.Inconsistent,
	}
}

func memberInputsToWire(in []domain.DeliveryGroupMemberInput) []map[string]any {
	if in == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, m := range in {
		row := map[string]any{}
		if m.PRKey != "" {
			row["pr_key"] = m.PRKey
		}
		if m.RepoName != "" {
			row["repo_name"] = m.RepoName
		}
		if m.PRNumber > 0 {
			row["pr_number"] = m.PRNumber
		}
		if m.GitHubNodeID != "" {
			row["github_node_id"] = m.GitHubNodeID
		}
		if m.Source != "" {
			row["source"] = string(m.Source)
		}
		if m.TaskID != "" {
			row["task_id"] = m.TaskID
		}
		if m.LineageStackID != "" {
			row["lineage_stack_id"] = m.LineageStackID
		}
		out = append(out, row)
	}
	return out
}

func deliveryGroupsPath(ws string) string {
	return "/api/v1/" + pathEscape(ws) + "/delivery-groups"
}

func deliveryGroupPath(ws, id string) string {
	return deliveryGroupsPath(ws) + "/" + pathEscape(id)
}

func (s *deliveryGroupStore) List(ctx context.Context, ws string, opts store.DeliveryGroupListOpts) (*store.DeliveryGroupPage, error) {
	q := url.Values{}
	if state := strings.TrimSpace(opts.State); state != "" {
		q.Set("state", state)
	}
	if epic := strings.TrimSpace(opts.EpicID); epic != "" {
		q.Set("epic_id", epic)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if cursor := strings.TrimSpace(opts.Cursor); cursor != "" {
		q.Set("cursor", cursor)
	}
	var resp struct {
		DeliveryGroups []deliveryGroupWire `json:"delivery_groups"`
		Count          int                 `json:"count"`
		HasMore        bool                `json:"has_more"`
		NextCursor     string              `json:"next_cursor,omitempty"`
	}
	if err := s.client.do(ctx, http.MethodGet, withQuery(deliveryGroupsPath(ws), q), nil, &resp); err != nil {
		return nil, err
	}
	page := &store.DeliveryGroupPage{
		Groups:     make([]*domain.DeliveryGroup, 0, len(resp.DeliveryGroups)),
		Count:      resp.Count,
		HasMore:    resp.HasMore,
		NextCursor: resp.NextCursor,
	}
	for _, g := range resp.DeliveryGroups {
		page.Groups = append(page.Groups, g.toDomain())
	}
	if page.Count == 0 {
		page.Count = len(page.Groups)
	}
	return page, nil
}

func (s *deliveryGroupStore) Get(ctx context.Context, ws, groupID string) (*domain.DeliveryGroup, error) {
	var resp deliveryGroupWire
	if err := s.client.do(ctx, http.MethodGet, deliveryGroupPath(ws, groupID), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *deliveryGroupStore) GetByPR(ctx context.Context, ws, prKey string) (*domain.DeliveryGroup, error) {
	path := deliveryGroupsPath(ws) + "/by-pr/" + pathEscape(prKey)
	var resp deliveryGroupWire
	if err := s.client.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *deliveryGroupStore) Create(ctx context.Context, ws, idempotencyKey string, in domain.DeliveryGroupCreate) (*store.DeliveryGroupWriteResult, error) {
	body := map[string]any{
		"id":    in.ID,
		"title": in.Title,
	}
	if in.EpicID != "" {
		body["epic_id"] = in.EpicID
	}
	if in.Owner != "" {
		body["owner"] = in.Owner
	}
	if in.Members != nil {
		body["members"] = memberInputsToWire(in.Members)
	}
	return s.write(ctx, http.MethodPost, deliveryGroupsPath(ws), idempotencyKey, "", body)
}

func (s *deliveryGroupStore) Update(ctx context.Context, ws, groupID, ifMatch, idempotencyKey string, in domain.DeliveryGroupUpdate) (*store.DeliveryGroupWriteResult, error) {
	body := map[string]any{}
	if in.Title != nil {
		body["title"] = *in.Title
	}
	if in.EpicID != nil {
		body["epic_id"] = *in.EpicID
	}
	if in.Owner != nil {
		body["owner"] = *in.Owner
	}
	return s.write(ctx, http.MethodPatch, deliveryGroupPath(ws, groupID), idempotencyKey, ifMatch, body)
}

func (s *deliveryGroupStore) SetMembers(ctx context.Context, ws, groupID, ifMatch, idempotencyKey string, members []domain.DeliveryGroupMemberInput) (*store.DeliveryGroupWriteResult, error) {
	body := map[string]any{
		"members": memberInputsToWire(members),
	}
	if members == nil {
		body["members"] = []map[string]any{}
	}
	path := deliveryGroupPath(ws, groupID) + "/members"
	return s.write(ctx, http.MethodPut, path, idempotencyKey, ifMatch, body)
}

func (s *deliveryGroupStore) Archive(ctx context.Context, ws, groupID, ifMatch, idempotencyKey string) (*store.DeliveryGroupWriteResult, error) {
	path := deliveryGroupPath(ws, groupID) + "/archive"
	return s.write(ctx, http.MethodPost, path, idempotencyKey, ifMatch, nil)
}

func (s *deliveryGroupStore) write(ctx context.Context, method, path, idempotencyKey, ifMatch string, body any) (*store.DeliveryGroupWriteResult, error) {
	headers := map[string]string{}
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		headers["X-Idempotency-Key"] = key
	}
	if match := strings.TrimSpace(ifMatch); match != "" {
		headers["If-Match"] = match
	}
	var resp deliveryGroupWire
	status, hdr, err := s.client.doWithResponse(ctx, method, path, body, &resp, headers)
	if err != nil {
		return nil, err
	}
	return &store.DeliveryGroupWriteResult{
		Group:    resp.toDomain(),
		Status:   status,
		ETag:     hdr.Get("ETag"),
		Replayed: strings.EqualFold(hdr.Get("X-Idempotency-Replayed"), "true"),
	}, nil
}

// deliveryGroupConflictError builds a typed conflict from FleetDB's envelope.
func deliveryGroupConflictError(prefix string, body []byte) error {
	code := extractErrorCode(body)
	meta := extractErrorMeta(body)
	err := &domain.DeliveryGroupConflictError{
		Code:      code,
		Message:   prefix,
		PRKey:     meta["pr_key"],
		GroupID:   meta["group_id"],
		Revision:  domain.ParseRevisionMeta(meta, "revision"),
		Retryable: strings.EqualFold(meta["retryable"], "true") || code == domain.DeliveryGroupConflictRetryable,
		Meta:      meta,
	}
	if err.Code == "" {
		err.Code = domain.DeliveryGroupConflictAlreadyExists
	}
	return err
}

func deliveryGroupPreconditionError(prefix string, body []byte) error {
	meta := extractErrorMeta(body)
	return &domain.DeliveryGroupPreconditionError{
		Code:             domain.DeliveryGroupPreconditionFailedCode,
		Message:          prefix,
		ExpectedRevision: domain.ParseRevisionMeta(meta, "expected_revision"),
		StoredRevision:   domain.ParseRevisionMeta(meta, "stored_revision"),
		Meta:             meta,
	}
}

func isDeliveryGroupAPIPath(requestPath string) bool {
	requestPath, _, _ = strings.Cut(requestPath, "?")
	segments := strings.Split(strings.Trim(requestPath, "/"), "/")
	// /api/v1/{ws}/delivery-groups[...]
	return len(segments) >= 4 && segments[0] == "api" && segments[1] == "v1" && segments[3] == "delivery-groups"
}

// classifyDeliveryGroupHTTPError handles delivery-group-specific status/code
// mapping. Returns nil when the response is not a delivery-group error that
// needs a specialized sentinel.
func classifyDeliveryGroupHTTPError(method, path string, status int, body []byte) error {
	if !isDeliveryGroupAPIPath(path) {
		return nil
	}
	code := extractErrorCode(body)
	prefix := fmt.Sprintf("fleetdb: %s %s: HTTP %d", method, path, status)
	if msg := extractErrorMessage(body); msg != "" {
		prefix += ": " + msg
	}
	switch status {
	case http.StatusConflict:
		return deliveryGroupConflictError(prefix, body)
	case http.StatusPreconditionFailed:
		return deliveryGroupPreconditionError(prefix, body)
	case http.StatusPreconditionRequired:
		return fmt.Errorf("%s: %w", prefix, domain.ErrInvalid)
	case http.StatusServiceUnavailable:
		if code == domain.DeliveryGroupInconsistentCode {
			return fmt.Errorf("%s: %w", prefix, domain.ErrDeliveryGroupInconsistent)
		}
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("%s: %w", prefix, domain.ErrInvalid)
	}
	return nil
}
