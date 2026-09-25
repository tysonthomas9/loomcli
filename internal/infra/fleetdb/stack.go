package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/stackstore/stackwire"
)

// StackClient is the HTTP client for fleet-db's stack lineage API
// (/api/v1/{workspace}/stacks), implementing stackwire.API. stackstore adapts
// it to the stacklineage domain types and the stackstore sentinel errors,
// reaching it through the store.Store handle (see stackwire.Provider). It is
// deliberately not part of store.Store: stack lineage has its own persistence
// contract (stackstore.Store).
type StackClient struct{ client *Client }

var _ stackwire.API = (*StackClient)(nil)

// Stacks returns the stack lineage API client.
func (c *Client) Stacks() stackwire.API { return &StackClient{client: c} }

// Wire shapes live in stackwire; these aliases keep call sites in this package
// short.
type (
	StackWire       = stackwire.Stack
	StackNodeWire   = stackwire.Node
	StackEnsure     = stackwire.Ensure
	StackNodePatch  = stackwire.NodePatch
	StackMoveReq    = stackwire.MoveRequest
	StackMoveResult = stackwire.MoveResult
	StackAPIError   = stackwire.APIError
	StackProvider   = stackwire.Provider
)

func (s *StackClient) stackPath(ws, id string) string {
	return "/api/v1/" + pathEscape(ws) + "/stacks/" + pathEscape(id)
}

func (s *StackClient) nodePath(ws, id, taskID string) string {
	return s.stackPath(ws, id) + "/nodes/" + pathEscape(taskID)
}

// Ensure creates the stack or updates its header.
func (s *StackClient) Ensure(ctx context.Context, ws, id string, in StackEnsure) (*StackWire, error) {
	var out StackWire
	if err := s.do(ctx, http.MethodPut, s.stackPath(ws, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns a live stack with its nodes in lineage order.
func (s *StackClient) Get(ctx context.Context, ws, id string) (*StackWire, error) {
	var out StackWire
	if err := s.do(ctx, http.MethodGet, s.stackPath(ws, id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns every live stack in the workspace, sorted by ID.
func (s *StackClient) List(ctx context.Context, ws string) ([]StackWire, error) {
	var out struct {
		Stacks []StackWire `json:"stacks"`
	}
	if err := s.do(ctx, http.MethodGet, "/api/v1/"+pathEscape(ws)+"/stacks", nil, &out); err != nil {
		return nil, err
	}
	return out.Stacks, nil
}

// Delete tombstones the stack and all its nodes.
func (s *StackClient) Delete(ctx context.Context, ws, id string) error {
	return s.do(ctx, http.MethodDelete, s.stackPath(ws, id), nil, nil)
}

// ListNodes returns the stack's nodes in lineage order.
func (s *StackClient) ListNodes(ctx context.Context, ws, id string) ([]StackNodeWire, error) {
	var out struct {
		Nodes []StackNodeWire `json:"nodes"`
	}
	if err := s.do(ctx, http.MethodGet, s.stackPath(ws, id)+"/nodes", nil, &out); err != nil {
		return nil, err
	}
	return out.Nodes, nil
}

// AddNode registers taskID on baseTaskID ("" = root unit).
func (s *StackClient) AddNode(ctx context.Context, ws, id, taskID, baseTaskID, mode string) (*StackNodeWire, error) {
	body := struct {
		TaskID     string `json:"task_id"`
		BaseTaskID string `json:"base_task_id,omitempty"`
		CommitMode string `json:"commit_mode,omitempty"`
	}{TaskID: taskID, BaseTaskID: baseTaskID, CommitMode: mode}
	var out StackNodeWire
	if err := s.do(ctx, http.MethodPost, s.stackPath(ws, id)+"/nodes", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetBase repoints taskID's predecessor ("" = root unit).
func (s *StackClient) SetBase(ctx context.Context, ws, id, taskID, baseTaskID string) (*StackNodeWire, error) {
	body := struct {
		BaseTaskID string `json:"base_task_id"`
	}{BaseTaskID: baseTaskID}
	var out StackNodeWire
	if err := s.do(ctx, http.MethodPut, s.nodePath(ws, id, taskID)+"/base", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MoveNode atomically splices taskID to sit immediately after req.AfterTaskID.
// When req.ExpectedRevision is set, a stale stack document revision returns
// 412 precondition_failed.
func (s *StackClient) MoveNode(ctx context.Context, ws, id, taskID string, req stackwire.MoveRequest) (*stackwire.MoveResult, error) {
	var out stackwire.MoveResult
	if err := s.do(ctx, http.MethodPost, s.nodePath(ws, id, taskID)+"/move", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveNode drops taskID, reparenting its successor onto its predecessor.
func (s *StackClient) RemoveNode(ctx context.Context, ws, id, taskID string) error {
	return s.do(ctx, http.MethodDelete, s.nodePath(ws, id, taskID), nil, nil)
}

// UpdateNode applies a publish-state patch to taskID.
func (s *StackClient) UpdateNode(ctx context.Context, ws, id, taskID string, patch StackNodePatch) (*StackNodeWire, error) {
	var out StackNodeWire
	if err := s.do(ctx, http.MethodPatch, s.nodePath(ws, id, taskID), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do is Client.do with error responses surfaced as *StackAPIError.
func (s *StackClient) do(ctx context.Context, method, path string, body, out any) error {
	c := s.client
	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, c.baseURL+path, auth, body)
	if err != nil {
		return fmt.Errorf("fleetdb: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fleetdb: %s %s: %w", method, path, err)
	}
	if resp.StatusCode < http.StatusBadRequest {
		// Success (and the redirect guard) share the generic decode path.
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
		return decodeStackResponse(resp, method, path, out)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if readErr != nil {
		return fmt.Errorf("fleetdb: %s %s: HTTP %d (read body: %w)", method, path, resp.StatusCode, readErr)
	}
	classified := classifyHTTPError(method, path, resp.StatusCode, respBody)
	if resp.StatusCode == http.StatusPreconditionFailed {
		// classifyHTTPError reads every precondition_failed as a skill
		// revision conflict; a stack 412 is a node revision race.
		classified = fmt.Errorf("fleetdb: %s %s: HTTP %d: %s: %w",
			method, path, resp.StatusCode, extractErrorMessage(respBody), domain.ErrConflict)
	}
	return &StackAPIError{
		Status:  resp.StatusCode,
		Code:    extractErrorCode(respBody),
		Message: extractErrorMessage(respBody),
		Err:     classified,
	}
}

func decodeStackResponse(resp *http.Response, method, path string, out any) error {
	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fleetdb: %s %s: unexpected HTTP %d redirect", method, path, resp.StatusCode)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			// Every stack endpoint that takes an out answers with a document;
			// an empty body would otherwise read as a zero-valued stack.
			err = errStackEmptyBody
		}
		return fmt.Errorf("fleetdb: decode response (%s %s): %w", method, path, err)
	}
	return nil
}

// errStackEmptyBody is returned when a stack endpoint that must answer with a
// document answers with none.
var errStackEmptyBody = errors.New("empty response body")
