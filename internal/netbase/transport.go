package netbase

import (
	"context"
	"maps"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

var (
	transportOnce sync.Once
	transport     *http.Transport

	disableForceIPv4 = os.Getenv("LOOM_DISABLE_FORCE_IPV4") == "1"
)

// Transport returns Loom's shared default HTTP transport.
func Transport() *http.Transport {
	transportOnce.Do(func() {
		transport = Clone(nil)
	})
	return transport
}

// Clone copies base without mutating it and applies Loom's sandbox egress
// defaults.
func Clone(base *http.Transport) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	// http.Transport.Clone initializes protocol defaults on its receiver.
	clone := cloneBaseTransport(base).Clone()
	clone.Proxy = http.ProxyFromEnvironment
	// Go disables automatic HTTP/2 when a Transport has a custom DialContext
	// unless ForceAttemptHTTP2 is true.
	clone.ForceAttemptHTTP2 = true
	clone.DialContext = forceIPv4DialContext(base.DialContext)
	return clone
}

func cloneBaseTransport(base *http.Transport) *http.Transport {
	clone := &http.Transport{
		Proxy:                  base.Proxy,
		OnProxyConnectResponse: base.OnProxyConnectResponse,
		DialContext:            base.DialContext,
		// Dial and DialTLS are deprecated, but a faithful copy must carry them:
		// dropping a field a caller set is the class of bug this copy exists to
		// avoid, and http.Transport still honors them when the Context variants
		// are unset.
		Dial:                   base.Dial,    //nolint:staticcheck // faithful copy of a caller-set field
		DialTLS:                base.DialTLS, //nolint:staticcheck // faithful copy of a caller-set field
		DialTLSContext:         base.DialTLSContext,
		TLSHandshakeTimeout:    base.TLSHandshakeTimeout,
		DisableKeepAlives:      base.DisableKeepAlives,
		DisableCompression:     base.DisableCompression,
		MaxIdleConns:           base.MaxIdleConns,
		MaxIdleConnsPerHost:    base.MaxIdleConnsPerHost,
		MaxConnsPerHost:        base.MaxConnsPerHost,
		IdleConnTimeout:        base.IdleConnTimeout,
		ResponseHeaderTimeout:  base.ResponseHeaderTimeout,
		ExpectContinueTimeout:  base.ExpectContinueTimeout,
		ProxyConnectHeader:     base.ProxyConnectHeader.Clone(),
		GetProxyConnectHeader:  base.GetProxyConnectHeader,
		MaxResponseHeaderBytes: base.MaxResponseHeaderBytes,
		WriteBufferSize:        base.WriteBufferSize,
		ReadBufferSize:         base.ReadBufferSize,
		ForceAttemptHTTP2:      base.ForceAttemptHTTP2,
	}
	if base.TLSClientConfig != nil {
		clone.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	if base.TLSNextProto != nil {
		clone.TLSNextProto = maps.Clone(base.TLSNextProto)
	}
	if base.HTTP2 != nil {
		clone.HTTP2 = &http.HTTP2Config{}
		*clone.HTTP2 = *base.HTTP2
	}
	if base.Protocols != nil {
		clone.Protocols = &http.Protocols{}
		*clone.Protocols = *base.Protocols
	}
	return clone
}

func forceIPv4DialContext(dial dialContextFunc) dialContextFunc {
	if dial == nil {
		// http.Transport falls back to a zero-value net.Dialer when DialContext
		// is nil, which imposes no dial deadline at all. Substituting
		// http.DefaultTransport's values bounds it instead.
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dial = dialer.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dial(ctx, forceIPv4Network(network, addr), addr)
	}
}

func forceIPv4Network(network, addr string) string {
	if network != "tcp" || disableForceIPv4 {
		return network
	}
	if host, _, err := net.SplitHostPort(addr); err == nil && isIPv6Literal(host) {
		return network
	}
	// Cloud sandboxes advertise IPv6 but do not provide IPv6 egress.
	return "tcp4"
}

func isIPv6Literal(host string) bool {
	if before, _, ok := strings.Cut(host, "%"); ok {
		host = before
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}
