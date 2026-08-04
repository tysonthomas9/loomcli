package netutil

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestPickFreeLoopbackPortReturnsBindableAddress(t *testing.T) {
	host, port, err := PickFreeLoopbackPort()
	if err != nil {
		t.Fatalf("PickFreeLoopbackPort() error = %v", err)
	}
	if port <= 0 {
		t.Fatalf("PickFreeLoopbackPort() port = %d, want positive", port)
	}
	if want := "127.0.0.1:" + strconv.Itoa(port); host != want {
		t.Fatalf("PickFreeLoopbackPort() host = %q, want %q", host, want)
	}

	l, err := net.Listen("tcp", host)
	if err != nil {
		t.Fatalf("returned host was not bindable: %v", err)
	}
	_ = l.Close()
}

func TestWaitForHealthzReturnsOnOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("request path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := WaitForHealthz(ctx, srv.URL, time.Second); err != nil {
		t.Fatalf("WaitForHealthz() error = %v", err)
	}
}

func TestWaitForHealthzReturnsLastStatusOnDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	err := WaitForHealthz(ctx, srv.URL, time.Second)
	if err == nil {
		t.Fatal("WaitForHealthz() error = nil, want deadline error")
	}
}
