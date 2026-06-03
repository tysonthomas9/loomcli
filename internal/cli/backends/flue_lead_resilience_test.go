package backends

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFlueLeadPrompt_ConnectionDropMidStream simulates the flue server crashing
// mid-turn: the connection is reset after a partial SSE frame. The client must
// not hang or panic; it surfaces the drop (or at least returns promptly).
func TestFlueLeadPrompt_ConnectionDropMidStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = bufrw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n")
		_, _ = bufrw.WriteString("event: text_delta\ndata: {\"type\":\"text_delta\",\"text\":\"partial answer\"}\n\n")
		_ = bufrw.Flush()
		// Force a RST so the client sees a read error rather than clean EOF.
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- flueLeadPrompt(context.Background(), srv.URL, "ws-x", "hi", &out)
	}()

	select {
	case err := <-done:
		// We don't require an error (a clean partial is acceptable), but it must
		// not hang or panic, and any streamed text should be visible.
		t.Logf("returned err=%v, streamed=%q", err, out.String())
		if !strings.Contains(out.String(), "partial answer") && err == nil {
			t.Fatal("expected either partial output or an error on a dropped stream")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("flueLeadPrompt hung after the connection dropped")
	}
}

// TestFlueLeadPrompt_ContextCancellation verifies a turn aborts promptly when
// the context is cancelled (e.g. shutdown), rather than blocking forever on a
// server that never goes idle.
func TestFlueLeadPrompt_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			_, _ = fmt.Fprint(w, "event: text_delta\ndata: {\"type\":\"text_delta\",\"text\":\"working...\"}\n\n")
			fl.Flush()
		}
		<-r.Context().Done() // never sends idle; hang until the client goes away
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	var out strings.Builder
	start := time.Now()
	err := flueLeadPrompt(ctx, srv.URL, "ws-x", "hi", &out)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("did not abort promptly on cancel (%v)", time.Since(start))
	}
	if err == nil {
		t.Fatal("expected a context-cancellation error")
	}
}
