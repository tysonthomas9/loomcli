package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeAuthRoundTripper struct {
	called bool
}

func (f *fakeAuthRoundTripper) Do(req *http.Request) (*http.Response, error) {
	f.called = true
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    req,
	}, nil
}

func TestAuthTransportDelegatesToClient(t *testing.T) {
	fake := &fakeAuthRoundTripper{}
	client := NewAuthHTTPClient(fake)
	req, err := http.NewRequest(http.MethodGet, "http://example.test/issues", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()
	if !fake.called {
		t.Fatal("wrapped client was not called")
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
}
