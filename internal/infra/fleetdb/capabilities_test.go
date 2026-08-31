package fleetdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func newCapabilityTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestCapabilitiesGetDecodesDocument(t *testing.T) {
	c, _ := newCapabilityTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/capabilities" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_version":1,"commit":"adca220cdce0","capabilities":["agents","issues"]}`))
	})

	doc, err := c.Capabilities().Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc.APIVersion != 1 || doc.Commit != "adca220cdce0" {
		t.Fatalf("unexpected identity: %+v", doc)
	}
	if !doc.Has("issues") || doc.Has("skills") {
		t.Fatalf("unexpected capability set: %+v", doc.Capabilities)
	}
}

func TestCapabilitiesGetEmptyListIsNotAnError(t *testing.T) {
	// A server that serves the route but registered nothing loom needs is a
	// legitimate answer — distinct from a body that failed to decode.
	c, _ := newCapabilityTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"api_version":1,"commit":"abc","capabilities":[]}`))
	})
	doc, err := c.Capabilities().Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(doc.Capabilities) != 0 {
		t.Fatalf("expected empty capability list, got %v", doc.Capabilities)
	}
}

func TestCapabilitiesGetBareNotFoundIsUnsupported(t *testing.T) {
	c, _ := newCapabilityTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	_, err := c.Capabilities().Get(context.Background())
	if !errors.Is(err, domain.ErrCapabilityEndpointUnsupported) {
		t.Fatalf("expected ErrCapabilityEndpointUnsupported, got %v", err)
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an absent capability endpoint must not read as a missing resource: %v", err)
	}
}

func TestCapabilitiesGetMethodNotAllowedIsUnsupported(t *testing.T) {
	c, _ := newCapabilityTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	_, err := c.Capabilities().Get(context.Background())
	if !errors.Is(err, domain.ErrCapabilityEndpointUnsupported) {
		t.Fatalf("expected ErrCapabilityEndpointUnsupported, got %v", err)
	}
}

func TestCapabilitiesGetNotFoundWithEnvelopeIsNotUnsupported(t *testing.T) {
	// A 404 from a real handler is a real error; only an unrouted path means
	// the server predates capability reporting.
	c, _ := newCapabilityTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"workspace not found"}}`))
	})
	_, err := c.Capabilities().Get(context.Background())
	if errors.Is(err, domain.ErrCapabilityEndpointUnsupported) {
		t.Fatalf("enveloped 404 must not read as an unsupported endpoint: %v", err)
	}
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestCapabilitiesGetServerErrorIsTransport(t *testing.T) {
	c, _ := newCapabilityTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.Capabilities().Get(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, domain.ErrCapabilityEndpointUnsupported) {
		t.Fatalf("a 5xx must not read as an unsupported endpoint: %v", err)
	}
}

func TestCapabilitiesGetDialFailureIsTransport(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	c, err := New(Config{BaseURL: url})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Capabilities().Get(context.Background()); err == nil {
		t.Fatal("expected a dial error")
	} else if errors.Is(err, domain.ErrCapabilityEndpointUnsupported) {
		t.Fatalf("a dial failure must not read as an unsupported endpoint: %v", err)
	}
}

func TestCapabilitiesGetMalformedBodyIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"truncated":      `{"api_version":1,`,
		"not json":       `<html>gateway</html>`,
		"empty":          ``,
		"no field":       `{"api_version":1,"commit":"abc"}`,
		"wrong document": `{"status":"ok"}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newCapabilityTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			if doc, err := c.Capabilities().Get(context.Background()); err == nil {
				t.Fatalf("expected an error, got %+v", doc)
			}
		})
	}
}
