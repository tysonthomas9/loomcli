package taskcontent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type stubDetailer struct {
	calls  int
	detail *backend.IssueDetailData
	err    error
	fn     func(id string) (*backend.IssueDetailData, error)
}

func (s *stubDetailer) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	s.calls++
	if s.fn != nil {
		return s.fn(id)
	}
	return s.detail, s.err
}

func detail(mutate func(*backend.IssueDetailData)) *backend.IssueDetailData {
	d := &backend.IssueDetailData{IssueData: backend.IssueData{ID: "T-1", Title: "a title"}}
	if mutate != nil {
		mutate(d)
	}
	return d
}

func TestHasContent(t *testing.T) {
	tests := []struct {
		name  string
		input *backend.IssueDetailData
		want  bool
	}{
		{"description only", detail(func(d *backend.IssueDetailData) { d.Description = "do the thing" }), true},
		{"acceptance criteria only", detail(func(d *backend.IssueDetailData) { d.AcceptanceCriteria = "- it works" }), true},
		{"design body only", detail(func(d *backend.IssueDetailData) { d.Design = "# plan" }), true},
		{"design flag only", detail(func(d *backend.IssueDetailData) { d.HasDesign = true }), true},
		{"design artifact only", detail(func(d *backend.IssueDetailData) { d.DesignArtifactID = "art-9" }), true},
		{"title only", detail(nil), false},
		{"notes only", detail(func(d *backend.IssueDetailData) { d.Notes = "tester scratch row" }), false},
		{"whitespace description", detail(func(d *backend.IssueDetailData) { d.Description = "  \n\t " }), false},
		{"whitespace acceptance criteria", detail(func(d *backend.IssueDetailData) { d.AcceptanceCriteria = "\n" }), false},
		{"fully empty", &backend.IssueDetailData{}, false},
		{"nil detail fails open", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasContent(tc.input); got != tc.want {
				t.Fatalf("HasContent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGate_RefusesBodylessTask(t *testing.T) {
	g := NewGate()
	d := &stubDetailer{detail: detail(nil)}
	allowed, err := g.Allow(context.Background(), d, backend.IssueData{ID: "T-1"}, "T-1")
	if allowed {
		t.Fatal("expected a bodyless task to be refused")
	}
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("err = %v, want ErrNoContent", err)
	}
}

func TestGate_AllowsTaskWithContent(t *testing.T) {
	g := NewGate()
	d := &stubDetailer{detail: detail(func(x *backend.IssueDetailData) { x.Description = "real work" })}
	allowed, err := g.Allow(context.Background(), d, backend.IssueData{ID: "T-1"}, "T-1")
	if !allowed || err != nil {
		t.Fatalf("Allow = (%v, %v), want (true, nil)", allowed, err)
	}
}

func TestGate_AllowFailsOpenOnGetError(t *testing.T) {
	for _, tc := range []struct {
		name string
		stub *stubDetailer
	}{
		{"get error", &stubDetailer{err: fmt.Errorf("backend down")}},
		{"nil detail", &stubDetailer{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGate()
			allowed, err := g.Allow(context.Background(), tc.stub, backend.IssueData{ID: "T-1"}, "T-1")
			if !allowed || err != nil {
				t.Fatalf("Allow = (%v, %v), want (true, nil)", allowed, err)
			}
		})
	}
}

func TestGate_NilGateAndNilDetailerAllow(t *testing.T) {
	var g *Gate
	if allowed, err := g.Allow(context.Background(), &stubDetailer{}, backend.IssueData{}, "T-1"); !allowed || err != nil {
		t.Fatalf("nil gate Allow = (%v, %v), want (true, nil)", allowed, err)
	}
	if allowed, err := NewGate().Allow(context.Background(), nil, backend.IssueData{}, "T-1"); !allowed || err != nil {
		t.Fatalf("nil detailer Allow = (%v, %v), want (true, nil)", allowed, err)
	}
	if allowed, err := NewGate().Allow(context.Background(), &stubDetailer{}, backend.IssueData{}, "  "); !allowed || err != nil {
		t.Fatalf("blank id Allow = (%v, %v), want (true, nil)", allowed, err)
	}
}

func TestGate_MemoizesRefusal(t *testing.T) {
	g := NewGate()
	updated := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	d := &stubDetailer{detail: detail(nil)}
	slim := backend.IssueData{ID: "T-1", UpdatedAt: updated}

	for i := 0; i < 3; i++ {
		if allowed, _ := g.Allow(context.Background(), d, slim, "T-1"); allowed {
			t.Fatalf("call %d: expected refusal", i)
		}
	}
	if d.calls != 1 {
		t.Fatalf("Get called %d times, want 1 (refusal must be memoized)", d.calls)
	}
}

func TestGate_MemoInvalidatesOnUpdatedAt(t *testing.T) {
	g := NewGate()
	first := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	d := &stubDetailer{detail: detail(nil)}
	if allowed, _ := g.Allow(context.Background(), d, backend.IssueData{ID: "T-1", UpdatedAt: first}, "T-1"); allowed {
		t.Fatal("expected the bodyless row to be refused")
	}

	// The tester fills in a description; UpdatedAt moves.
	d.detail = detail(func(x *backend.IssueDetailData) { x.Description = "now it has a body" })
	second := first.Add(time.Minute)
	allowed, err := g.Allow(context.Background(), d, backend.IssueData{ID: "T-1", UpdatedAt: second}, "T-1")
	if !allowed || err != nil {
		t.Fatalf("Allow after edit = (%v, %v), want (true, nil)", allowed, err)
	}
	if d.calls != 2 {
		t.Fatalf("Get called %d times, want 2 (memo must invalidate on UpdatedAt)", d.calls)
	}
}

func TestGate_MemoExpiresAfterTTL(t *testing.T) {
	g := NewGate()
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	d := &stubDetailer{detail: detail(nil)}
	slim := backend.IssueData{ID: "T-1", UpdatedAt: now}

	if allowed, _ := g.Allow(context.Background(), d, slim, "T-1"); allowed {
		t.Fatal("expected refusal")
	}
	now = now.Add(refusalTTL - time.Second)
	if allowed, _ := g.Allow(context.Background(), d, slim, "T-1"); allowed {
		t.Fatal("expected refusal within the TTL")
	}
	if d.calls != 1 {
		t.Fatalf("Get called %d times within the TTL, want 1", d.calls)
	}
	now = now.Add(2 * time.Second)
	if allowed, _ := g.Allow(context.Background(), d, slim, "T-1"); allowed {
		t.Fatal("expected refusal after the TTL too (the row is still bodyless)")
	}
	if d.calls != 2 {
		t.Fatalf("Get called %d times after the TTL, want 2 (memo must expire)", d.calls)
	}
}

func TestGate_CacheBounded(t *testing.T) {
	g := NewGate()
	d := &stubDetailer{detail: detail(nil)}
	for i := 0; i < refusalCacheCap+64; i++ {
		id := fmt.Sprintf("T-%d", i)
		if allowed, _ := g.Allow(context.Background(), d, backend.IssueData{ID: id}, id); allowed {
			t.Fatalf("%s: expected refusal", id)
		}
	}
	g.mu.Lock()
	size := len(g.refusals)
	g.mu.Unlock()
	if size > refusalCacheCap {
		t.Fatalf("refusal memo holds %d entries, want <= %d", size, refusalCacheCap)
	}
}

func TestGate_ContentClearsMemo(t *testing.T) {
	g := NewGate()
	d := &stubDetailer{detail: detail(nil)}
	if allowed, _ := g.Allow(context.Background(), d, backend.IssueData{ID: "T-1"}, "T-1"); allowed {
		t.Fatal("expected refusal")
	}
	d.detail = detail(func(x *backend.IssueDetailData) { x.AcceptanceCriteria = "- done" })
	// Same (zero) UpdatedAt, but the TTL-expiry path is not what clears it here:
	// force a re-read by bumping UpdatedAt, then assert the memo is gone.
	if allowed, _ := g.Allow(context.Background(), d, backend.IssueData{ID: "T-1", UpdatedAt: time.Unix(1, 0)}, "T-1"); !allowed {
		t.Fatal("expected the now-described row to be allowed")
	}
	g.mu.Lock()
	_, present := g.refusals["T-1"]
	g.mu.Unlock()
	if present {
		t.Fatal("an allowed task must not keep a refusal memo")
	}
}

func TestEnabled_KillSwitch(t *testing.T) {
	tests := []struct {
		value string
		set   bool
		want  bool
	}{
		{"", false, true},
		{"", true, true},
		{"off", true, false},
		{"OFF", true, false},
		{" off ", true, false},
		{"0", true, false},
		{"false", true, false},
		{"no", true, false},
		{"on", true, true},
		{"1", true, true},
		{"garbage", true, true},
	}
	for _, tc := range tests {
		name := tc.value
		if !tc.set {
			name = "unset"
		}
		t.Run(name, func(t *testing.T) {
			if tc.set {
				t.Setenv(EnvKillSwitch, tc.value)
			} else {
				t.Setenv(EnvKillSwitch, "")
			}
			if got := Enabled(); got != tc.want {
				t.Fatalf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGate_DisabledByEnvAllowsBodyless(t *testing.T) {
	t.Setenv(EnvKillSwitch, "off")
	g := NewGate()
	d := &stubDetailer{detail: detail(nil)}
	allowed, err := g.Allow(context.Background(), d, backend.IssueData{ID: "T-1"}, "T-1")
	if !allowed || err != nil {
		t.Fatalf("Allow = (%v, %v), want (true, nil) with the gate disabled", allowed, err)
	}
	if d.calls != 0 {
		t.Fatalf("Get called %d times with the gate disabled, want 0", d.calls)
	}
}
