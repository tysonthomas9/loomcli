package skillmat

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type fakeSkillMaterializationLeaseStore struct {
	acquireErrs []error
	lease       *domain.SkillMaterializationLease
	releaseErr  error
	acquires    []store.SkillMaterializationLeaseAcquire
	calls       *[]string
}

func (s *fakeSkillMaterializationLeaseStore) Acquire(_ context.Context, in store.SkillMaterializationLeaseAcquire) (*domain.SkillMaterializationLease, error) {
	if s.calls != nil {
		*s.calls = append(*s.calls, "acquire")
	}
	s.acquires = append(s.acquires, in)
	index := len(s.acquires) - 1
	if index < len(s.acquireErrs) && s.acquireErrs[index] != nil {
		return nil, s.acquireErrs[index]
	}
	return s.lease, nil
}

func (s *fakeSkillMaterializationLeaseStore) Renew(context.Context, string, string, string, time.Duration) (time.Time, error) {
	return time.Time{}, nil
}

func (s *fakeSkillMaterializationLeaseStore) Release(context.Context, string, string, string) error {
	if s.calls != nil {
		*s.calls = append(*s.calls, "release")
	}
	return s.releaseErr
}

type leasedMaterializeStore struct {
	store.Store
	leases store.SkillMaterializationLeaseStore
}

func (s leasedMaterializeStore) SkillMaterializationLeases() store.SkillMaterializationLeaseStore {
	return s.leases
}

func TestMaterializeLeasedAcquiresMaterializesAndReleases(t *testing.T) {
	target := t.TempDir()
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		lease: &domain.SkillMaterializationLease{Token: "token-1"},
		calls: &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	if err := materializeLeasedWith(t.Context(), st, "WS", "reviewer", target, deps); err != nil {
		t.Fatalf("materializeLeasedWith: %v", err)
	}
	if want := []string{"acquire", "materialize", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if len(leases.acquires) != 1 {
		t.Fatalf("acquires = %d", len(leases.acquires))
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}
	got := leases.acquires[0]
	if got.WorkspaceKey != "WS" || got.TargetKey != skillMaterializationTargetKey("test-host", absoluteTarget) || got.Holder != "reviewer@test-host#1234" || got.TTL != 15*time.Second {
		t.Fatalf("acquire = %+v", got)
	}
}

func TestSkillMaterializationTargetKeyFormat(t *testing.T) {
	const want = "d292026f144b448d38c2ab87fc0edd21b0098791a9b69e1b1ca086689c7ed2a4"
	if got := skillMaterializationTargetKey("test-host", "/absolute/target"); got != want {
		t.Fatalf("target key = %q, want %q", got, want)
	}
}

func TestMaterializeLeasedSkipsAfterBoundedConflictBackoff(t *testing.T) {
	var calls []string
	conflict := &domain.SkillMaterializationLeaseConflictError{
		Holder: "other@test-host#9", ExpiresAt: time.Now().UTC().Add(time.Second),
	}
	leases := &fakeSkillMaterializationLeaseStore{calls: &calls}
	for range len(materializationLeaseBackoff) + 1 {
		leases.acquireErrs = append(leases.acquireErrs, conflict)
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)
	var slept time.Duration
	deps.sleep = func(_ context.Context, delay time.Duration) error {
		slept += delay
		return nil
	}

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatalf("materializeLeasedWith: %v", err)
	}
	if slept > 2*time.Second {
		t.Fatalf("total backoff = %s, want <= 2s", slept)
	}
	if len(leases.acquires) != len(materializationLeaseBackoff)+1 {
		t.Fatalf("acquires = %d, want %d", len(leases.acquires), len(materializationLeaseBackoff)+1)
	}
	for _, call := range calls {
		if call == "materialize" || call == "release" {
			t.Fatalf("calls = %v, contention must skip materialization", calls)
		}
	}
}

// The case the retry loop exists for: contention that clears. The always-wins
// and always-conflicts tests around this one both take the loop's edges, so
// without this the retry-then-succeed path — acquire, back off, acquire again,
// then materialize under the lease — is never executed.
func TestMaterializeLeasedRetriesUntilContentionClears(t *testing.T) {
	if len(materializationLeaseBackoff) < 2 {
		t.Skipf("backoff schedule has %d steps, need 2", len(materializationLeaseBackoff))
	}
	var calls []string
	conflict := &domain.SkillMaterializationLeaseConflictError{
		Holder: "other@test-host#9", ExpiresAt: time.Now().UTC().Add(time.Second),
	}
	leases := &fakeSkillMaterializationLeaseStore{
		acquireErrs: []error{conflict, conflict, nil},
		lease:       &domain.SkillMaterializationLease{Token: "token-after-wait"},
		calls:       &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)
	var delays []time.Duration
	deps.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}

	if err := materializeLeasedWith(t.Context(), st, "WS", "reviewer", t.TempDir(), deps); err != nil {
		t.Fatalf("materializeLeasedWith: %v", err)
	}
	want := []string{"acquire", "acquire", "acquire", "materialize", "release"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	// Each retry waits the step for the attempt that failed, in order.
	if wantDelays := materializationLeaseBackoff[:2]; !reflect.DeepEqual(delays, wantDelays) {
		t.Fatalf("backoff delays = %v, want %v", delays, wantDelays)
	}
}

func TestMaterializeLeasedDegradesWhenLeaseStoreIsUnavailable(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		acquireErrs: []error{fmt.Errorf("redis down: %w", domain.ErrSkillMaterializationLeaseStoreUnavailable)},
		calls:       &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatalf("materializeLeasedWith: %v", err)
	}
	if want := []string{"acquire", "materialize"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// A fleet-db deployed without the lease routes at all is the same operational
// condition as an unavailable lease store: materialize unlocked, do not fail.
func TestMaterializeLeasedDegradesWhenLeaseRouteIsMissing(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		acquireErrs: []error{fmt.Errorf("fleetdb: POST /api/v1/WS/skill-materialization-leases: HTTP 404: %w",
			domain.ErrSkillMaterializationLeaseRouteMissing)},
		calls: &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatalf("materializeLeasedWith: %v", err)
	}
	if want := []string{"acquire", "materialize"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestMaterializeLeasedDegradesOnLeaseTransportError(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		acquireErrs: []error{&url.Error{Op: "POST", URL: "http://fleet-db/leases", Err: syscall.ECONNREFUSED}},
		calls:       &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatalf("materializeLeasedWith: %v", err)
	}
	if want := []string{"acquire", "materialize"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestMaterializeLeasedReleaseFailureDoesNotOverrideMaterialization(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		lease:      &domain.SkillMaterializationLease{Token: "token-1"},
		releaseErr: errors.New("release failed"),
		calls:      &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatalf("materializeLeasedWith: %v", err)
	}
	if want := []string{"acquire", "materialize", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func testLeasedMaterializeDeps(calls *[]string) leasedMaterializeDeps {
	return leasedMaterializeDeps{
		hostname: func() (string, error) { return "test-host", nil },
		pid:      func() int { return 1234 },
		sleep:    func(context.Context, time.Duration) error { return nil },
		materialize: func(context.Context, store.Store, string, string, string) error {
			*calls = append(*calls, "materialize")
			return nil
		},
	}
}
