package modbuilder

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type credentialSeedInvalidatorSpy struct {
	calls int
}

func (s *credentialSeedInvalidatorSpy) InvalidateCredentialSeeds() {
	s.calls++
}

func TestNewLocalSettingsHandlersWiresCredentialInvalidator(t *testing.T) {
	invalidator := &credentialSeedInvalidatorSpy{}
	handlers := NewLocalSettingsHandlers(t.TempDir(), invalidator)
	if handlers.RuntimeCredentialPreflight == nil {
		t.Fatal("runtime credential preflight handler was not wired")
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(
		`{"runtime_credentials":{"github":{"token":"gh-new"}}}`,
	))
	rec := httptest.NewRecorder()

	handlers.Patch.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if invalidator.calls != 1 {
		t.Fatalf("invalidation calls = %d, want 1", invalidator.calls)
	}
}
