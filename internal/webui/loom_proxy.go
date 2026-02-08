package webui

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
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

// safeDialContext returns a DialContext function that validates resolved IPs
// are not in private/reserved ranges (to prevent SSRF). If allowPrivate is
// true, the check is skipped entirely.
func safeDialContext(allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
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

const defaultLoomServerURL = "http://localhost:8081"

// newLoomProxy returns a reverse proxy for the loom API or nil if misconfigured.
// defaultURL, if non-empty, overrides the compiled-in default when LOOM_SERVER_URL
// is not set. This lets 'loom serve' pass the actual API port.
func newLoomProxy(defaultURL string) http.Handler {
	loomURL := strings.TrimSpace(os.Getenv("LOOM_SERVER_URL"))
	if loomURL == "" {
		if defaultURL != "" {
			loomURL = defaultURL
		} else {
			loomURL = defaultLoomServerURL
		}
	}

	target, err := url.Parse(loomURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		log.Printf("loom proxy disabled: invalid LOOM_SERVER_URL=%q", loomURL)
		return nil
	}

	// SECURITY: LOOM_PROXY_ALLOWED_HOSTS controls which non-localhost hosts the
	// proxy can forward to. Adding entries here carries SSRF risk — the proxy
	// could be used to reach internal services. By default, connections to
	// private/reserved IP ranges (10.x, 172.16.x, 192.168.x, link-local) are
	// blocked even for allowed hosts. Set LOOM_PROXY_ALLOW_PRIVATE_IPS=1 to
	// disable this protection if you explicitly need internal network access.
	allowedHosts := strings.Split(os.Getenv("LOOM_PROXY_ALLOWED_HOSTS"), ",")
	allowedHostsMap := make(map[string]bool)
	for _, h := range allowedHosts {
		h = strings.TrimSpace(h)
		if h != "" {
			allowedHostsMap[h] = true
		}
	}

	// SECURITY: Only allow proxying to localhost OR explicitly allowed hosts.
	if target.Scheme != "http" && target.Scheme != "https" {
		log.Printf("loom proxy disabled: invalid scheme %q (only http/https allowed)", target.Scheme)
		return nil
	}
	host := target.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" && !allowedHostsMap[host] {
		log.Printf("loom proxy disabled: host %q not allowed", host)
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	allowPrivateIPs := os.Getenv("LOOM_PROXY_ALLOW_PRIVATE_IPS") == "1"
	proxy.Transport = &http.Transport{
		DialContext:           safeDialContext(allowPrivateIPs),
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	originalDirector := proxy.Director
	debug := os.Getenv("LOOM_PROXY_DEBUG") == "1"

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if debug {
			log.Printf("loom proxy error %s %s: %v", r.Method, r.URL.String(), err)
		}
		w.WriteHeader(http.StatusBadGateway)
	}

	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api/loom")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.Host = target.Host
		if debug {
			log.Printf("loom proxy %s %s", req.Method, req.URL.String())
		}
	}

	return proxy
}
