package fleet

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// assertJSONResponse decodes the response body into a generic map and returns it.
func assertJSONResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	return result
}

// assertEnvelopeSuccess checks that the response has {success: true} and no error field.
func assertEnvelopeSuccess(t *testing.T, body map[string]interface{}) {
	t.Helper()
	success, ok := body["success"]
	if !ok {
		t.Fatal("missing 'success' field in response")
	}
	if success != true {
		t.Errorf("success = %v, want true", success)
	}
	if errVal, ok := body["error"]; ok {
		if str, isStr := errVal.(string); isStr && str != "" {
			t.Errorf("unexpected 'error' field in success response: %v", errVal)
		}
	}
}

// assertEnvelopeSuccessWithData checks that the response has {success: true, <dataFieldName>: <present>} and no error field.
func assertEnvelopeSuccessWithData(t *testing.T, body map[string]interface{}, dataFieldName string) {
	t.Helper()
	assertEnvelopeSuccess(t, body)
	if _, ok := body[dataFieldName]; !ok {
		t.Errorf("missing '%s' field in success response", dataFieldName)
	}
}

// assertEnvelopeError checks that the response has {success: false, error: <string>} and no data field.
func assertEnvelopeError(t *testing.T, body map[string]interface{}, dataFieldName string) {
	t.Helper()
	success, ok := body["success"]
	if !ok {
		t.Fatal("missing 'success' field in response")
	}
	if success != false {
		t.Errorf("success = %v, want false", success)
	}
	errVal, ok := body["error"]
	if !ok {
		t.Fatal("missing 'error' field in error response")
	}
	if _, ok := errVal.(string); !ok {
		t.Errorf("'error' field is %T, want string", errVal)
	}
	if dataVal, ok := body[dataFieldName]; ok && dataVal != nil {
		t.Errorf("unexpected '%s' field in error response: %v", dataFieldName, dataVal)
	}
}
