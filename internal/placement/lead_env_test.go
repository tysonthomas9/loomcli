package placement

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// TestOccupantTokenNeverReachesCreateEnv is the credential-exposure fix. Create
// env is DURABLE provider-side state -- the Daytona adapter sends it to the
// sandbox-create API, which stores it on the sandbox and serves it back from
// the API and console for the sandbox's whole lifetime. Process env lives only
// as long as the PTY. A bearer token in the first is a credential sitting in
// third-party metadata long after the process that needed it is gone.
func TestOccupantTokenNeverReachesCreateEnv(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	if _, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4)); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	create := provider.createCall(t, 0)
	if _, present := create.Env[OccupantTokenEnv]; present {
		t.Fatalf("create env carries %s; it must only reach the PTY process", OccupantTokenEnv)
	}
	// Every secret key, not just the one we happen to name above.
	for _, key := range leadSecretEnvKeys {
		if _, present := create.Env[key]; present {
			t.Errorf("create env carries secret key %q", key)
		}
	}
	// Non-secret placement identity must still be there: the split must not
	// have thrown out the env the sandbox legitimately needs.
	for _, key := range []string{"LOOM_WORKSPACE", "LOOM_AGENT_NAME", "LOOM_LEAD_PLACEMENT_ID"} {
		if create.Env[key] == "" {
			t.Errorf("create env lost %q", key)
		}
	}

	// The process still gets a usable token, or the lead cannot authenticate.
	spec := provider.startProcessCall(t, 0)
	if strings.TrimSpace(spec.Env[OccupantTokenEnv]) == "" {
		t.Fatal("process env lost the occupant token; the lead could not authenticate")
	}
	if claims := parseProcessToken(t, spec); claims.WorkspaceKey != "WS" {
		t.Fatalf("process token workspace = %q, want WS", claims.WorkspaceKey)
	}
}

// TestProvisionRejectsCallerSuppliedReservedEnv pins reject-not-overwrite.
// Overwriting is the wrong failure: the caller believes their value took
// effect, and the safety of the scheme then depends on the overwrite list never
// drifting from the reserved list. Rejecting makes them impossible to disagree.
func TestProvisionRejectsCallerSuppliedReservedEnv(t *testing.T) {
	ctx := context.Background()
	for _, key := range reservedLeadEnvKeys {
		t.Run(key, func(t *testing.T) {
			for _, where := range []string{"env", "process env"} {
				st := memstore.New()
				provider := &fakeProvider{}
				broker := mustBroker(t, st, provider)
				req := testProvisionRequest("nova", 2, 4)
				if where == "env" {
					req.Env[key] = "attacker-supplied"
				} else {
					req.Process.Env = map[string]string{key: "attacker-supplied"}
				}

				_, err := broker.Provision(ctx, req)
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("%s %s: Provision err = %v, want ErrInvalid", where, key, err)
				}
				if got := provider.createCallCount(); got != 0 {
					t.Fatalf("%s %s: provider saw %d Create call(s) after a rejected request", where, key, got)
				}
			}
		})
	}
}

// TestProvisionAllowsUnreservedEnv is the inverse guard: rejecting reserved
// keys must not reject ordinary caller env.
func TestProvisionAllowsUnreservedEnv(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	req := testProvisionRequest("nova", 2, 4)
	req.Env["CUSTOM_SETTING"] = "value"
	if _, err := broker.Provision(ctx, req); err != nil {
		t.Fatalf("Provision with ordinary env: %v", err)
	}
	if got := provider.createCall(t, 0).Env["CUSTOM_SETTING"]; got != "value" {
		t.Fatalf("caller env CUSTOM_SETTING = %q, want value", got)
	}
}
