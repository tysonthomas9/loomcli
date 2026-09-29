package bootstrap

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
)

func TestEmbeddedFleetDBVerifiesLocalBrowserKey(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := appendBrowserDelegationVerifierEnv([]string{"PATH=/bin"}, dir, logger)
	kp, err := browserauth.LoadOrCreateLocalKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range kp.FleetVerifierEnv() {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "seed") {
		t.Fatal("private key material leaked into fleet-db env")
	}
}

func TestEmbeddedFleetDBExplicitVerifierWins(t *testing.T) {
	dir := t.TempDir()
	in := []string{browserauth.EnvFleetPublicKeys + "=ops=AAAA"}
	env := appendBrowserDelegationVerifierEnv(in, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(env) != 1 {
		t.Fatalf("explicit verifier overridden: %v", env)
	}
}
