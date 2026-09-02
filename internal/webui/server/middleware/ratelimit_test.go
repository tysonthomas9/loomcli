package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// newRateLimitHarness installs RateLimit around a handler that returns 200 and
// counts the requests that reach it. The caller is responsible for rl.Stop().
func newRateLimitHarness(t *testing.T, cfg RateLimitConfig) (http.Handler, *int) {
	t.Helper()
	hits := 0
	rl, mw := RateLimit(cfg)
	t.Cleanup(rl.Stop)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	return h, &hits
}

func doReq(h http.Handler, method, path, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRateLimitDisabledPassesThrough(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	cfg.Enabled = false
	cfg.ReadRate = 1
	cfg.ReadBurst = 1
	h, hits := newRateLimitHarness(t, cfg)

	for i := 0; i < 50; i++ {
		if rec := doReq(h, http.MethodGet, "/api/issues", "10.0.0.1:1234"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, rec.Code)
		}
	}
	if *hits != 50 {
		t.Fatalf("handler hits = %d, want 50", *hits)
	}
}

func TestRateLimitEnabledRejectsOverBurst(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	cfg.ReadRate = 1
	cfg.ReadBurst = 1
	h, _ := newRateLimitHarness(t, cfg)

	if rec := doReq(h, http.MethodGet, "/api/issues", "10.0.0.2:1234"); rec.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", rec.Code)
	}
	rec := doReq(h, http.MethodGet, "/api/issues", "10.0.0.2:1234")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("429 response is missing a Retry-After header")
	}
	if body := rec.Body.String(); !contains(body, "rate limit exceeded") {
		t.Errorf("429 body = %q, want it to mention the rate limit error", body)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestRateLimitSeparatesReadAndMutate(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	cfg.MutateRate = 1
	cfg.MutateBurst = 1
	h, _ := newRateLimitHarness(t, cfg)

	if rec := doReq(h, http.MethodPost, "/api/issues", "10.0.0.3:1"); rec.Code != http.StatusOK {
		t.Fatalf("first POST: got %d, want 200", rec.Code)
	}
	if rec := doReq(h, http.MethodPost, "/api/issues", "10.0.0.3:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second POST: got %d, want 429", rec.Code)
	}
	if rec := doReq(h, http.MethodGet, "/api/issues", "10.0.0.3:1"); rec.Code != http.StatusOK {
		t.Fatalf("GET after mutate bucket drained: got %d, want 200", rec.Code)
	}
}

func TestRateLimitPerIP(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	cfg.ReadRate = 1
	cfg.ReadBurst = 1
	h, _ := newRateLimitHarness(t, cfg)

	if rec := doReq(h, http.MethodGet, "/api/issues", "10.0.0.4:1"); rec.Code != http.StatusOK {
		t.Fatalf("ip A first: got %d, want 200", rec.Code)
	}
	if rec := doReq(h, http.MethodGet, "/api/issues", "10.0.0.4:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("ip A second: got %d, want 429", rec.Code)
	}
	if rec := doReq(h, http.MethodGet, "/api/issues", "10.0.0.5:1"); rec.Code != http.StatusOK {
		t.Fatalf("ip B first: got %d, want 200 (buckets must be per IP)", rec.Code)
	}
}

func TestRateLimitExcludedPaths(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		cfg := DefaultRateLimitConfig()
		cfg.Enabled = enabled
		cfg.ReadRate = 1
		cfg.ReadBurst = 1
		h, _ := newRateLimitHarness(t, cfg)

		// Drain the bucket on a non-excluded path first.
		doReq(h, http.MethodGet, "/api/issues", "10.0.0.6:1")
		doReq(h, http.MethodGet, "/api/issues", "10.0.0.6:1")

		for _, path := range []string{"/health", "/api/health", "/api/client-errors"} {
			if rec := doReq(h, http.MethodGet, path, "10.0.0.6:1"); rec.Code != http.StatusOK {
				t.Errorf("enabled=%v %s: got %d, want 200", enabled, path, rec.Code)
			}
		}
	}
}

func TestRateLimitConfigWithDefaults(t *testing.T) {
	d := DefaultRateLimitConfig()

	tests := []struct {
		name string
		in   RateLimitConfig
		want RateLimitConfig
	}{
		{
			name: "zero value gets every default but stays disabled",
			in:   RateLimitConfig{},
			want: RateLimitConfig{
				Enabled: false, ReadRate: d.ReadRate, ReadBurst: d.ReadBurst,
				MutateRate: d.MutateRate, MutateBurst: d.MutateBurst,
				CleanupInterval: d.CleanupInterval, EntryTTL: d.EntryTTL,
			},
		},
		{
			name: "negative rates fall back to defaults, Enabled preserved",
			in:   RateLimitConfig{Enabled: true, ReadRate: -5, MutateRate: -1},
			want: RateLimitConfig{
				Enabled: true, ReadRate: d.ReadRate, ReadBurst: d.ReadBurst,
				MutateRate: d.MutateRate, MutateBurst: d.MutateBurst,
				CleanupInterval: d.CleanupInterval, EntryTTL: d.EntryTTL,
			},
		},
		{
			name: "burst below rate is clamped up to ceil(rate)",
			in: RateLimitConfig{
				Enabled: true, ReadRate: 250, ReadBurst: 10,
				MutateRate: 30, MutateBurst: 5,
				CleanupInterval: time.Minute, EntryTTL: time.Hour,
			},
			want: RateLimitConfig{
				Enabled: true, ReadRate: 250, ReadBurst: 250,
				MutateRate: 30, MutateBurst: 30,
				CleanupInterval: time.Minute, EntryTTL: time.Hour,
			},
		},
		{
			name: "already valid config is unchanged",
			in:   d,
			want: d,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.WithDefaults(); got != tt.want {
				t.Errorf("WithDefaults() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRateLimitWithDefaultsNeverYieldsZeroLimiter(t *testing.T) {
	// rate.NewLimiter(0, 0) rejects every request; WithDefaults exists to make
	// that unreachable from a flag or env typo.
	cfg := RateLimitConfig{Enabled: true}.WithDefaults()
	if cfg.ReadRate == 0 || cfg.ReadBurst == 0 || cfg.MutateRate == 0 || cfg.MutateBurst == 0 {
		t.Fatalf("WithDefaults left a zero limiter parameter: %+v", cfg)
	}
	if l := rate.NewLimiter(cfg.ReadRate, cfg.ReadBurst); !l.Allow() {
		t.Error("limiter built from normalized defaults rejected its first request")
	}
}

func TestRateLimitDisabledStopIsSafe(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	cfg.Enabled = false
	rl, _ := RateLimit(cfg)
	rl.Stop()
	rl.Stop() // stopOnce makes the repeat a no-op rather than a double close.
}
