package httptransport_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/httptransport"
)

type jsonPayload struct {
	Name string `json:"name"`
}

func TestDecodeOneJSONRequestPolicy(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		options      httptransport.JSONDecodeOptions
		wantName     string
		wantTrailing bool
		wantTooLarge bool
		wantUnknown  bool
	}{
		{name: "one value", body: `{"name":"ok"}`, wantName: "ok"},
		{name: "second value", body: `{"name":"ok"} {}`, wantTrailing: true},
		{name: "trailing garbage", body: `{"name":"ok"} trailing`, wantTrailing: true},
		{
			name: "strict unknown field", body: `{"name":"ok","extra":true}`,
			options: httptransport.JSONDecodeOptions{DisallowUnknownFields: true}, wantUnknown: true,
		},
		{
			name: "oversized initial value", body: `{"name":"too-large"}`,
			options: httptransport.JSONDecodeOptions{MaxBytes: 8}, wantTooLarge: true,
		},
		{
			name: "oversized trailing whitespace", body: `{"name":"ok"}` + strings.Repeat(" ", 32),
			options: httptransport.JSONDecodeOptions{MaxBytes: 16}, wantTrailing: true, wantTooLarge: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			var got jsonPayload
			err := httptransport.DecodeOneJSONRequest(rec, req, &got, test.options)
			assertJSONDecodeResult(t, err, test.wantTrailing, test.wantTooLarge, test.wantUnknown)
			if err == nil && got.Name != test.wantName {
				t.Fatalf("name = %q, want %q", got.Name, test.wantName)
			}
		})
	}
}

func TestDecodeOneJSONBytesPolicy(t *testing.T) {
	var got jsonPayload
	if err := httptransport.DecodeOneJSONBytes([]byte(`{"name":"ok"}`), &got, httptransport.JSONDecodeOptions{}); err != nil {
		t.Fatalf("valid bytes error = %v", err)
	}
	if got.Name != "ok" {
		t.Fatalf("name = %q, want ok", got.Name)
	}

	err := httptransport.DecodeOneJSONBytes(
		[]byte(`{"name":"ok"} {}`),
		&got,
		httptransport.JSONDecodeOptions{},
	)
	assertJSONDecodeResult(t, err, true, false, false)

	err = httptransport.DecodeOneJSONBytes(
		[]byte(`{"name":"too-large"}`),
		&got,
		httptransport.JSONDecodeOptions{MaxBytes: 8},
	)
	assertJSONDecodeResult(t, err, false, true, false)
}

func assertJSONDecodeResult(t *testing.T, err error, wantTrailing, wantTooLarge, wantUnknown bool) {
	t.Helper()
	if wantTrailing {
		if !errors.Is(err, httptransport.ErrTrailingJSON) {
			t.Fatalf("error = %T %v, want ErrTrailingJSON", err, err)
		}
	}
	if wantTooLarge {
		var maxBytesErr *http.MaxBytesError
		if !errors.As(err, &maxBytesErr) {
			t.Fatalf("error = %T %v, want *http.MaxBytesError", err, err)
		}
	}
	if wantUnknown {
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v, want unknown-field error", err)
		}
	}
	if !wantTrailing && !wantTooLarge && !wantUnknown && err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
