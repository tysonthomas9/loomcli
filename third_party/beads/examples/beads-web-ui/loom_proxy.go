package main

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultLoomServerURL = "http://localhost:9000"

// newLoomProxy returns a reverse proxy for the loom API or nil if misconfigured.
func newLoomProxy() http.Handler {
	loomURL := strings.TrimSpace(os.Getenv("LOOM_SERVER_URL"))
	if loomURL == "" {
		loomURL = defaultLoomServerURL
	}

	target, err := url.Parse(loomURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		log.Printf("loom proxy disabled: invalid LOOM_SERVER_URL=%q", loomURL)
		return nil
	}

	// Parse allowed hosts from env (comma-separated)
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
	proxy.Transport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
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
