package serve

import "testing"

// TestValidateAuthJWKSURL_NonFatalCases verifies that validateAuthJWKSURL
// accepts valid HTTPS URLs and HTTP loopback URLs without fataling. It also
// accepts HTTP non-loopback URLs when allowInsecure is true.
//
// Fatal cases (invalid URL, http non-loopback without allowInsecure) call
// log.Fatalf which in turn calls os.Exit and cannot be tested without a
// subprocess. The existing codebase does not use the subprocess pattern
// elsewhere (see `grep -r "TestMain\|os.Exit\|log.Fatalf"
// internal/cli/serve/ --include='*_test.go'`), so per the task instructions
// we only test non-fatal paths here rather than introducing a new pattern.
func TestValidateAuthJWKSURL_NonFatalCases(t *testing.T) {
	cases := []struct {
		name          string
		jwksURL       string
		allowInsecure bool
	}{
		{
			name:          "https URL",
			jwksURL:       "https://auth.example.com/jwks",
			allowInsecure: false,
		},
		{
			name:          "http loopback 127.0.0.1",
			jwksURL:       "http://127.0.0.1:3001/jwks",
			allowInsecure: false,
		},
		{
			name:          "http loopback localhost",
			jwksURL:       "http://localhost:3001/jwks",
			allowInsecure: false,
		},
		{
			name:          "http loopback ::1",
			jwksURL:       "http://[::1]:3001/jwks",
			allowInsecure: false,
		},
		{
			name:          "http non-loopback with allowInsecure",
			jwksURL:       "http://10.0.0.5/jwks",
			allowInsecure: true,
		},
		{
			name:          "https non-loopback with allowInsecure (still fine)",
			jwksURL:       "https://auth.example.com/jwks",
			allowInsecure: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("validateAuthJWKSURL panicked for %q (allowInsecure=%v): %v",
						tc.jwksURL, tc.allowInsecure, r)
				}
			}()
			validateAuthJWKSURL(tc.jwksURL, tc.allowInsecure)
		})
	}
}
