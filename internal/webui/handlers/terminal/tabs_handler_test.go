package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

func TestTerminalTabHandlers(t *testing.T) {
	fake := &fakeTerminalService{tab: &tabmeta.TabMetadata{SessionName: "sess", Workspace: "WS", Label: "Shell"}}

	rec := httptest.NewRecorder()
	HandleListTerminalTabs(fake).ServeHTTP(rec, terminalReq(http.MethodGet, "/tabs", nil, "WS", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandleGetTerminalTab(fake).ServeHTTP(rec, terminalReq(http.MethodGet, "/tabs/sess", nil, "WS", "sess"))
	if rec.Code != http.StatusOK || fake.getSession != "sess" {
		t.Fatalf("get status=%d session=%q body=%s", rec.Code, fake.getSession, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandlePatchTerminalTab(fake).ServeHTTP(rec, terminalReq(http.MethodPatch, "/tabs/sess", []byte(`{"label":"New","notes":"n","sort_order":2,"pinned":true,"issue_id":"T-1"}`), "WS", "sess"))
	if rec.Code != http.StatusOK || fake.patchFields["label"] != "New" || fake.patchFields["pinned"] != "true" {
		t.Fatalf("patch status=%d fields=%v body=%s", rec.Code, fake.patchFields, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandlePutTerminalTab(fake).ServeHTTP(rec, terminalReq(http.MethodPut, "/tabs/sess", []byte(`{"label":"Shell","sort_order":3,"notes":"notes","pinned":true}`), "WS", "sess"))
	if rec.Code != http.StatusOK || fake.putMeta == nil || fake.putMeta.SessionName != "sess" || fake.putMeta.Workspace != "WS" || fake.putMeta.CreatedAt.IsZero() {
		t.Fatalf("put status=%d meta=%+v body=%s", rec.Code, fake.putMeta, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandleDeleteTerminalTab(fake).ServeHTTP(rec, terminalReq(http.MethodDelete, "/tabs/sess", nil, "WS", "sess"))
	if rec.Code != http.StatusOK || fake.deleted != "sess" {
		t.Fatalf("delete status=%d deleted=%q body=%s", rec.Code, fake.deleted, rec.Body.String())
	}
}

func TestTerminalTabHandlerValidation(t *testing.T) {
	fake := &fakeTerminalService{tab: &tabmeta.TabMetadata{SessionName: "sess", Workspace: "WS", Label: "Shell"}}
	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    []byte
	}{
		{"bad patch json", HandlePatchTerminalTab(fake), http.MethodPatch, []byte(`{`)},
		{"empty patch", HandlePatchTerminalTab(fake), http.MethodPatch, []byte(`{}`)},
		{"bad put json", HandlePutTerminalTab(fake), http.MethodPut, []byte(`{`)},
		{"put missing label", HandlePutTerminalTab(fake), http.MethodPut, []byte(`{"label":""}`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.handler.ServeHTTP(rec, terminalReq(tt.method, "/tabs/sess", tt.body, "WS", "sess"))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	label := "Label"
	notes := "Notes"
	order := 7
	pinned := false
	issue := "ISSUE-1"
	fields := buildPatchFields(tabPatchRequest{Label: &label, Notes: &notes, SortOrder: &order, Pinned: &pinned, IssueID: &issue})
	if fields["sort_order"] != "7" || fields["pinned"] != "false" || fields["issue_id"] != "ISSUE-1" {
		t.Fatalf("fields = %#v", fields)
	}
}

func terminalReq(method, target string, body []byte, workspace, session string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req = req.WithContext(middleware.WithWorkspace(context.Background(), workspace))
	if session != "" {
		req.SetPathValue("session", session)
	}
	return req
}

type fakeTerminalService struct {
	tab          *tabmeta.TabMetadata
	getSession   string
	patchFields  map[string]string
	putMeta      *tabmeta.TabMetadata
	deleted      string
	tokenWS      string
	tokenSession string
	tokenUser    string
	activeTab    string
	patchWS      string
	patchTab     string
	setupWS      string
	setupReq     service.TerminalSetupRequest
	err          error
}

func (f *fakeTerminalService) GenerateToken(_ context.Context, wsID, session, userID string) (string, error) {
	f.tokenWS, f.tokenSession, f.tokenUser = wsID, session, userID
	if f.err != nil {
		return "", f.err
	}
	return "token", nil
}
func (f *fakeTerminalService) ListTabs(context.Context, string) ([]tabmeta.TabMetadata, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []tabmeta.TabMetadata{*f.tab}, nil
}
func (f *fakeTerminalService) GetTab(_ context.Context, _ string, session string) (*tabmeta.TabMetadata, error) {
	f.getSession = session
	if f.err != nil {
		return nil, f.err
	}
	return f.tab, nil
}
func (f *fakeTerminalService) PatchTab(_ context.Context, _ string, _ string, fields map[string]string) (*service.PatchTabResult, error) {
	f.patchFields = fields
	if f.err != nil {
		return nil, f.err
	}
	tab := *f.tab
	if label := fields["label"]; label != "" {
		tab.Label = label
	}
	return &service.PatchTabResult{Tab: &tab}, nil
}
func (f *fakeTerminalService) PutTab(_ context.Context, _ string, meta *tabmeta.TabMetadata) error {
	f.putMeta = meta
	return f.err
}
func (f *fakeTerminalService) DeleteTab(_ context.Context, _ string, session string) error {
	f.deleted = session
	return f.err
}
func (f *fakeTerminalService) ListSessionsByIssue(context.Context) (map[string][]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return map[string][]string{"ISSUE-1": {"main", "review"}}, nil
}
func (f *fakeTerminalService) GetTerminalState(context.Context, string) (string, error) {
	return f.activeTab, f.err
}
func (f *fakeTerminalService) PatchTerminalState(_ context.Context, wsID, activeTab string) error {
	f.patchWS, f.patchTab = wsID, activeTab
	return f.err
}
func (f *fakeTerminalService) StartSetup(_ context.Context, wsID string, req service.TerminalSetupRequest) (*service.TerminalSetupResult, error) {
	f.setupWS, f.setupReq = wsID, req
	if f.err != nil {
		return nil, f.err
	}
	return &service.TerminalSetupResult{SessionName: "setup-codex", Backend: req.Backend, Action: req.Action, Command: "codex login", Created: true}, nil
}

func TestTabMetadataResponseShape(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleListTerminalTabs(&fakeTerminalService{tab: &tabmeta.TabMetadata{SessionName: "s", Label: "L", CreatedAt: time.Now()}}).ServeHTTP(rec, terminalReq(http.MethodGet, "/tabs", nil, "WS", ""))
	var body tabMetadataResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Success || body.Data == nil {
		t.Fatalf("body = %+v", body)
	}
}
