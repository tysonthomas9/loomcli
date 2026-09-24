//go:build darwin || linux

package local

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
)

func TestBrowserSessionRequestRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	reg := browserauth.NewOperatorSessionRegistry()
	srv, err := browserauth.ListenOperatorSocket(browserauth.OperatorSocketPath(dir), reg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx := context.Background()

	issued := browserSessionRequest(ctx, "issue", "ws", dir, nil)
	if !issued.OK || issued.Token == "" {
		t.Fatalf("issue = %+v", issued)
	}
	refreshed := browserSessionRequest(ctx, "refresh", "", dir, strings.NewReader(issued.Token+"\n"))
	if !refreshed.OK || refreshed.Token != "" {
		t.Fatalf("refresh = %+v", refreshed)
	}
	if r := browserSessionRequest(ctx, "revoke", "", dir, strings.NewReader(issued.Token)); !r.OK {
		t.Fatalf("revoke = %+v", r)
	}
	if reg.Len() != 0 {
		t.Fatal("session not revoked")
	}
}

func TestBrowserSessionRequestErrors(t *testing.T) {
	ctx := context.Background()
	if r := browserSessionRequest(ctx, "issue", "", t.TempDir(), nil); r.OK || r.Code != "bad_request" {
		t.Fatalf("missing workspace = %+v", r)
	}
	if r := browserSessionRequest(ctx, "refresh", "", t.TempDir(), strings.NewReader("")); r.OK || r.Code != "bad_request" {
		t.Fatalf("missing token = %+v", r)
	}
	if r := browserSessionRequest(ctx, "steal", "ws", t.TempDir(), nil); r.OK || r.Code != "bad_request" {
		t.Fatalf("bad action = %+v", r)
	}
	if r := browserSessionRequest(ctx, "issue", "ws", t.TempDir(), nil); r.OK || r.Code != "bridge_unavailable" {
		t.Fatalf("no runtime = %+v", r)
	}
}
