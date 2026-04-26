package agentd

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
)

// tlsConfigFromPEM builds a *tls.Config that webui can present to a
// loom-agentd over mTLS. certPEM + keyPEM are the short-lived client cert
// minted by the control-plane (see loom-control-plane/internal/certs.Mint —
// 2-minute lifetime, ECDSA P-256, CN = "{workspace}/{agent}"). caRootPEM is
// the CA that signs agentd's *server* cert; loaded once at AgentdClient
// construction time and shared across every per-session tls.Config built
// here.
//
// The CN convention agentd presents is "agent:{workspace}/{agent}" — see
// loom-control-plane's mint helper and the agentd certcheck reference
// noted there. We pin ServerName accordingly so the TLS stack rejects a
// cert minted for a different (workspace, agent) pair, even if it's signed
// by the same CA.
//
// Returns a fully-populated config with TLS 1.3 minimum, mutual auth, the
// caller's cert as the only client identity, and an empty session cache —
// AttachSession produces a new config per attach so caching across attaches
// would only mask state-confused tests.
func tlsConfigFromPEM(certPEM, keyPEM string, caRootPEM []byte, workspace, agent string) (*tls.Config, error) {
	if certPEM == "" {
		return nil, errors.New("agentd: tlsConfigFromPEM: certPEM is empty")
	}
	if keyPEM == "" {
		return nil, errors.New("agentd: tlsConfigFromPEM: keyPEM is empty")
	}
	if len(caRootPEM) == 0 {
		return nil, errors.New("agentd: tlsConfigFromPEM: caRootPEM is empty")
	}
	if workspace == "" {
		return nil, errors.New("agentd: tlsConfigFromPEM: workspace is empty")
	}
	if agent == "" {
		return nil, errors.New("agentd: tlsConfigFromPEM: agent is empty")
	}

	clientCert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("agentd: parse client cert+key: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caRootPEM) {
		return nil, errors.New("agentd: caRootPEM did not contain any usable certificates")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
		ServerName:   serverNameForAgent(workspace, agent),
	}, nil
}

// serverNameForAgent returns the SNI / verified-server-name string the
// agentd's server cert is expected to present. Matches the
// "agent:{workspace}/{agent}" convention the control-plane mints — keeping
// this in a single helper means every consumer of the cert (AttachSession,
// tests, future Phase 3 stream attach) renders the same canonical string.
func serverNameForAgent(workspace, agent string) string {
	return "agent:" + workspace + "/" + agent
}
