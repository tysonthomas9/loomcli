package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// patchRecorderService records the fields map PatchTab was handed. Only the
// tab-patch path carries logic; every other method panics so an unexpected
// call from the handler under test is loud rather than silently nil.
type patchRecorderService struct {
	service.TerminalService
	gotFields map[string]string
}

func (s *patchRecorderService) PatchTab(_ context.Context, _, session string, fields map[string]string) (*service.PatchTabResult, error) {
	s.gotFields = fields
	return &service.PatchTabResult{Tab: &tabmeta.TabMetadata{SessionName: session}}, nil
}

// patchTab drives HandlePatchTerminalTab with a raw JSON body and returns the
// recorded response plus the fields the service received.
func patchTab(t *testing.T, body string) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()

	svc := &patchRecorderService{}
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/E2E/terminal/tabs/term_1", strings.NewReader(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "E2E"))
	req.SetPathValue("session", "term_1")

	rec := httptest.NewRecorder()
	HandlePatchTerminalTab(svc)(rec, req)
	return rec, svc.gotFields
}

func TestHandlePatchTerminalTab_ReplacedAt(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantField  string // "" means the key must be absent
		wantHasKey bool
	}{
		{
			name:       "dismiss clears the marker",
			body:       `{"replaced_at":""}`,
			wantStatus: http.StatusOK,
			wantField:  "",
			wantHasKey: true,
		},
		{
			name:       "explicit RFC3339 timestamp is forwarded",
			body:       `{"replaced_at":"2026-08-14T16:52:03Z"}`,
			wantStatus: http.StatusOK,
			wantField:  "2026-08-14T16:52:03Z",
			wantHasKey: true,
		},
		{
			name:       "unparseable timestamp is rejected",
			body:       `{"replaced_at":"not-a-time"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "replaced_reason alone is not a field",
			body:       `{"replaced_reason":"anything"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "existing fields keep working",
			body:       `{"label":"renamed"}`,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, fields := patchTab(t, tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus != http.StatusOK {
				if fields != nil {
					t.Errorf("service was called with %v, want no call", fields)
				}
				return
			}
			got, ok := fields["replaced_at"]
			if ok != tt.wantHasKey {
				t.Fatalf("replaced_at present = %v, want %v (fields %v)", ok, tt.wantHasKey, fields)
			}
			if ok && got != tt.wantField {
				t.Errorf("replaced_at = %q, want %q", got, tt.wantField)
			}
		})
	}
}

// replaced_reason is server-written: even alongside a valid replaced_at it
// must not reach the store, or a client could widen the enum.
func TestHandlePatchTerminalTab_IgnoresClientReplacedReason(t *testing.T) {
	_, fields := patchTab(t, `{"replaced_at":"2026-08-14T16:52:03Z","replaced_reason":"user_hacked_it"}`)
	if _, ok := fields["replaced_reason"]; ok {
		t.Errorf("replaced_reason reached the store: %v", fields)
	}
}

func TestHandlePatchTerminalTab_RejectsEmptyBody(t *testing.T) {
	rec, fields := patchTab(t, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if fields != nil {
		t.Errorf("service was called with %v, want no call", fields)
	}
	var resp tabMetadataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != "no fields to update" {
		t.Errorf("error = %q, want %q", resp.Error, "no fields to update")
	}
}

func TestBuildPatchFields_ExistingFieldsUnchanged(t *testing.T) {
	label, notes, issue := "l", "n", "PUPPET-1"
	order, pinned := 3, true
	fields := buildPatchFields(tabPatchRequest{
		Label:     &label,
		Notes:     &notes,
		SortOrder: &order,
		Pinned:    &pinned,
		IssueID:   &issue,
	})

	want := map[string]string{
		"label": "l", "notes": "n", "sort_order": "3",
		"pinned": "true", "issue_id": "PUPPET-1",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("fields[%q] = %q, want %q", k, fields[k], v)
		}
	}
	if _, ok := fields["replaced_at"]; ok {
		t.Errorf("replaced_at set without a request field: %v", fields)
	}
}
