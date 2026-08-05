package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type brokerFake struct {
	launchBearer    string
	launchWorkspace string
	exchangeCode    string
	exchangeWS      string
	launchErr       error
	exchangeErr     error
}

func (f *brokerFake) MintLaunchCode(bearer, workspace string) (authority.LocalBrowserLaunch, error) {
	f.launchBearer, f.launchWorkspace = bearer, workspace
	return authority.LocalBrowserLaunch{Code: "launch", Workspace: workspace, ExpiresAt: time.Unix(1, 0)}, f.launchErr
}

func (f *brokerFake) ExchangeLaunchCode(code, workspace string) (authority.LocalBrowserSession, error) {
	f.exchangeCode, f.exchangeWS = code, workspace
	return authority.LocalBrowserSession{Bearer: "session", Workspace: workspace, ExpiresAt: time.Unix(2, 0)}, f.exchangeErr
}

func TestLocalBrowserSessionHTTPUsesCanonicalWorkspaceAndNoStore(t *testing.T) {
	broker := &brokerFake{}
	mux := http.NewServeMux()
	New(broker, func(context.Context) string { return "workspace-id" }).Register(mux)

	launchReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/alias/operator-sessions/launch", nil)
	launchReq.Header.Set("Authorization", "Bearer durable")
	launchRec := httptest.NewRecorder()
	mux.ServeHTTP(launchRec, launchReq)
	if launchRec.Code != http.StatusCreated {
		t.Fatalf("launch status = %d body=%s", launchRec.Code, launchRec.Body.String())
	}
	if broker.launchBearer != "Bearer durable" || broker.launchWorkspace != "workspace-id" {
		t.Fatalf("launch inputs = %q %q", broker.launchBearer, broker.launchWorkspace)
	}
	if launchRec.Header().Get("Cache-Control") != "no-store" || launchRec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("sensitive headers = %v", launchRec.Header())
	}

	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/alias/operator-sessions/exchange", strings.NewReader(`{"launch_code":"launch"}`))
	exchangeRec := httptest.NewRecorder()
	mux.ServeHTTP(exchangeRec, exchangeReq)
	if exchangeRec.Code != http.StatusOK {
		t.Fatalf("exchange status = %d body=%s", exchangeRec.Code, exchangeRec.Body.String())
	}
	if broker.exchangeCode != "launch" || broker.exchangeWS != "workspace-id" {
		t.Fatalf("exchange inputs = %q %q", broker.exchangeCode, broker.exchangeWS)
	}
	var payload map[string]string
	if err := json.Unmarshal(exchangeRec.Body.Bytes(), &payload); err != nil || payload["access_token"] != "session" {
		t.Fatalf("exchange payload = %v err=%v", payload, err)
	}
}

func TestLocalBrowserSessionHTTPFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		broker   *brokerFake
		resolver func(context.Context) string
		method   string
		path     string
		body     string
		auth     string
		want     int
	}{
		{name: "missing durable bearer", broker: &brokerFake{}, resolver: func(context.Context) string { return "ws" }, method: http.MethodPost, path: "/api/workspaces/ws/operator-sessions/launch", want: http.StatusUnauthorized},
		{name: "invalid durable bearer", broker: &brokerFake{launchErr: authority.ErrInvalidOperatorToken}, resolver: func(context.Context) string { return "ws" }, method: http.MethodPost, path: "/api/workspaces/ws/operator-sessions/launch", auth: "Bearer bad", want: http.StatusUnauthorized},
		{name: "missing canonical workspace", broker: &brokerFake{}, resolver: func(context.Context) string { return "" }, method: http.MethodPost, path: "/api/workspaces/ws/operator-sessions/launch", auth: "Bearer durable", want: http.StatusBadRequest},
		{name: "malformed exchange", broker: &brokerFake{}, resolver: func(context.Context) string { return "ws" }, method: http.MethodPost, path: "/api/workspaces/ws/operator-sessions/exchange", body: `{"unknown":true}`, want: http.StatusBadRequest},
		{name: "replayed exchange", broker: &brokerFake{exchangeErr: authority.ErrInvalidOperatorToken}, resolver: func(context.Context) string { return "ws" }, method: http.MethodPost, path: "/api/workspaces/ws/operator-sessions/exchange", body: `{"launch_code":"used"}`, want: http.StatusUnauthorized},
		{name: "wrong workspace", broker: &brokerFake{exchangeErr: authority.ErrWorkspaceMismatch}, resolver: func(context.Context) string { return "ws" }, method: http.MethodPost, path: "/api/workspaces/ws/operator-sessions/exchange", body: `{"launch_code":"launch"}`, want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			New(tt.broker, tt.resolver).Register(mux)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d body=%s want=%d", rec.Code, rec.Body.String(), tt.want)
			}
			if strings.Contains(rec.Body.String(), "durable") || strings.Contains(rec.Body.String(), "used") {
				t.Fatalf("error response leaked credential: %s", rec.Body.String())
			}
		})
	}
}

func TestLocalBrowserSessionHTTPMapsInternalErrorsWithoutDetail(t *testing.T) {
	broker := &brokerFake{launchErr: errors.New("secret backend detail")}
	mux := http.NewServeMux()
	New(broker, func(context.Context) string { return "ws" }).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/operator-sessions/launch", nil)
	req.Header.Set("Authorization", "Bearer durable")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
