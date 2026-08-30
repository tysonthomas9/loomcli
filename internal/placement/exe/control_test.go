package exe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

func testProvider(t *testing.T, handler http.HandlerFunc) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Provider{control: newControlClient("tok", srv.URL, 0), hostKeys: newHostKeyStore("")}, srv
}

// TestCreateOutcomeTaxonomy is the contract that underpins the billing-leak
// fix. placement/provider.go:121 says NotDispatched may be returned only for
// failures BEFORE any network I/O, so every completed HTTP response -- however
// hopeless -- must be Unknown.
func TestCreateOutcomeTaxonomy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		vmName  string
		want    placement.CreateOutcome
		wantErr bool
	}{
		{
			name:   "200 naming the vm is Created",
			status: 200, body: `{"vms":[{"vm_name":"loom-p1"}]}`, vmName: "loom-p1",
			want: placement.CreateOutcomeCreated,
		},
		{
			// The single most dangerous case: a duplicate-name 422 PROVES a
			// same-name VM exists. Calling it NotDispatched would let the
			// broker sever the only record of a billing sandbox.
			name:   "422 duplicate name is Unknown, never NotDispatched",
			status: 422, body: `{"error":"name \"loom-p1\" is not available"}`, vmName: "loom-p1",
			want: placement.CreateOutcomeUnknown, wantErr: true,
		},
		{
			name:   "403 is Unknown",
			status: 403, body: `{"error":"forbidden"}`, vmName: "loom-p1",
			want: placement.CreateOutcomeUnknown, wantErr: true,
		},
		{
			name:   "500 is Unknown",
			status: 500, body: `{"error":"boom"}`, vmName: "loom-p1",
			want: placement.CreateOutcomeUnknown, wantErr: true,
		},
		{
			// A 200 whose body names nothing does not prove creation, but it
			// certainly does not prove non-dispatch either.
			name:   "200 without a VM identity is Unknown",
			status: 200, body: `{"ok":true}`, vmName: "loom-p1",
			want: placement.CreateOutcomeUnknown, wantErr: true,
		},
		{
			// The ONLY NotDispatched path: rejected locally, before any I/O.
			name:   "locally rejected name is NotDispatched",
			status: 200, body: `{"vms":[{"vm_name":"x"}]}`, vmName: "Bad Name!",
			want: placement.CreateOutcomeNotDispatched, wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dispatched bool
			p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				dispatched = true
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			res, err := p.Create(context.Background(), placement.CreateRequest{
				Name:     tc.vmName,
				Resource: placement.ResourceSize{VCPU: 2, MemGiB: 4},
			})
			if res.Outcome != tc.want {
				t.Fatalf("outcome = %q, want %q (err=%v)", res.Outcome, tc.want, err)
			}
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if tc.want == placement.CreateOutcomeNotDispatched && dispatched {
				t.Fatal("NotDispatched was returned after the request reached the server")
			}
			if tc.want == placement.CreateOutcomeCreated && res.SandboxID != tc.vmName {
				t.Fatalf("sandbox id = %q, want %q", res.SandboxID, tc.vmName)
			}
		})
	}
}

// TestListRefusesToInferAbsence guards the two-pass absence protocol. A 403
// body unmarshals cleanly into an empty slice, so without status + error + key
// checks an EXPIRING TOKEN would look like a fleet-wide proven absence and
// trigger mass release.
func TestListRefusesToInferAbsence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"403 with an error body", `{"error":"forbidden"}`, 403},
		{"200 with an error body", `{"error":"nope"}`, 200},
		{"200 with no vms key", `{"other":[]}`, 200},
		{"200 with unparseable body", `not json`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := p.Get(context.Background(), "loom-p1")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if errors.Is(err, placement.ErrSandboxNotFound) {
				t.Fatal("reported PROVEN ABSENCE from a response that proves nothing")
			}
		})
	}
}

func TestGetReportsNotFoundOnlyForAWellFormedEmptyResult(t *testing.T) {
	p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"vms":[]}`)
	})
	if _, err := p.Get(context.Background(), "loom-p1"); !errors.Is(err, placement.ErrSandboxNotFound) {
		t.Fatalf("err = %v, want ErrSandboxNotFound", err)
	}
}

// TestDeleteParsesTheBody: live-verified that `rm <absent>` answers HTTP 200
// with {"deleted":[],"failed":[...]}. Trusting the status code would mark a
// live, billing VM as released.
func TestDeleteParsesTheBody(t *testing.T) {
	t.Run("confirmed delete succeeds", func(t *testing.T) {
		p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"deleted":["loom-p1"],"failed":[]}`)
		})
		if err := p.Delete(context.Background(), "loom-p1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})
	t.Run("200 with a failed entry is unconfirmed, not absent", func(t *testing.T) {
		// NDJSON, exactly as the live service returns it.
		p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "{\"error\":\"VM \\\"loom-p1\\\" not found\"}\n{\"deleted\":[],\"failed\":[\"loom-p1\"]}")
		})
		err := p.Delete(context.Background(), "loom-p1")
		if !errors.Is(err, ErrDeleteUnconfirmed) {
			t.Fatalf("err = %v, want ErrDeleteUnconfirmed", err)
		}
		if errors.Is(err, placement.ErrSandboxNotFound) {
			t.Fatal("an ambiguous failed entry must not be reported as proven absence")
		}
	})
}

// TestNDJSONErrorIsVisible pins the parsing finding: a single json.Unmarshal
// over a two-object body fails with "Extra data", which silently hid every
// server-reported error.
func TestNDJSONErrorIsVisible(t *testing.T) {
	body := "{\"error\":\"boom\"}\n{\"vms\":[]}"
	if got := errorText(body); got != "boom" {
		t.Fatalf("errorText over NDJSON = %q, want boom", got)
	}
	if n := len(decodeStream(body)); n != 2 {
		t.Fatalf("decodeStream returned %d objects, want 2", n)
	}
}

func TestCreateCommandIsAllowlisted(t *testing.T) {
	t.Run("rejects injection attempts before any I/O", func(t *testing.T) {
		for _, name := range []string{
			"loom p1", "loom-p1;rm -rf /", "loom-p1 --tag=x", "LOOM-P1", "", "-leading",
		} {
			if _, err := (createOpts{Name: name}).buildCreate(); !errors.Is(err, ErrUnsafeArg) {
				t.Errorf("name %q: err = %v, want ErrUnsafeArg", name, err)
			}
		}
		for _, env := range []map[string]string{
			{"BAD KEY": "v"}, {"K": "value with spaces"}, {"K": "v;rm -rf /"},
		} {
			if _, err := (createOpts{Name: "loom-p1", Env: env}).buildCreate(); !errors.Is(err, ErrUnsafeArg) {
				t.Errorf("env %v: err = %v, want ErrUnsafeArg", env, err)
			}
		}
	})
	t.Run("serializes a valid request deterministically", func(t *testing.T) {
		cmd, err := (createOpts{
			Name: "loom-p1", CPU: 2, Memory: "4gb", Image: "ubuntu-24.04",
			Tags: []string{"loom-env__dev"}, Env: map[string]string{"B": "2", "A": "1"},
		}).buildCreate()
		if err != nil {
			t.Fatalf("buildCreate: %v", err)
		}
		for _, want := range []string{
			"new --json --no-email", "--name=loom-p1", "--cpu=2", "--memory=4gb",
			"--image=ubuntu-24.04", "--tag=loom-env__dev",
		} {
			if !strings.Contains(cmd, want) {
				t.Errorf("command missing %q: %s", want, cmd)
			}
		}
		// Sorted env keeps the command deterministic; map order is not.
		if !strings.Contains(cmd, "--env A=1 --env B=2") {
			t.Errorf("env not serialized in sorted order: %s", cmd)
		}
	})
}
