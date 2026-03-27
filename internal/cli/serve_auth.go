package cli

import (
	"log"
	"log/slog"
	"net/url"
)

// validateAuthURL validates the --auth-url flag. It fatals on invalid URLs or
// insecure non-loopback HTTP without --auth-allow-insecure.
func validateAuthURL(authURL string, allowInsecure bool) {
	u, err := url.Parse(authURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		log.Fatalf("--auth-url must be a valid http:// or https:// URL, got: %s", authURL) //nolint:nologprintf
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if !isLoopback && !allowInsecure {
			log.Fatalf("--auth-url uses http:// with non-loopback host %q. "+ //nolint:nologprintf
				"This allows MITM on JWKS. Use https:// or add --auth-allow-insecure.", host)
		}
		if isLoopback {
			slog.Warn("--auth-url uses http:// (loopback only) — use https:// in production", "host", host)
		} else {
			slog.Warn("--auth-allow-insecure is set — JWKS keys fetched over unencrypted HTTP", "host", host)
		}
	}
}
