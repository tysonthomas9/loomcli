package interactionmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type activityAPIStub struct {
	query interaction.ActivityQuery
	auth  authority.OperatorAuthority
}

func (stub *activityAPIStub) ListActivity(
	_ context.Context,
	auth authority.OperatorAuthority,
	query interaction.ActivityQuery,
) ([]interaction.Activity, error) {
	stub.auth = auth
	stub.query = query
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return []interaction.Activity{
		{
			WorkspaceKey: query.WorkspaceKey,
			AgentID:      query.AgentID,
			Kind:         interaction.ActivitySession,
			SourceID:     "session-1",
			TaskID:       "TASK-1",
			Status:       "completed",
			StartedAt:    now,
		},
		{
			WorkspaceKey: query.WorkspaceKey,
			AgentID:      query.AgentID,
			Kind:         interaction.ActivityBatchRun,
			SourceID:     "run-1",
			TaskID:       "TASK-2",
			Status:       "completed",
			StartedAt:    now.Add(-time.Minute),
		},
	}, nil
}

type operatorResolverStub struct {
	workspace string
	action    authority.Action
}

func (stub *operatorResolverStub) ResolveOperatorAuthority(
	_ *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	stub.workspace = workspace
	stub.action = action
	return authority.OperatorAuthority{}, nil
}

func TestAgentActivityCombinesInteractionProjectionWithTaskReferences(t *testing.T) {
	api := &activityAPIStub{}
	resolver := &operatorResolverStub{}
	mux := http.NewServeMux()
	New(Config{Interaction: api, Authority: resolver}).Register(mux)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/workspaces/WS/agents/docs/activity?limit=7",
		nil,
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if api.query.WorkspaceKey != "WS" || api.query.AgentID != "docs" || api.query.Limit != 7 {
		t.Fatalf("query = %+v", api.query)
	}
	if resolver.workspace != "WS" || resolver.action != interaction.ActionReadActivity {
		t.Fatalf("authority scope = %q/%q", resolver.workspace, resolver.action)
	}
	var payload struct {
		AgentID  string                 `json:"agent_id"`
		Activity []interaction.Activity `json:"activity"`
		Count    int                    `json:"count"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.AgentID != "docs" || payload.Count != 2 ||
		len(payload.Activity) != 2 ||
		payload.Activity[0].TaskID != "TASK-1" ||
		payload.Activity[1].Kind != interaction.ActivityBatchRun {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAgentActivityFailsClosedWithoutCapabilityOrAuthority(t *testing.T) {
	for name, config := range map[string]Config{
		"capability": {Authority: &operatorResolverStub{}},
		"authority":  {Interaction: &activityAPIStub{}},
	} {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			New(config).Register(mux)
			response := httptest.NewRecorder()
			mux.ServeHTTP(
				response,
				withCanonicalWorkspace(httptest.NewRequest(
					http.MethodGet,
					"/api/workspaces/WS/agents/docs/activity",
					nil,
				), "WS", "WS"),
			)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
			}
		})
	}
}

type sessionCommandAPIStub struct {
	activityAPIStub
	order             []string
	terminalCommand   interaction.UpdateTerminalCommand
	finishCommand     interaction.FinishSessionCommand
	transcriptCommand interaction.PublishTranscriptCommand
	completeCommand   interaction.CompleteInboxCommand
}

func (stub *sessionCommandAPIStub) PublishTranscript(
	_ context.Context,
	_ authority.SessionAuthority,
	command interaction.PublishTranscriptCommand,
) (*interaction.AgentSession, error) {
	stub.order = append(stub.order, "transcript")
	command.Content = append([]byte(nil), command.Content...)
	command.Metadata = cloneTestMetadata(command.Metadata)
	stub.transcriptCommand = command
	return &interaction.AgentSession{
		WorkspaceKey:         command.WorkspaceKey,
		SessionID:            command.SessionID,
		TranscriptArtifactID: "transcript-" + command.SessionID,
	}, nil
}

func cloneTestMetadata(input map[string]string) map[string]string {
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func (*sessionCommandAPIStub) PatchSession(
	context.Context,
	authority.SessionAuthority,
	interaction.PatchSessionCommand,
) (*interaction.AgentSession, error) {
	return nil, nil
}

func (*sessionCommandAPIStub) HeartbeatSession(
	context.Context,
	authority.SessionAuthority,
	interaction.HeartbeatSessionCommand,
) (*interaction.AgentSession, error) {
	return nil, nil
}

func (stub *sessionCommandAPIStub) FinishSession(
	_ context.Context,
	_ authority.SessionAuthority,
	command interaction.FinishSessionCommand,
) (*interaction.AgentSession, error) {
	stub.order = append(stub.order, "finish")
	stub.finishCommand = command
	return &interaction.AgentSession{
		WorkspaceKey: command.WorkspaceKey,
		SessionID:    command.SessionID,
		Status:       command.Status,
	}, nil
}

func (stub *sessionCommandAPIStub) UpdateTerminal(
	_ context.Context,
	_ authority.SessionAuthority,
	command interaction.UpdateTerminalCommand,
) (*interaction.TerminalSession, error) {
	stub.order = append(stub.order, "terminal")
	stub.terminalCommand = command
	return &interaction.TerminalSession{
		WorkspaceKey: command.WorkspaceKey,
		TerminalID:   command.TerminalID,
		Status:       command.Status,
	}, nil
}

func (*sessionCommandAPIStub) ClaimInbox(
	context.Context,
	authority.SessionAuthority,
	interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, nil
}

func (stub *sessionCommandAPIStub) CompleteInbox(
	_ context.Context,
	_ authority.SessionAuthority,
	command interaction.CompleteInboxCommand,
) (*interaction.InboxMessage, error) {
	stub.completeCommand = command
	return &interaction.InboxMessage{
		WorkspaceKey: command.WorkspaceKey,
		MessageID:    command.MessageID,
		SessionID:    command.SessionID,
		Attempt:      command.Attempt,
		Status:       command.Status,
	}, nil
}

type sessionAuthorityResolverStub struct {
	issuer  *authority.Issuer
	actions []authority.Action
	tokens  []string
	proofs  []interaction.SessionAuthorityProof
}

func newSessionAuthorityResolverStub() *sessionAuthorityResolverStub {
	return &sessionAuthorityResolverStub{issuer: authority.NewIssuer()}
}

func (stub *sessionAuthorityResolverStub) ResolveSessionAuthority(
	_ context.Context,
	action authority.Action,
	proof interaction.SessionAuthorityProof,
) (authority.SessionAuthority, error) {
	stub.actions = append(stub.actions, action)
	stub.tokens = append(stub.tokens, string(proof.Token.Bytes()))
	proof.Token = nil
	stub.proofs = append(stub.proofs, proof)
	principal, err := stub.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "session:" + proof.SessionID,
		Class:     authority.ClassSession,
		Workspace: proof.WorkspaceKey,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		return authority.SessionAuthority{}, err
	}
	return stub.issuer.IssueSessionForOwner(
		principal,
		proof.WorkspaceKey,
		action,
		authority.SessionOwner{
			SessionID:    proof.SessionID,
			AgentID:      proof.AgentID,
			TerminalID:   proof.TerminalID,
			NodeID:       proof.NodeID,
			LeaseID:      proof.LeaseID,
			FencingToken: proof.FencingToken,
		},
	)
}

func TestFinishSessionUsesOneAtomicCommandWithoutTokenLeak(t *testing.T) {
	const rawToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	api := &sessionCommandAPIStub{}
	resolver := newSessionAuthorityResolverStub()
	mux := http.NewServeMux()
	New(Config{
		Interaction: api, Authority: &operatorResolverStub{},
		SessionAuthorities: resolver,
	}).Register(mux)

	body := []byte(`{
		"agent_id":"agent-docs",
		"terminal_id":"terminal-1",
		"node_id":"node-1",
		"lease_id":"lease-1",
		"fencing_token":7,
		"status":"completed",
		"summary":"done"
	}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/interaction/sessions/session-1/finish",
		bytes.NewReader(body),
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	request.Header.Set(sessionTokenHeader, rawToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), rawToken) {
		t.Fatalf("response leaked session token: %s", response.Body.String())
	}
	if request.Header.Get(sessionTokenHeader) != "" {
		t.Fatal("session token header remained on request after authority derivation")
	}
	if len(api.order) != 1 || api.order[0] != "finish" {
		t.Fatalf("mutation order = %v", api.order)
	}
	if api.finishCommand.Status != interaction.SessionCompleted {
		t.Fatalf("finish command = %+v", api.finishCommand)
	}
	if len(resolver.actions) != 1 ||
		resolver.actions[0] != interaction.ActionFinishSession {
		t.Fatalf("resolved actions = %v", resolver.actions)
	}
	for _, token := range resolver.tokens {
		if token != rawToken {
			t.Fatalf("resolved token = %q", token)
		}
	}
}

func TestPublishTranscriptStreamsContentWithHeaderProofAndNoTokenLeak(t *testing.T) {
	const rawToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	api := &sessionCommandAPIStub{}
	resolver := newSessionAuthorityResolverStub()
	mux := http.NewServeMux()
	New(Config{
		Interaction: api, Authority: &operatorResolverStub{},
		SessionAuthorities: resolver,
	}).Register(mux)

	content := []byte("{\"seq\":1,\"role\":\"user\",\"text\":\"hello\"}\n")
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/interaction/sessions/session-1/transcript",
		bytes.NewReader(content),
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	request.Header.Set("Content-Type", "application/x-ndjson")
	request.Header.Set(sessionTokenHeader, rawToken)
	request.Header.Set(sessionAgentHeader, "agent-docs")
	request.Header.Set(sessionTerminalHeader, "terminal-1")
	request.Header.Set(sessionNodeHeader, "node-1")
	request.Header.Set(sessionLeaseHeader, "lease-1")
	request.Header.Set(sessionFenceHeader, "7")
	request.Header.Set(transcriptMetadataHeader, `{"backend":"codex"}`)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), rawToken) || request.Header.Get(sessionTokenHeader) != "" {
		t.Fatalf("session credential survived transcript request: body=%s header=%q", response.Body.String(), request.Header.Get(sessionTokenHeader))
	}
	if len(api.order) != 1 || api.order[0] != "transcript" ||
		api.transcriptCommand.WorkspaceKey != "WS" ||
		api.transcriptCommand.SessionID != "session-1" ||
		string(api.transcriptCommand.Content) != string(content) ||
		api.transcriptCommand.Metadata["backend"] != "codex" {
		t.Fatalf("transcript command/order = %+v/%v", api.transcriptCommand, api.order)
	}
	if len(resolver.actions) != 1 || resolver.actions[0] != interaction.ActionPublishTranscript ||
		resolver.tokens[0] != rawToken {
		t.Fatalf("resolved action/tokens = %v/%v", resolver.actions, resolver.tokens)
	}
	for _, name := range []string{
		sessionAgentHeader, sessionTerminalHeader, sessionNodeHeader,
		sessionLeaseHeader, sessionFenceHeader, transcriptMetadataHeader,
	} {
		if request.Header.Get(name) != "" {
			t.Fatalf("session transcript header %s remained on request", name)
		}
	}
}

func TestCompleteInboxForwardsExactClaimAttempt(t *testing.T) {
	const rawToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	api := &sessionCommandAPIStub{}
	resolver := newSessionAuthorityResolverStub()
	mux := http.NewServeMux()
	New(Config{
		Interaction: api, Authority: &operatorResolverStub{},
		SessionAuthorities: resolver,
	}).Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/interaction/sessions/session-1/inbox/message-1/complete",
		strings.NewReader(`{
			"agent_id":"agent-docs",
			"terminal_id":"terminal-1",
			"node_id":"node-1",
			"lease_id":"lease-1",
			"fencing_token":7,
			"attempt":3,
			"status":"queued",
			"error_class":"runtime_busy"
		}`),
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	request.Header.Set(sessionTokenHeader, rawToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if api.completeCommand.WorkspaceKey != "WS" ||
		api.completeCommand.SessionID != "session-1" ||
		api.completeCommand.MessageID != "message-1" ||
		api.completeCommand.Attempt != 3 ||
		api.completeCommand.Status != interaction.InboxQueued {
		t.Fatalf("complete command = %+v", api.completeCommand)
	}
}

func TestSessionCommandRejectsNonCanonicalTokenBeforeAuthorityResolution(t *testing.T) {
	api := &sessionCommandAPIStub{}
	resolver := newSessionAuthorityResolverStub()
	mux := http.NewServeMux()
	New(Config{
		Interaction: api, Authority: &operatorResolverStub{},
		SessionAuthorities: resolver,
	}).Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/interaction/sessions/session-1/heartbeat",
		strings.NewReader(`{
			"agent_id":"agent-docs",
			"node_id":"node-1",
			"lease_id":"lease-1",
			"fencing_token":7
		}`),
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	request.Header.Set(
		sessionTokenHeader,
		"ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if len(resolver.actions) != 0 {
		t.Fatalf("authority resolver called for invalid token: %v", resolver.actions)
	}
}

func TestInteractionRoutesUseCanonicalWorkspaceAndFailClosedWithoutResolution(t *testing.T) {
	t.Run("activity alias resolves to canonical workspace", func(t *testing.T) {
		api := &activityAPIStub{}
		resolver := &operatorResolverStub{}
		mux := http.NewServeMux()
		New(Config{Interaction: api, Authority: resolver}).Register(mux)
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/workspaces/ALIAS/agents/docs/activity",
			nil,
		)
		request = withCanonicalWorkspace(request, "ALIAS", "CANONICAL")
		response := httptest.NewRecorder()

		mux.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
		}
		if api.query.WorkspaceKey != "CANONICAL" || resolver.workspace != "CANONICAL" {
			t.Fatalf("query/authority workspaces = %q/%q", api.query.WorkspaceKey, resolver.workspace)
		}
	})

	t.Run("session alias resolves to canonical workspace", func(t *testing.T) {
		const rawToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		api := &sessionCommandAPIStub{}
		resolver := newSessionAuthorityResolverStub()
		mux := http.NewServeMux()
		New(Config{
			Interaction: api, Authority: &operatorResolverStub{},
			SessionAuthorities: resolver,
		}).Register(mux)
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/ALIAS/interaction/sessions/session-1/finish",
			strings.NewReader(`{
				"agent_id":"agent-docs",
				"node_id":"node-1",
				"lease_id":"lease-1",
				"fencing_token":7,
				"status":"completed"
			}`),
		)
		request = withCanonicalWorkspace(request, "ALIAS", "CANONICAL")
		request.Header.Set(sessionTokenHeader, rawToken)
		response := httptest.NewRecorder()

		mux.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
		}
		if api.finishCommand.WorkspaceKey != "CANONICAL" ||
			len(resolver.proofs) != 1 ||
			resolver.proofs[0].WorkspaceKey != "CANONICAL" {
			t.Fatalf("command/proof = %+v/%+v", api.finishCommand, resolver.proofs)
		}
	})

	t.Run("missing canonical workspace fails closed", func(t *testing.T) {
		api := &activityAPIStub{}
		resolver := &operatorResolverStub{}
		mux := http.NewServeMux()
		New(Config{Interaction: api, Authority: resolver}).Register(mux)
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/workspaces/WS/agents/docs/activity",
			nil,
		)
		response := httptest.NewRecorder()

		mux.ServeHTTP(response, request)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
		}
		if resolver.workspace != "" || api.query.WorkspaceKey != "" {
			t.Fatalf("capability/authority invoked = %q/%q", api.query.WorkspaceKey, resolver.workspace)
		}
	})
}

func withCanonicalWorkspace(request *http.Request, requested, canonical string) *http.Request {
	ref := middleware.WorkspaceRef{RequestedID: requested, CanonicalID: canonical}
	return request.WithContext(middleware.WithWorkspaceRef(request.Context(), ref))
}
