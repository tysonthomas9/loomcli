// Package client is the typed Go client of the Agent API REST routes
// (design v2 §9.1), scoped to one workspace. It sends and decodes the
// agentsv1 wire types, sends each write's RequestID as the Idempotency-Key
// header, and never sends an actor: the server takes the caller from the
// request's authentication.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
)

// TokenSource returns the bearer token for a request; "" sends none.
type TokenSource func(ctx context.Context) (string, error)

// Config wires a Client. HTTP defaults to http.DefaultClient.
type Config struct {
	BaseURL   string // the loom serve origin, for example http://127.0.0.1:8080
	Workspace string
	Token     TokenSource
	HTTP      *http.Client
}

// Client calls one workspace's Agent API.
type Client struct {
	base  string
	token TokenSource
	http  *http.Client
}

// New returns a Client for cfg.
func New(cfg Config) *Client {
	c := &Client{base: strings.TrimRight(cfg.BaseURL, "/") + "/api/workspaces/" + url.PathEscape(cfg.Workspace) + "/v1/",
		token: cfg.Token, http: cfg.HTTP}
	if c.http == nil {
		c.http = http.DefaultClient
	}
	return c
}

// StatusError is an error response that carries no Agent API code, for
// example a failed authentication or an oversized body.
type StatusError struct {
	Status  int
	Message string
	Kind    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("agent API: HTTP %d: %s", e.Status, e.Message)
}

// Create creates an agent (POST agents).
func (c *Client) Create(ctx context.Context, requestID string, b agentsv1.CreateBody) (a agentsv1.Agent, err error) {
	return a, c.do(ctx, http.MethodPost, "agents", nil, requestID, b, &a)
}

// List returns one page of agents and the next page's cursor. The workspace
// is the client's; f.WorkspaceID and f.IncludeDeleted are not sent.
func (c *Client) List(ctx context.Context, f loomstore.AgentFilter) (l agentsv1.AgentList, err error) {
	q := url.Values{}
	for k, v := range map[string]string{"owner_kind": f.OwnerKind, "owner_id": f.OwnerID, "parent": f.Parent,
		"root": f.Root, "preset": f.Preset, "mode": f.Mode, "harness": f.Harness, "role_kind": f.RoleKind,
		"state": f.State, "subject_type": f.SubjectType, "subject_id": f.SubjectID,
		"external_key_prefix": f.ExternalKeyPrefix, "name": f.Name, "after": f.After} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.IncludeArchived {
		q.Set("include_archived", "true")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return l, c.do(ctx, http.MethodGet, "agents", q, "", nil, &l)
}

// Get returns an agent with its waiting messages and open asks.
func (c *Client) Get(ctx context.Context, agentID string) (a agentsv1.Agent, err error) {
	return a, c.do(ctx, http.MethodGet, agent(agentID), nil, "", nil, &a)
}

// Update changes an agent's name, model or harness (PATCH).
func (c *Client) Update(ctx context.Context, requestID, agentID string, b agentsv1.UpdateBody) (a agentsv1.Agent, err error) {
	return a, c.do(ctx, http.MethodPatch, agent(agentID), nil, requestID, b, &a)
}

// Delete deletes an agent. fingerprint confirms the unsaved work an earlier
// unsaved_work error listed.
func (c *Client) Delete(ctx context.Context, requestID, agentID string, cascade bool, fingerprint string) error {
	q := url.Values{}
	if cascade {
		q.Set("cascade", "true")
	}
	if fingerprint != "" {
		q.Set("fingerprint", fingerprint)
	}
	return c.do(ctx, http.MethodDelete, agent(agentID), q, requestID, nil, nil)
}

// Archive archives an agent; reason "" is done.
func (c *Client) Archive(ctx context.Context, requestID, agentID, reason string) error {
	return c.do(ctx, http.MethodPost, agent(agentID)+"/archive", nil, requestID, agentsv1.ArchiveBody{Reason: reason}, nil)
}

// Unarchive unarchives an agent.
func (c *Client) Unarchive(ctx context.Context, requestID, agentID string) error {
	return c.do(ctx, http.MethodPost, agent(agentID)+"/unarchive", nil, requestID, nil, nil)
}

// Send sends text from the authenticated caller.
func (c *Client) Send(ctx context.Context, requestID, agentID, text string) (r agentsv1.SendResult, err error) {
	return r, c.do(ctx, http.MethodPost, agent(agentID)+"/messages", nil, requestID, agentsv1.SendBody{Text: text}, &r)
}

// Withdraw clears the authenticated caller's waiting message.
func (c *Client) Withdraw(ctx context.Context, requestID, agentID string) (r agentsv1.WithdrawResult, err error) {
	return r, c.do(ctx, http.MethodDelete, agent(agentID)+"/messages/waiting", nil, requestID, nil, &r)
}

// Respond answers an open ask.
func (c *Client) Respond(ctx context.Context, requestID, agentID, askID string, b agentsv1.RespondBody) error {
	return c.do(ctx, http.MethodPost, agent(agentID)+"/asks/"+url.PathEscape(askID), nil, requestID, b, nil)
}

// ListEvents returns one snapshot-pinned page of an agent's events after
// q.After. To resume after a drop, call it again with After set to the last
// event's Seq (or the page's Next).
func (c *Client) ListEvents(ctx context.Context, q loomstore.EventQuery) (p agentsv1.EventPage, err error) {
	v := url.Values{}
	for k, n := range map[string]int64{"after": q.After, "snapshot": q.Snapshot, "limit": int64(q.Limit)} {
		if n > 0 {
			v.Set(k, strconv.FormatInt(n, 10))
		}
	}
	if len(q.Kinds) > 0 {
		v.Set("kind", strings.Join(q.Kinds, ","))
	}
	return p, c.do(ctx, http.MethodGet, agent(q.AgentID)+"/events", v, "", nil, &p)
}

// Presets lists the presets.
func (c *Client) Presets(ctx context.Context) ([]agentsv1.Preset, error) {
	var l agentsv1.PresetList
	err := c.do(ctx, http.MethodGet, "presets", nil, "", nil, &l)
	return l.Presets, err
}

// Preset returns a preset by name or name@version.
func (c *Client) Preset(ctx context.Context, name string) (p agentsv1.Preset, err error) {
	return p, c.do(ctx, http.MethodGet, "presets/"+url.PathEscape(name), nil, "", nil, &p)
}

func agent(id string) string { return "agents/" + url.PathEscape(id) }

// do sends one request; a non-2xx answer is returned by decodeError.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, requestID string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		req.Header.Set("Idempotency-Key", requestID)
	}
	if c.token != nil {
		tok, err := c.token(ctx)
		if err != nil {
			return err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return decodeError(resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// decodeError turns an error answer into a *loomagent.Error when it carries
// a code, else a *StatusError.
func decodeError(status int, raw []byte) error {
	var e struct {
		agentsv1.Error
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &e) != nil {
		e.Error.Error = strings.TrimSpace(string(raw))
	}
	if e.Code != "" {
		return &loomagent.Error{Code: e.Code, Message: e.Error.Error, Allowed: e.Allowed, Paths: e.Paths, Fingerprint: e.Fingerprint}
	}
	return &StatusError{Status: status, Message: e.Error.Error, Kind: e.Kind}
}
