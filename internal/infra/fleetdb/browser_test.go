package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestBrowserClientSendsDelegationAndDecodes(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.EscapedPath())
		if r.Header.Get(BrowserDelegationHeader) != "jws-token" {
			t.Errorf("delegation header = %q", r.Header.Get(BrowserDelegationHeader))
		}
		if r.Header.Get("X-API-Key") != "k" {
			t.Errorf("service credential not sent")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/browsers":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "Docs" || body["request_id"] != "r1" || len(body) != 2 {
				t.Errorf("create body = %v", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"b1","workspace_key":"WS","owner_agent_id":"lead","created_by":"agent:lead","name":"Docs","desired_state":"running","status":"starting","request_id":"r1","selected":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/browsers":
			_, _ = w.Write([]byte(`null`))
		case r.URL.Path == "/api/v1/WS/browsers/b1":
			_, _ = w.Write([]byte(`{"id":"b1","status":"failed"}`))
		case r.URL.Path == "/api/v1/WS/browsers/b1/select":
			_, _ = w.Write([]byte(`{"id":"b1","selected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := c.Browsers()
	created, err := b.Create(ctx, "WS", "jws-token", domain.BrowserCreate{Name: "Docs", RequestID: "r1"})
	if err != nil || created.ID != "b1" || created.OwnerAgentID != "lead" || created.Status != domain.BrowserStatusStarting || !created.Selected {
		t.Fatalf("create = %+v %v", created, err)
	}
	list, err := b.List(ctx, "WS", "jws-token")
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("list = %#v %v (must be non-nil empty)", list, err)
	}
	got, err := b.Get(ctx, "WS", "jws-token", "b1")
	if err != nil || got.Status != domain.BrowserStatusFailed {
		t.Fatalf("get = %+v %v", got, err)
	}
	sel, err := b.Select(ctx, "WS", "jws-token", "b1")
	if err != nil || !sel.Selected {
		t.Fatalf("select = %+v %v", sel, err)
	}
	if len(gotPaths) != 4 {
		t.Fatalf("paths = %v", gotPaths)
	}
}

func TestBrowserClientRefusesMissingDelegation(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if _, err := c.Browsers().List(context.Background(), "WS", " "); !errors.Is(err, domain.ErrBrowserUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("request sent without a delegation")
	}
}

func TestBrowserClientErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, domain.ErrBrowserUnauthorized},
		{http.StatusForbidden, domain.ErrBrowserForbidden},
		{http.StatusConflict, domain.ErrBrowserRequestConflict},
		{http.StatusNotFound, domain.ErrNotFound},
		{http.StatusBadRequest, domain.ErrInvalid},
		{http.StatusServiceUnavailable, domain.ErrBrowserUnavailable},
		{http.StatusInternalServerError, domain.ErrBrowserUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"x","code":"y"}`))
		}))
		c, _ := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
		_, err := c.Browsers().Get(context.Background(), "WS", "d", "b1")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
		srv.Close()
	}
	// Transport failure.
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1"})
	if _, err := c.Browsers().List(context.Background(), "WS", "d"); !errors.Is(err, domain.ErrBrowserUnavailable) {
		t.Fatalf("transport err = %v", err)
	}
}
