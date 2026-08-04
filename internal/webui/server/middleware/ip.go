package middleware

import (
	"net"
	"net/http"
)

// ExtractClientIP returns the client IP address from the request's RemoteAddr.
// X-Forwarded-For is NOT trusted (clients can spoof it to bypass rate limiting).
func ExtractClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
