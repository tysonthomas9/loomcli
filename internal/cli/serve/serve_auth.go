package serve

import (
	"log"
	"log/slog"
	"net/url"
)

// validateAuthURL validates the --auth-url flag. It fatals on invalid URLs or
// insecure non-loopback HTTP without --auth-allow-insecure.
func validateAuthURL(authURL string, allowInsecure bool) {
	validateAuthEndpointURL("--auth-url", authURL, allowInsecure)
}

func validateAuthJWKSURL(jwksURL string, allowInsecure bool) {
	validateAuthEndpointURL("--auth-jwks-url", jwksURL, allowInsecure)
}

func validateAuthEndpointURL(flagName, rawURL string, allowInsecure bool) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		log.Fatalf("%s must be a valid http:// or https:// URL, got: %s", flagName, rawURL) //nolint:nologprintf
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if !isLoopback && !allowInsecure {
			log.Fatalf("%s uses http:// with non-loopback host %q. "+ //nolint:nologprintf
				"This allows MITM on JWKS. Use https:// or add --auth-allow-insecure.", flagName, host)
		}
		if isLoopback {
			slog.Warn(flagName+" uses http:// (loopback only) — use https:// in production", "host", host)
		} else {
			slog.Warn("--auth-allow-insecure is set — JWKS keys fetched over unencrypted HTTP", "host", host)
		}
	}
}
