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
