package httpapi

import "net/http"

// authRoundTripper is an http.RoundTripper interface that mirrors the Do()
// signature used by internal/httpclient.Client. Any type that implements
// this interface can back the api.Backend's authenticated HTTP client.
//
// Defining the interface here (rather than importing httpclient) keeps the
// api package free of CLI-layer imports — essential for the depguard
// infra-isolation rule that forbids the Work Items Loom API adapter from importing
// internal/cli.
type authRoundTripper interface {
	Do(req *http.Request) (*http.Response, error)
}

// AuthTransport wraps a Do()-capable client (e.g., httpclient.Client) as an
// http.RoundTripper. This lets the api.Backend run on a standard http.Client
// while still getting the wrapped client's token management, device flow,
// and retry-on-401 behavior transparently.
//
// The wrapped client is responsible for setting the Authorization header
// and handling auth discovery.
type AuthTransport struct {
	Client authRoundTripper
}

// RoundTrip satisfies http.RoundTripper by delegating to the wrapped client.
func (t *AuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.Client.Do(req)
}

// NewAuthHTTPClient constructs an http.Client whose Transport delegates to
// the given Do-capable client. Pass in an httpclient.Client from
// internal/httpclient to get OIDC device-flow and token-cache behavior.
func NewAuthHTTPClient(client authRoundTripper) *http.Client {
	return &http.Client{
		Transport: &AuthTransport{Client: client},
	}
}
