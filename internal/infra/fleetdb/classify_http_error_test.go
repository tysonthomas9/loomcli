package fleetdb

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestClassifyHTTPError_GoneMapsToErrGone(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"code":"lease_expired","message":"heartbeat agent ownership lease failed"}}`)
	err := classifyHTTPError(http.MethodPost, "/api/v1/WS/agent-ownership-leases/a/heartbeat", http.StatusGone, body)
	if !errors.Is(err, domain.ErrGone) {
		t.Fatalf("err = %v, want errors.Is ErrGone", err)
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, must not satisfy ErrAlreadyExists/ErrConflict", err)
	}
}

// The pre-410 mappings must be unchanged (back-compat matrix).
func TestClassifyHTTPError_ExistingMappingsUnchanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, domain.ErrNotFound},
		{http.StatusConflict, domain.ErrAlreadyExists},
		{http.StatusBadRequest, domain.ErrInvalid},
		{http.StatusUnprocessableEntity, domain.ErrInvalid},
		{http.StatusForbidden, domain.ErrConflict},
	}
	for _, tc := range cases {
		err := classifyHTTPError(http.MethodPost, "/x", tc.status, nil)
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestClassifyHTTPError_SkillForbiddenStaysDistinct(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(http.MethodPut, "/api/v1/WS/roles/reviewer/skills/code-review/files/SKILL.md", http.StatusForbidden, nil)
	if !errors.Is(err, domain.ErrSkillForbidden) {
		t.Fatalf("err = %v, want errors.Is ErrSkillForbidden", err)
	}
	if errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, skill forbidden must not collapse to ErrConflict", err)
	}
}

func TestClassifyHTTPError_SkillForbiddenMatchesOnlySkillRouteFamilies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		path      string
		wantSkill bool
	}{
		{name: "workspace collection", path: "/api/v1/WS/skills", wantSkill: true},
		{name: "workspace item", path: "/api/v1/WS/skills/code-review", wantSkill: true},
		{name: "role file", path: "/api/v1/WS/roles/reviewer/skills/code-review/files/SKILL.md", wantSkill: true},
		{name: "repo named skills", path: "/api/v1/WS/repos/skills", wantSkill: false},
		{name: "unrelated suffix", path: "/api/v1/WS/things/skills", wantSkill: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError(http.MethodPut, tt.path, http.StatusForbidden, nil)
			if got := errors.Is(err, domain.ErrSkillForbidden); got != tt.wantSkill {
				t.Fatalf("errors.Is(%v, ErrSkillForbidden) = %t, want %t", err, got, tt.wantSkill)
			}
			if !tt.wantSkill && !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("err = %v, want generic ErrConflict", err)
			}
		})
	}
}

// A fleet-db that does not serve the lease routes answers with ServeMux's
// default handler (text/plain, no envelope) or a bare 405. That is the same
// operational condition as an unavailable lease store, and must classify as
// one — while a real handler's enveloped 404 keeps meaning "no such lease".
func TestClassifyHTTPError_SkillMaterializationLeaseRouteMissing(t *testing.T) {
	t.Parallel()
	const (
		collection = "/api/v1/WS/skill-materialization-leases"
		item       = "/api/v1/WS/skill-materialization-leases/abc123"
	)
	tests := []struct {
		name   string
		method string
		path   string
		status int
		body   []byte
		check  func(t *testing.T, err error)
	}{
		{
			name: "unrouted collection 404", method: http.MethodPost, path: collection,
			status: http.StatusNotFound, body: []byte("404 page not found\n"),
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrSkillMaterializationLeaseRouteMissing) {
					t.Fatalf("err = %v, want errors.Is ErrSkillMaterializationLeaseRouteMissing", err)
				}
				if !errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, want errors.Is ErrSkillMaterializationLeaseStoreUnavailable", err)
				}
			},
		},
		{
			name: "method not allowed on collection", method: http.MethodPost, path: collection,
			status: http.StatusMethodNotAllowed, body: nil,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrSkillMaterializationLeaseRouteMissing) {
					t.Fatalf("err = %v, want errors.Is ErrSkillMaterializationLeaseRouteMissing", err)
				}
				if !errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, want errors.Is ErrSkillMaterializationLeaseStoreUnavailable", err)
				}
			},
		},
		{
			name: "proxy html 404 on collection", method: http.MethodPost, path: collection,
			status: http.StatusNotFound, body: []byte("<html><body>404 Not Found</body></html>"),
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, want errors.Is ErrSkillMaterializationLeaseStoreUnavailable", err)
				}
			},
		},
		{
			name: "enveloped 404 on lease item stays not found", method: http.MethodDelete, path: item,
			status: http.StatusNotFound, body: []byte(`{"error":{"code":"not_found","message":"lease not found"}}`),
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("err = %v, want errors.Is ErrNotFound", err)
				}
				if errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, an enveloped lease 404 must not degrade", err)
				}
			},
		},
		{
			name: "store unavailable 503 unchanged", method: http.MethodPost, path: collection,
			status: http.StatusServiceUnavailable,
			body:   []byte(`{"error":{"code":"` + skillMaterializationLeaseStoreUnavailableCode + `"}}`),
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, want errors.Is ErrSkillMaterializationLeaseStoreUnavailable", err)
				}
				if errors.Is(err, domain.ErrSkillMaterializationLeaseRouteMissing) {
					t.Fatalf("err = %v, a 503 is not a missing route", err)
				}
			},
		},
		{
			name: "lease conflict 409 unchanged", method: http.MethodPost, path: collection,
			status: http.StatusConflict,
			body:   []byte(`{"error":{"code":"` + skillMaterializationLeaseConflictCode + `","meta":{"holder":"lead@host#1"}}}`),
			check: func(t *testing.T, err error) {
				var conflict *domain.SkillMaterializationLeaseConflictError
				if !errors.As(err, &conflict) {
					t.Fatalf("err = %v, want *domain.SkillMaterializationLeaseConflictError", err)
				}
			},
		},
		{
			name: "generic conflict 409 still hard-fails", method: http.MethodPost, path: collection,
			status: http.StatusConflict, body: []byte(`{"error":{"code":"conflict"}}`),
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrConflict) {
					t.Fatalf("err = %v, want errors.Is ErrConflict", err)
				}
				if errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, a 409 conflict must keep hard-failing", err)
				}
			},
		},
		{
			name: "unrouted 404 off the lease family does not leak", method: http.MethodGet,
			path: "/api/v1/WS/issues/X", status: http.StatusNotFound, body: nil,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("err = %v, want errors.Is ErrNotFound", err)
				}
				if errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, the route-missing arm leaked off the lease family", err)
				}
			},
		},
		{
			name: "resource merely named like the lease family", method: http.MethodPost,
			path: "/api/v1/WS/repos/skill-materialization-leases", status: http.StatusNotFound, body: nil,
			check: func(t *testing.T, err error) {
				if errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) {
					t.Fatalf("err = %v, segment matching must not reach a deeper segment", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t, classifyHTTPError(tt.method, tt.path, tt.status, tt.body))
		})
	}
}
