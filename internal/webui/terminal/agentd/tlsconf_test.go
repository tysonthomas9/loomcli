package agentd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// makeSelfSigned generates a fresh ECDSA P-256 keypair and returns a
// self-signed cert + key as PEM strings. Used as the "minted client cert"
// in tlsconf tests; also doubles as the agentd-CA-root PEM since the cert
// is its own issuer.
func makeSelfSigned(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return
}

func TestTLSConfigFromPEM_RoundTrip(t *testing.T) {
	certPEM, keyPEM := makeSelfSigned(t, "agent.demo.main")
	caPEM := []byte(certPEM) // self-signed: cert is its own root

	cfg, err := tlsConfigFromPEM(certPEM, keyPEM, caPEM, "demo", "main")
	if err != nil {
		t.Fatalf("tlsConfigFromPEM: %v", err)
	}
	if cfg == nil {
		t.Fatalf("tlsConfigFromPEM returned nil cfg")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %x, want TLS13 (%x)", cfg.MinVersion, tls.VersionTLS13)
	}
	if got, want := cfg.ServerName, "agent.demo.main"; got != want {
		t.Errorf("ServerName = %q, want %q", got, want)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates len = %d, want 1", len(cfg.Certificates))
	}
	if cfg.RootCAs == nil {
		t.Errorf("RootCAs = nil, want populated pool")
	}
	// Sanity: the parsed leaf must carry exactly one DER cert that we can
	// re-parse — guards against accidentally storing the chain in the
	// wrong field.
	if len(cfg.Certificates[0].Certificate) != 1 {
		t.Fatalf("Certificates[0].Certificate len = %d, want 1", len(cfg.Certificates[0].Certificate))
	}
	if _, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0]); err != nil {
		t.Errorf("re-parse leaf cert: %v", err)
	}
}

func TestTLSConfigFromPEM_RejectsEmptyInputs(t *testing.T) {
	certPEM, keyPEM := makeSelfSigned(t, "agent.demo.main")
	caPEM := []byte(certPEM)

	cases := []struct {
		name      string
		cert, key string
		ca        []byte
		ws, agent string
	}{
		{"empty cert", "", keyPEM, caPEM, "ws", "a"},
		{"empty key", certPEM, "", caPEM, "ws", "a"},
		{"empty ca", certPEM, keyPEM, nil, "ws", "a"},
		{"empty workspace", certPEM, keyPEM, caPEM, "", "a"},
		{"empty agent", certPEM, keyPEM, caPEM, "ws", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tlsConfigFromPEM(tc.cert, tc.key, tc.ca, tc.ws, tc.agent); err == nil {
				t.Errorf("%s: tlsConfigFromPEM = nil err, want error", tc.name)
			}
		})
	}
}

func TestTLSConfigFromPEM_RejectsMalformedCert(t *testing.T) {
	_, err := tlsConfigFromPEM("not-a-pem", "not-a-key", []byte("ca"), "ws", "a")
	if err == nil {
		t.Fatalf("tlsConfigFromPEM with malformed cert returned nil err")
	}
}

func TestTLSConfigFromPEM_RejectsMalformedCA(t *testing.T) {
	certPEM, keyPEM := makeSelfSigned(t, "agent.demo.main")
	_, err := tlsConfigFromPEM(certPEM, keyPEM, []byte("not-a-ca-pem"), "ws", "a")
	if err == nil {
		t.Fatalf("tlsConfigFromPEM with malformed CA returned nil err")
	}
	if !strings.Contains(err.Error(), "caRootPEM") {
		t.Errorf("error = %v, want one mentioning caRootPEM", err)
	}
}

func TestServerNameForAgent(t *testing.T) {
	if got, want := serverNameForAgent("ws", "a"), "agent.ws.a"; got != want {
		t.Errorf("serverNameForAgent = %q, want %q", got, want)
	}
}
