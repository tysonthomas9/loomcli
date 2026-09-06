package route

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRecorderRecordsHandleAndHandleFunc(t *testing.T) {
	r := NewRecorder()
	r.Handle("GET /a", http.NotFoundHandler())
	r.HandleFunc("POST /b", func(http.ResponseWriter, *http.Request) {})

	want := []string{"GET /a", "POST /b"}
	if got := r.Patterns(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Patterns() = %v, want %v", got, want)
	}
}

func TestRecorderEmpty(t *testing.T) {
	if got := NewRecorder().Patterns(); len(got) != 0 {
		t.Fatalf("Patterns() on empty recorder = %v, want empty", got)
	}
}

func TestRecorderDelegatesToMux(t *testing.T) {
	r := NewRecorder()
	r.HandleFunc("GET /hello", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	})
	r.Handle("GET /handler", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/hello", http.StatusTeapot, "hi"},
		{"/handler", http.StatusNoContent, ""},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.code {
			t.Errorf("%s: status = %d, want %d", tc.path, rec.Code, tc.code)
		}
		if got := rec.Body.String(); got != tc.body {
			t.Errorf("%s: body = %q, want %q", tc.path, got, tc.body)
		}
	}

	// The embedded mux surface stays reachable through the Recorder.
	if _, pattern := r.Handler(httptest.NewRequest(http.MethodGet, "/hello", nil)); pattern != "GET /hello" {
		t.Errorf("Handler() pattern = %q, want %q", pattern, "GET /hello")
	}
}

func TestRecorderPatternsSortedAndDeduped(t *testing.T) {
	r := NewRecorder()
	h := http.NotFoundHandler()
	// Registered out of order, and the same pattern twice via both methods.
	r.Handle("/z", h)
	r.HandleFunc("/a", func(http.ResponseWriter, *http.Request) {})
	r.Handle("/m", h)
	r.patterns = append(r.patterns, "/a", "/z") // duplicates the mux would reject

	want := []string{"/a", "/m", "/z"}
	if got := r.Patterns(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Patterns() = %v, want %v", got, want)
	}
}

func TestRecorderPatternsIsDefensiveCopy(t *testing.T) {
	r := NewRecorder()
	r.Handle("/a", http.NotFoundHandler())
	r.Handle("/b", http.NotFoundHandler())

	got := r.Patterns()
	got[0] = "mutated"
	got = append(got, "extra")
	_ = got

	want := []string{"/a", "/b"}
	if again := r.Patterns(); !reflect.DeepEqual(again, want) {
		t.Fatalf("Patterns() after caller mutation = %v, want %v", again, want)
	}
}

// Compile-time proof that both a plain mux and a Recorder satisfy Router, which
// is what makes the *http.ServeMux -> route.Router parameter swap behavior-free.
var (
	_ Router = (*http.ServeMux)(nil)
	_ Router = (*Recorder)(nil)
)
