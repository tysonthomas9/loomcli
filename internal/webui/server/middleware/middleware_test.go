package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChain_Empty(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	chained := Chain()(inner)

	rec := httptest.NewRecorder()
	chained.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", rec.Body.String())
	}
}

func TestChain_Single(t *testing.T) {
	addHeader := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Single", "yes")
			next.ServeHTTP(w, r)
		})
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	chained := Chain(addHeader)(inner)

	rec := httptest.NewRecorder()
	chained.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Header().Get("X-Single") != "yes" {
		t.Fatal("single middleware did not execute")
	}
}

func TestChain_Order(t *testing.T) {
	// Record the order in which middleware runs on the request path.
	var order []string

	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	a := mw("a")
	b := mw("b")
	c := mw("c")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	// Chain(a, b, c)(h) should be a(b(c(h))), so execution order is a, b, c, handler.
	chained := Chain(a, b, c)(inner)

	rec := httptest.NewRecorder()
	chained.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	expected := []string{"a", "b", "c", "handler"}
	if len(order) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, order)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Fatalf("position %d: expected %q, got %q (full: %v)", i, expected[i], order[i], order)
		}
	}
}

func TestChain_EquivalentToManualNesting(t *testing.T) {
	// Verify Chain(a, b, c)(h) produces the same observable behavior as a(b(c(h))).
	var chainedOrder, manualOrder []string

	mwFor := func(target *[]string, name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				*target = append(*target, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	handler := func(target *[]string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*target = append(*target, "handler")
		})
	}

	// Chain approach
	chained := Chain(
		mwFor(&chainedOrder, "a"),
		mwFor(&chainedOrder, "b"),
		mwFor(&chainedOrder, "c"),
	)(handler(&chainedOrder))

	// Manual nesting: a(b(c(h)))
	manual := mwFor(&manualOrder, "a")(
		mwFor(&manualOrder, "b")(
			mwFor(&manualOrder, "c")(
				handler(&manualOrder),
			),
		),
	)

	rec1 := httptest.NewRecorder()
	chained.ServeHTTP(rec1, httptest.NewRequest("GET", "/", nil))

	rec2 := httptest.NewRecorder()
	manual.ServeHTTP(rec2, httptest.NewRequest("GET", "/", nil))

	if len(chainedOrder) != len(manualOrder) {
		t.Fatalf("length mismatch: chained=%v manual=%v", chainedOrder, manualOrder)
	}
	for i := range chainedOrder {
		if chainedOrder[i] != manualOrder[i] {
			t.Fatalf("position %d differs: chained=%v manual=%v", i, chainedOrder, manualOrder)
		}
	}
}
