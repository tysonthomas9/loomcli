package skillmat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type fakeSkillMaterializationLeaseStore struct {
	acquireErrs        []error
	lease              *domain.SkillMaterializationLease
	releaseErr         error
	renewErr           error
	renewCalls         atomic.Int32
	renewNotify        chan struct{}
	echoTrees          bool
	preserveNilTrees   bool
	preserveZeroExpiry bool
	acquires           []store.SkillMaterializationLeaseAcquire
	calls              *[]string
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
	if s.lease != nil {
		out := *s.lease
		if out.TargetKey == "" {
			out.TargetKey = in.TargetKey
		}
		if out.Holder == "" {
			out.Holder = in.Holder
		}
		if out.ExpiresAt.IsZero() && !s.preserveZeroExpiry {
			out.ExpiresAt = time.Now().Add(time.Minute)
		}
		if s.echoTrees || (out.TreeRevisions == nil && !s.preserveNilTrees) {
			out.TreeRevisions = append([]string{}, in.TreeRevisions...)
		}
		return &out, nil
	}
	return nil, nil
}

func (s *fakeSkillMaterializationLeaseStore) Renew(context.Context, string, string, string, time.Duration) (time.Time, error) {
	s.renewCalls.Add(1)
	if s.renewNotify != nil {
		select {
		case s.renewNotify <- struct{}{}:
		default:
		}
	}
	return time.Time{}, s.renewErr
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
		lease: &domain.SkillMaterializationLease{Token: "token-1", TreeRevisions: []string{"wft1_a", "wft1_b"}},
		calls: &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)
	deps.resolve = func(context.Context, store.Store, string, string) (*materializationPlan, error) {
		return &materializationPlan{TreeRevisions: []string{"wft1_a", "wft1_b"}}, nil
	}

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
	if !reflect.DeepEqual(got.TreeRevisions, []string{"wft1_a", "wft1_b"}) {
		t.Fatalf("acquire tree revisions = %v", got.TreeRevisions)
	}
	if leases.renewCalls.Load() != 1 {
		t.Fatalf("renew calls = %d, want final pre-commit renewal", leases.renewCalls.Load())
	}
}

func TestMaterializeLeasedAcquiresExactResolvedTreeRevisionSet(t *testing.T) {
	skills := &staticSkillStore{skills: []*skillFixture{
		{Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha skill", Content: "alpha body"},
		{Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "beta skill", Content: "beta body"},
	}}
	base := materializeStore{skills: skills, files: skills.workspaceFiles()}
	leases := &fakeSkillMaterializationLeaseStore{lease: &domain.SkillMaterializationLease{Token: "token-1"}, echoTrees: true}
	st := leasedMaterializeStore{Store: base, leases: leases}
	deps := testLeasedMaterializeDeps(new([]string))
	deps.resolve = resolveMaterializationPlan
	deps.apply = func(ctx context.Context, _ store.Store, _, _ string, plan *materializationPlan, beforeCommit beforeMaterializationCommit) error {
		if len(plan.Skills) != 2 {
			t.Fatalf("planned skills = %d, want 2", len(plan.Skills))
		}
		return beforeCommit(ctx)
	}

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatal(err)
	}
	if len(leases.acquires) != 1 || len(leases.acquires[0].TreeRevisions) != 2 {
		t.Fatalf("acquires = %+v", leases.acquires)
	}
	if !sort.StringsAreSorted(leases.acquires[0].TreeRevisions) || leases.acquires[0].TreeRevisions[0] == leases.acquires[0].TreeRevisions[1] {
		t.Fatalf("tree revisions = %v, want sorted distinct exact set", leases.acquires[0].TreeRevisions)
	}
}

func TestMaterializeLeasedEmptyProjectionStillAcquiresSerializationLease(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		lease: &domain.SkillMaterializationLease{Token: "empty-token", TreeRevisions: []string{}}, calls: &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)
	deps.resolve = func(context.Context, store.Store, string, string) (*materializationPlan, error) {
		return &materializationPlan{TreeRevisions: []string{}}, nil
	}

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatal(err)
	}
	if len(leases.acquires) != 1 || leases.acquires[0].TreeRevisions == nil || len(leases.acquires[0].TreeRevisions) != 0 {
		t.Fatalf("empty projection acquire = %+v", leases.acquires)
	}
}

func TestMaterializeLeasedRejectsMismatchedLeaseRevisionSetBeforeHydration(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		lease: &domain.SkillMaterializationLease{Token: "token-1", TreeRevisions: []string{"wft1_other"}}, calls: &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)
	deps.resolve = func(context.Context, store.Store, string, string) (*materializationPlan, error) {
		return &materializationPlan{TreeRevisions: []string{"wft1_expected"}}, nil
	}

	err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps)
	if !errors.Is(err, domain.ErrIntegrity) {
		t.Fatalf("error = %v, want integrity failure", err)
	}
	if want := []string{"acquire", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestMaterializeLeasedRejectsMissingOrNullLeaseRevisionSetAndReleases(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "missing", body: `{"token":"token-1"}`},
		{name: "null", body: `{"token":"token-1","tree_revisions":null}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var lease domain.SkillMaterializationLease
			if err := json.Unmarshal([]byte(tt.body), &lease); err != nil {
				t.Fatal(err)
			}
			leases := &fakeSkillMaterializationLeaseStore{
				lease: &lease,
				calls: &calls, preserveNilTrees: true,
			}
			st := leasedMaterializeStore{leases: leases}
			deps := testLeasedMaterializeDeps(&calls)
			deps.resolve = func(context.Context, store.Store, string, string) (*materializationPlan, error) {
				return &materializationPlan{TreeRevisions: []string{}}, nil
			}

			err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps)
			if !errors.Is(err, domain.ErrIntegrity) {
				t.Fatalf("error = %v, want integrity failure", err)
			}
			if want := []string{"acquire", "release"}; !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestMaterializeLeasedRejectsInvalidLeaseIdentityOrExpiryAndReleases(t *testing.T) {
	future := time.Now().Add(time.Minute)
	tests := []struct {
		name  string
		lease *domain.SkillMaterializationLease
	}{
		{name: "wrong target", lease: &domain.SkillMaterializationLease{Token: "token-1", TargetKey: "other-target", Holder: "lead@test-host#1234", ExpiresAt: future, TreeRevisions: []string{}}},
		{name: "wrong holder", lease: &domain.SkillMaterializationLease{Token: "token-1", Holder: "other@test-host#9", ExpiresAt: future, TreeRevisions: []string{}}},
		{name: "missing expiry", lease: &domain.SkillMaterializationLease{Token: "token-1", Holder: "lead@test-host#1234", TreeRevisions: []string{}}},
		{name: "expired", lease: &domain.SkillMaterializationLease{Token: "token-1", Holder: "lead@test-host#1234", ExpiresAt: time.Unix(1, 0), TreeRevisions: []string{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			leases := &fakeSkillMaterializationLeaseStore{
				lease: tt.lease, calls: &calls, preserveZeroExpiry: tt.name == "missing expiry",
			}
			st := leasedMaterializeStore{leases: leases}
			deps := testLeasedMaterializeDeps(&calls)

			err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps)
			if !errors.Is(err, domain.ErrIntegrity) {
				t.Fatalf("error = %v, want integrity failure", err)
			}
			if want := []string{"acquire", "release"}; !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		})
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

func TestMaterializeLeasedReresolvesExactPlanAfterContention(t *testing.T) {
	var calls []string
	conflict := &domain.SkillMaterializationLeaseConflictError{
		Holder: "other@test-host#9", ExpiresAt: time.Now().UTC().Add(time.Second),
	}
	leases := &fakeSkillMaterializationLeaseStore{
		acquireErrs: []error{conflict, nil},
		lease:       &domain.SkillMaterializationLease{Token: "token-after-wait"},
		echoTrees:   true,
		calls:       &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)
	resolveCalls := 0
	deps.resolve = func(context.Context, store.Store, string, string) (*materializationPlan, error) {
		resolveCalls++
		return &materializationPlan{TreeRevisions: []string{fmt.Sprintf("wft1_plan_%d", resolveCalls)}}, nil
	}
	deps.apply = func(ctx context.Context, _ store.Store, _, _ string, plan *materializationPlan, beforeCommit beforeMaterializationCommit) error {
		if want := []string{"wft1_plan_2"}; !reflect.DeepEqual(plan.TreeRevisions, want) {
			t.Fatalf("applied revisions = %v, want freshly resolved %v", plan.TreeRevisions, want)
		}
		return beforeCommit(ctx)
	}

	if err := materializeLeasedWith(t.Context(), st, "WS", "reviewer", t.TempDir(), deps); err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 2 {
		t.Fatalf("resolve calls = %d, want one resolution per acquire attempt", resolveCalls)
	}
	if len(leases.acquires) != 2 ||
		!reflect.DeepEqual(leases.acquires[0].TreeRevisions, []string{"wft1_plan_1"}) ||
		!reflect.DeepEqual(leases.acquires[1].TreeRevisions, []string{"wft1_plan_2"}) {
		t.Fatalf("acquire revisions = %+v, want plans 1 then 2", leases.acquires)
	}
}

func TestMaterializeLeasedFailsBeforeHydrationWhenLeaseStoreIsUnavailable(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		acquireErrs: []error{fmt.Errorf("redis down: %w", domain.ErrSkillMaterializationLeaseStoreUnavailable)},
		calls:       &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps)
	if !IsStoreUnavailable(err) {
		t.Fatalf("materializeLeasedWith error = %v, want StoreUnavailableError", err)
	}
	if want := []string{"acquire"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestMaterializeLeasedFailsBeforeHydrationOnLeaseTransportError(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		acquireErrs: []error{&url.Error{Op: "POST", URL: "http://fleet-db/leases", Err: syscall.ECONNREFUSED}},
		calls:       &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps)
	if !IsStoreUnavailable(err) {
		t.Fatalf("materializeLeasedWith error = %v, want StoreUnavailableError", err)
	}
	if want := []string{"acquire"}; !reflect.DeepEqual(calls, want) {
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

func TestMaterializeLeasedLeaseLossFailsBeforeCommitAndStillReleases(t *testing.T) {
	var calls []string
	leases := &fakeSkillMaterializationLeaseStore{
		lease:    &domain.SkillMaterializationLease{Token: "token-1"},
		renewErr: domain.ErrSkillMaterializationLeaseTokenMismatch,
		calls:    &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps)
	if !errors.Is(err, domain.ErrSkillMaterializationLeaseTokenMismatch) {
		t.Fatalf("materializeLeasedWith error = %v, want token mismatch", err)
	}
	if want := []string{"acquire", "materialize", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestMaterializeLeasedClassifiesRenewalStoreOutageAndPreservesProjection(t *testing.T) {
	var calls []string
	outage := &url.Error{Op: "PUT", URL: "http://fleet-db/leases/target", Err: syscall.ECONNREFUSED}
	leases := &fakeSkillMaterializationLeaseStore{
		lease: &domain.SkillMaterializationLease{Token: "token-1"}, renewErr: outage, calls: &calls,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)

	err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps)
	if !IsStoreUnavailable(err) || !errors.Is(err, outage) {
		t.Fatalf("error = %v, want StoreUnavailableError wrapping renewal outage", err)
	}
	if want := []string{"acquire", "materialize", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestMaterializeLeasedRenewsDuringSlowHydrationAndAgainBeforeCommit(t *testing.T) {
	var calls []string
	notified := make(chan struct{}, 1)
	leases := &fakeSkillMaterializationLeaseStore{
		lease: &domain.SkillMaterializationLease{Token: "token-1"}, calls: &calls, renewNotify: notified,
	}
	st := leasedMaterializeStore{leases: leases}
	deps := testLeasedMaterializeDeps(&calls)
	deps.renewEvery = time.Millisecond
	deps.apply = func(ctx context.Context, _ store.Store, _, _ string, _ *materializationPlan, beforeCommit beforeMaterializationCommit) error {
		calls = append(calls, "materialize")
		select {
		case <-notified:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("timed out waiting for background renewal")
		}
		return beforeCommit(ctx)
	}

	if err := materializeLeasedWith(t.Context(), st, "WS", "lead", t.TempDir(), deps); err != nil {
		t.Fatal(err)
	}
	if leases.renewCalls.Load() < 2 {
		t.Fatalf("renew calls = %d, want background plus final renewal", leases.renewCalls.Load())
	}
}

func testLeasedMaterializeDeps(calls *[]string) leasedMaterializeDeps {
	return leasedMaterializeDeps{
		hostname: func() (string, error) { return "test-host", nil },
		pid:      func() int { return 1234 },
		sleep:    func(context.Context, time.Duration) error { return nil },
		resolve: func(context.Context, store.Store, string, string) (*materializationPlan, error) {
			return &materializationPlan{TreeRevisions: []string{}}, nil
		},
		apply: func(ctx context.Context, _ store.Store, _, _ string, _ *materializationPlan, beforeCommit beforeMaterializationCommit) error {
			*calls = append(*calls, "materialize")
			if beforeCommit != nil {
				return beforeCommit(ctx)
			}
			return nil
		},
	}
}
