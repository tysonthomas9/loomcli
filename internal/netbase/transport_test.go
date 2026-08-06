package netbase

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var errDialStopped = errors.New("dial stopped")

func TestForceIPv4DialContextRewritesGenericTCP(t *testing.T) {
	if disableForceIPv4 {
		t.Skip("LOOM_DISABLE_FORCE_IPV4 disables tcp4 rewrites")
	}

	var got []string
	dial := forceIPv4DialContext(func(_ context.Context, network, _ string) (net.Conn, error) {
		got = append(got, network)
		return nil, errDialStopped
	})

	cases := []struct {
		name string
		want string
	}{
		{name: "tcp", want: "tcp4"},
		{name: "tcp4", want: "tcp4"},
		{name: "tcp6", want: "tcp6"},
		{name: "unix", want: "unix"},
	}

	for _, tc := range cases {
		_, err := dial(context.Background(), tc.name, "example.test:80")
		if !errors.Is(err, errDialStopped) {
			t.Fatalf("dial(%q) error = %v, want %v", tc.name, err, errDialStopped)
		}
	}

	if len(got) != len(cases) {
		t.Fatalf("got %d dials, want %d", len(got), len(cases))
	}
	for i, tc := range cases {
		if got[i] != tc.want {
			t.Fatalf("dial %d network = %q, want %q", i, got[i], tc.want)
		}
	}
}

func TestForceIPv4NetworkPreservesIPv6Literal(t *testing.T) {
	if disableForceIPv4 {
		t.Skip("LOOM_DISABLE_FORCE_IPV4 disables tcp4 rewrites")
	}

	for _, addr := range []string{"[::1]:443", "[2001:db8::1]:443", "[fe80::1%lo0]:443"} {
		if got := forceIPv4Network("tcp", addr); got != "tcp" {
			t.Fatalf("forceIPv4Network(%q) = %q, want tcp", addr, got)
		}
	}
}

func TestForceIPv4KillSwitch(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestNetbaseIPv4Helper") //nolint:norawexec
	cmd.Env = ipv4HelperEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ipv4 helper failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "tcp" {
		t.Fatalf("network = %q, want tcp", strings.TrimSpace(string(out)))
	}
}

func TestNetbaseIPv4Helper(t *testing.T) {
	if os.Getenv("NETBASE_IPV4_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(forceIPv4Network("tcp", "example.test:443"))
	os.Exit(0)
}

func TestCloneEnablesHTTP2ForDefaultLikeTransport(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	base := &http.Transport{
		MaxIdleConnsPerHost:   128,
		MaxIdleConns:          256,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	got := Clone(base)

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if got.TLSClientConfig != nil {
		tlsConfig = got.TLSClientConfig.Clone()
	}
	tlsConfig.RootCAs = roots
	got.TLSClientConfig = tlsConfig

	resp, err := (&http.Client{Transport: got}).Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Proto != "HTTP/2.0" {
		t.Fatalf("resp.Proto = %q, want HTTP/2.0", resp.Proto)
	}
	if got.TLSNextProto == nil || got.TLSNextProto["h2"] == nil {
		t.Fatal("Clone did not install HTTP/2 TLSNextProto handler")
	}
	t.Logf("resp.Proto=%s TLSNextProto[h2]=%t", resp.Proto, got.TLSNextProto["h2"] != nil)
}

func TestCloneDoesNotMutateBase(t *testing.T) {
	base := &http.Transport{}
	got := Clone(base)
	if got == base {
		t.Fatal("Clone returned base transport")
	}
	if base.Proxy != nil {
		t.Fatal("Clone mutated base.Proxy")
	}
	if base.DialContext != nil {
		t.Fatal("Clone mutated base.DialContext")
	}
	if base.ForceAttemptHTTP2 {
		t.Fatal("Clone mutated base.ForceAttemptHTTP2")
	}
	if base.TLSClientConfig != nil {
		t.Fatal("Clone mutated base.TLSClientConfig")
	}
	if base.TLSNextProto != nil {
		t.Fatal("Clone mutated base.TLSNextProto")
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	base = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   tlsConfig,
	}
	_ = Clone(base)
	if len(tlsConfig.NextProtos) != 0 {
		t.Fatalf("Clone mutated base TLS NextProtos: %v", tlsConfig.NextProtos)
	}
}

func TestClonePreservesTransportKnobs(t *testing.T) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	base := &http.Transport{
		MaxIdleConnsPerHost:    128,
		MaxConnsPerHost:        64,
		MaxIdleConns:           256,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 16384,
		ForceAttemptHTTP2:      true,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		ReadBufferSize:         4096,
		WriteBufferSize:        8192,
		TLSClientConfig:        tlsConfig,
	}

	got := Clone(base)
	if got == base {
		t.Fatal("Clone returned base transport")
	}
	if got.MaxIdleConnsPerHost != base.MaxIdleConnsPerHost ||
		got.MaxConnsPerHost != base.MaxConnsPerHost ||
		got.MaxIdleConns != base.MaxIdleConns ||
		got.IdleConnTimeout != base.IdleConnTimeout ||
		got.TLSHandshakeTimeout != base.TLSHandshakeTimeout ||
		got.ExpectContinueTimeout != base.ExpectContinueTimeout ||
		got.ResponseHeaderTimeout != base.ResponseHeaderTimeout ||
		got.MaxResponseHeaderBytes != base.MaxResponseHeaderBytes ||
		got.ForceAttemptHTTP2 != base.ForceAttemptHTTP2 ||
		got.DisableKeepAlives != base.DisableKeepAlives ||
		got.DisableCompression != base.DisableCompression ||
		got.ReadBufferSize != base.ReadBufferSize ||
		got.WriteBufferSize != base.WriteBufferSize {
		t.Fatal("Clone did not preserve transport knobs")
	}
	if got.TLSClientConfig == nil || got.TLSClientConfig.MinVersion != tlsConfig.MinVersion {
		t.Fatal("Clone did not preserve TLS config")
	}
	if got.Proxy == nil {
		t.Fatal("Clone did not set Proxy")
	}
	if got.DialContext == nil {
		t.Fatal("Clone did not set DialContext")
	}
}

func TestTransportProxyFromEnvironment(t *testing.T) {
	cases := []struct {
		name   string
		target string
		proxy  string
		want   string
	}{
		{name: "set", target: "http://sandbox.example/resource", proxy: "http://127.0.0.1:18080", want: "http://127.0.0.1:18080"},
		{name: "unset", target: "http://sandbox.example/resource", want: "<nil>"},
		{name: "ipv4 loopback", target: "http://127.0.0.1/resource", proxy: "http://127.0.0.1:18080", want: "<nil>"},
		{name: "localhost", target: "http://localhost/resource", proxy: "http://127.0.0.1:18080", want: "<nil>"},
		{name: "ipv6 loopback", target: "http://[::1]/resource", proxy: "http://127.0.0.1:18080", want: "<nil>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestNetbaseProxyHelper") //nolint:norawexec
			cmd.Env = proxyHelperEnv(tc.proxy, tc.target)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("proxy helper failed: %v\n%s", err, out)
			}
			if strings.TrimSpace(string(out)) != tc.want {
				t.Fatalf("proxy = %q, want %q", strings.TrimSpace(string(out)), tc.want)
			}
		})
	}
}

func TestNetbaseProxyHelper(t *testing.T) {
	if os.Getenv("NETBASE_PROXY_HELPER") != "1" {
		return
	}
	target := os.Getenv("NETBASE_PROXY_TARGET")
	if target == "" {
		target = "http://sandbox.example/resource"
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	proxyURL, err := Clone(nil).Proxy(req)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if proxyURL == nil {
		_, _ = os.Stdout.WriteString("<nil>")
		os.Exit(0)
	}
	_, _ = os.Stdout.WriteString(proxyURL.String())
	os.Exit(0)
}

func proxyHelperEnv(proxy, target string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
			"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
			"NETBASE_PROXY_HELPER", "NETBASE_PROXY_TARGET":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "NETBASE_PROXY_HELPER=1")
	env = append(env, "NETBASE_PROXY_TARGET="+target)
	if proxy != "" {
		env = append(env, "HTTP_PROXY="+proxy)
	}
	return env
}

func ipv4HelperEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "LOOM_DISABLE_FORCE_IPV4", "NETBASE_IPV4_HELPER":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "LOOM_DISABLE_FORCE_IPV4=1", "NETBASE_IPV4_HELPER=1")
	return env
}
