package stackstore

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/stackstore/stackwire"
)

func TestMapPublishLeaseErr(t *testing.T) {
	t.Parallel()
	busy := &domain.StackPublishLeaseBusyError{Holder: "h", Generation: 2}
	cases := []struct {
		name string
		in   error
		want error
	}{
		{name: "nil", in: nil, want: nil},
		{name: "busy", in: busy, want: domain.ErrStackPublishLeaseBusy},
		{name: "unavailable", in: domain.ErrStackPublishLeaseStoreUnavailable, want: domain.ErrStackPublishLeaseStoreUnavailable},
		{name: "token mismatch", in: domain.ErrStackPublishLeaseTokenMismatch, want: domain.ErrStackPublishLeaseTokenMismatch},
		{name: "already lost", in: domain.ErrStackPublishLeaseLost, want: domain.ErrStackPublishLeaseLost},
		{name: "gone", in: domain.ErrGone, want: domain.ErrStackPublishLeaseLost},
		{name: "not found", in: domain.ErrNotFound, want: domain.ErrStackPublishLeaseStoreUnavailable},
		{name: "transport", in: errors.New("dial tcp"), want: domain.ErrStackPublishLeaseStoreUnavailable},
		{
			name: "api 503",
			in:   &stackwire.APIError{Status: http.StatusServiceUnavailable, Code: "stack_publish_lease_store_unavailable", Err: errors.New("503")},
			want: domain.ErrStackPublishLeaseStoreUnavailable,
		},
		{
			name: "api 410",
			in:   &stackwire.APIError{Status: http.StatusGone, Code: "lease_expired", Err: domain.ErrGone},
			want: domain.ErrStackPublishLeaseLost,
		},
		{
			name: "api 404",
			in:   &stackwire.APIError{Status: http.StatusNotFound, Code: "not_found", Err: domain.ErrNotFound},
			want: domain.ErrStackPublishLeaseStoreUnavailable,
		},
		{
			name: "api token mismatch",
			in:   &stackwire.APIError{Status: http.StatusConflict, Code: "stack_publish_lease_token_mismatch", Err: domain.ErrStackPublishLeaseTokenMismatch},
			want: domain.ErrStackPublishLeaseTokenMismatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapPublishLeaseErr(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMapPublishLeaseErrPreservesBusyMeta(t *testing.T) {
	t.Parallel()
	in := &domain.StackPublishLeaseBusyError{Holder: "other@host#1", Generation: 9}
	got := mapPublishLeaseErr(fmt.Errorf("wrap: %w", in))
	var busy *domain.StackPublishLeaseBusyError
	if !errors.As(got, &busy) || busy.Holder != "other@host#1" || busy.Generation != 9 {
		t.Fatalf("busy meta lost: %#v", got)
	}
}

func TestAdmissionFromWireFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := admissionFromWire(nil); !errors.Is(err, domain.ErrStackPublishLeaseStoreUnavailable) {
		t.Fatalf("nil lease: %v", err)
	}
	if _, err := admissionFromWire(&stackwire.PublishLease{}); !errors.Is(err, domain.ErrStackPublishLeaseStoreUnavailable) {
		t.Fatalf("empty token: %v", err)
	}
	got, err := admissionFromWire(&stackwire.PublishLease{Token: "t", Holder: "h", Generation: 1})
	if err != nil || got.Token != "t" {
		t.Fatalf("valid grant: got=%+v err=%v", got, err)
	}
}
