package daytona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// TestCreateOutcomeClassification pins the fail-closed outcome mapping: only a
// provably-local failure may claim NotDispatched, because the broker releases
// the placement on that claim and a wrong claim recreates the billing leak.
func TestCreateOutcomeClassification(t *testing.T) {
	t.Run("payload validation failure is not dispatched", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			t.Fatalf("request %s %s dispatched, want local failure", req.Method, req.URL.Path)
			return nil, nil
		})
		allowlist := make([]string, maxDomainAllowlistEntries+1)
		for i := range allowlist {
			allowlist[i] = fmt.Sprintf("host%d.test", i)
		}
		result, err := provider.Create(context.Background(), placement.CreateRequest{
			SnapshotRef:            DefaultSnapshotName,
			Resource:               placement.ResourceSize{VCPU: 1, MemGiB: 2},
			NetworkDomainAllowlist: allowlist,
		})
		if err == nil {
			t.Fatal("Create succeeded, want allowlist validation error")
		}
		if result.Outcome != placement.CreateOutcomeNotDispatched {
			t.Fatalf("Outcome = %q, want %q", result.Outcome, placement.CreateOutcomeNotDispatched)
		}
	})

	t.Run("execute error is unknown", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"provider unavailable"}`), nil
		})
		result, err := provider.Create(context.Background(), placement.CreateRequest{
			SnapshotRef: DefaultSnapshotName,
			Resource:    placement.ResourceSize{VCPU: 1, MemGiB: 2},
		})
		if err == nil {
			t.Fatal("Create succeeded, want server error")
		}
		// A 5xx cannot prove the sandbox was not created server-side; the
		// broker must treat it as "a sandbox may exist".
		if result.Outcome != placement.CreateOutcomeUnknown {
			t.Fatalf("Outcome = %q, want %q", result.Outcome, placement.CreateOutcomeUnknown)
		}
		if result.SandboxID != "" {
			t.Fatalf("SandboxID = %q, want empty", result.SandboxID)
		}
	})

	t.Run("transport error is unknown", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset by peer")
		})
		result, err := provider.Create(context.Background(), placement.CreateRequest{
			SnapshotRef: DefaultSnapshotName,
			Resource:    placement.ResourceSize{VCPU: 1, MemGiB: 2},
		})
		if err == nil {
			t.Fatal("Create succeeded, want transport error")
		}
		// The reset may have happened after the request reached Daytona.
		if result.Outcome != placement.CreateOutcomeUnknown {
			t.Fatalf("Outcome = %q, want %q", result.Outcome, placement.CreateOutcomeUnknown)
		}
	})

	t.Run("success is created", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://proxy.test/toolbox", nil)), nil
		})
		result, err := provider.Create(context.Background(), placement.CreateRequest{
			SnapshotRef: DefaultSnapshotName,
			Resource:    placement.ResourceSize{VCPU: 1, MemGiB: 2},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if result.Outcome != placement.CreateOutcomeCreated || result.SandboxID != "sandbox-1" {
			t.Fatalf("result = %+v, want created sandbox-1", result)
		}
	})
}

func TestCreateSendsDeterministicName(t *testing.T) {
	var captured map[string]any
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://proxy.test/toolbox", nil)), nil
	})

	if _, err := provider.Create(context.Background(), placement.CreateRequest{
		SnapshotRef: DefaultSnapshotName,
		Name:        "lead-placement-42",
		Resource:    placement.ResourceSize{VCPU: 1, MemGiB: 2},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := captured["name"]; got != "lead-placement-42" {
		t.Fatalf("name = %v, want lead-placement-42 — without it an ambiguous create cannot be reconciled by point read", got)
	}
}

func TestFindByNameIsAuthoritativePointRead(t *testing.T) {
	t.Run("resolves the real sandbox id", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet || req.URL.Path != "/api/sandbox/lead-placement-42" {
				t.Fatalf("request = %s %s, want GET /api/sandbox/lead-placement-42", req.Method, req.URL.Path)
			}
			return jsonResponse(http.StatusOK, sandboxBody("sandbox-9", "started", "https://proxy.test/toolbox", map[string]string{
				placement.PlacementLabelKey: "lead-placement-42",
			})), nil
		})
		sandbox, err := provider.FindByName(context.Background(), "lead-placement-42")
		if err != nil {
			t.Fatalf("FindByName: %v", err)
		}
		if sandbox.ID != "sandbox-9" {
			t.Fatalf("sandbox id = %q, want sandbox-9 (the provider's id, not the name)", sandbox.ID)
		}
	})

	t.Run("maps not found", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, `{"message":"Sandbox not found"}`), nil
		})
		if _, err := provider.FindByName(context.Background(), "lead-placement-42"); !errors.Is(err, placement.ErrSandboxNotFound) {
			t.Fatalf("FindByName = %v, want ErrSandboxNotFound", err)
		}
	})

	t.Run("does not map server errors to absence", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"provider unavailable"}`), nil
		})
		_, err := provider.FindByName(context.Background(), "lead-placement-42")
		if err == nil || errors.Is(err, placement.ErrSandboxNotFound) {
			t.Fatalf("FindByName = %v, want a non-absence error", err)
		}
	})
}
