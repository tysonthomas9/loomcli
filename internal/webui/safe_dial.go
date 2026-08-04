package webui

import (
	"context"
	"fmt"
	"net"
	"time"
)

// isPrivateIP reports whether ip is in a private, reserved, or otherwise
// non-routable range (excluding loopback, which is always allowed).
func isPrivateIP(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// Broadcast (255.255.255.255).
	if ip4 := ip.To4(); ip4 != nil &&
		ip4[0] == 255 && ip4[1] == 255 && ip4[2] == 255 && ip4[3] == 255 {
		return true
	}
	return false
}

// SafeDialContext returns a DialContext function that validates resolved IPs
// are not in private/reserved ranges (to prevent SSRF). If allowPrivate is
// true, the check is skipped entirely.
func SafeDialContext(allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if allowPrivate {
		return dialer.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address: %w", err)
		}
		// Allow loopback without DNS check.
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return dialer.DialContext(ctx, network, addr)
		}
		// Resolve and check all IPs.
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS resolution failed for %s: %w", host, err)
		}
		for _, ipAddr := range ips {
			if ipAddr.IP.IsLoopback() {
				continue
			}
			if isPrivateIP(ipAddr.IP) {
				return nil, fmt.Errorf("blocked: %s resolves to private IP %s", host, ipAddr.IP)
			}
		}
		// Connect to the resolved IP directly to prevent TOCTOU/DNS rebinding.
		if len(ips) > 0 {
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		}
		return dialer.DialContext(ctx, network, addr)
	}
}
