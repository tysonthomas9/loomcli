package prreview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestReviewerAdmissionErrorsPreserveUnauthenticatedContract(t *testing.T) {
	for _, reason := range []authority.DenialReason{
		authority.DenialInvalidAuthority,
		authority.DenialExpired,
	} {
		t.Run(string(reason), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeReviewerConversationError(recorder, &authority.AdmissionError{Reason: reason})

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Code != "unauthenticated" {
				t.Fatalf("code = %q, want unauthenticated", response.Code)
			}
		})
	}
}

func TestReviewerWrongClassPreservesForbiddenContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeReviewerConversationError(recorder, &authority.AdmissionError{Reason: authority.DenialWrongClass})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "forbidden" {
		t.Fatalf("code = %q, want forbidden", response.Code)
	}
}
