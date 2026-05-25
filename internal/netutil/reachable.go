package netutil

import (
	"net"
	"net/url"
	"time"
)

// DialReachable reports whether something is accepting TCP connections at
// the host:port of rawURL. It is used to tell a recycled PID (URL dead,
// nothing listening) apart from a present-but-unhealthy server (URL
// reachable, /healthz failing): the former is safe to evict and respawn,
// the latter must not be trampled.
func DialReachable(rawURL string, timeout time.Duration) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
