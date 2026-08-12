//go:build testbackend

package cli

// The echo backend is selected only by the explicit testbackend build profile.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestEchoBackend_Name(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}
	if got := eb.Name(); got != "echo" {
		t.Fatalf("Name() = %q, want %q", got, "echo")
	}
}

func TestEchoBackend_InvokeInteractive(t *testing.T) {
	var called bool
	handler := func(inv EchoInvocation, w io.Writer) error {
		called = true
		if inv.Mode != "interactive" {
			t.Errorf("Mode = %q, want %q", inv.Mode, "interactive")
		}
		_, _ = fmt.Fprintln(w, `{"type":"result","subtype":"success","result":"ok"}`)
		return nil
	}

	eb := &EchoBackend{handler: handler}
	err := eb.InvokeInteractive("/tmp/work", "hello", "agent1")
	if err != nil {
		t.Fatalf("InvokeInteractive returned error: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}

	invs := eb.Invocations()
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1", len(invs))
	}
	inv := invs[0]
	if inv.WorkDir != "/tmp/work" {
		t.Errorf("WorkDir = %q, want %q", inv.WorkDir, "/tmp/work")
	}
	if inv.Prompt != "hello" {
		t.Errorf("Prompt = %q, want %q", inv.Prompt, "hello")
	}
	if inv.AgentName != "agent1" {
		t.Errorf("AgentName = %q, want %q", inv.AgentName, "agent1")
	}
	if inv.Mode != "interactive" {
		t.Errorf("Mode = %q, want %q", inv.Mode, "interactive")
	}
}

func TestEchoBackend_InvokeNonInteractive(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}
	shutdown := make(chan struct{})
	err := eb.InvokeNonInteractive("/tmp/work", "do stuff", "bot", shutdown, nil)
	if err != nil {
		t.Fatalf("InvokeNonInteractive returned error: %v", err)
	}

	invs := eb.Invocations()
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1", len(invs))
	}
	if invs[0].Mode != "non-interactive" {
		t.Errorf("Mode = %q, want %q", invs[0].Mode, "non-interactive")
	}
}

func TestEchoBackend_InvokeStreaming(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}
	rc, err := eb.InvokeStreaming(context.Background(), "/tmp/work", "stream me", "streamer")
	if err != nil {
		t.Fatalf("InvokeStreaming returned error: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty output from streaming")
	}

	invs := eb.Invocations()
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1", len(invs))
	}
	if invs[0].Mode != "streaming" {
		t.Errorf("Mode = %q, want %q", invs[0].Mode, "streaming")
	}
}

func TestEchoBackend_HealthCheck(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}
	hs := eb.HealthCheck()
	if !hs.Healthy {
		t.Errorf("Healthy = false, want true")
	}
	if !hs.Installed {
		t.Errorf("Installed = false, want true")
	}
	if hs.Message != "ready" {
		t.Errorf("Message = %q, want %q", hs.Message, "ready")
	}
}

func TestEchoBackend_Reset(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}

	// Generate some invocations.
	_ = eb.InvokeInteractive("/tmp", "p1", "a1")
	_ = eb.InvokeInteractive("/tmp", "p2", "a2")
	if got := len(eb.Invocations()); got != 2 {
		t.Fatalf("before Reset: got %d invocations, want 2", got)
	}

	eb.Reset()
	if got := len(eb.Invocations()); got != 0 {
		t.Fatalf("after Reset: got %d invocations, want 0", got)
	}
}

func TestEchoBackend_ErrorHandler(t *testing.T) {
	sentinel := errors.New("test error")
	eb := &EchoBackend{handler: ErrorHandler(sentinel)}

	err := eb.InvokeInteractive("/tmp", "fail", "agent")
	if !errors.Is(err, sentinel) {
		t.Fatalf("got error %v, want %v", err, sentinel)
	}

	// Invocation should still be recorded even when handler errors.
	if got := len(eb.Invocations()); got != 1 {
		t.Fatalf("got %d invocations, want 1", got)
	}
}

func TestEchoBackend_SequenceHandler(t *testing.T) {
	var order []int
	var mu sync.Mutex
	makeHandler := func(id int) EchoHandler {
		return func(_ EchoInvocation, w io.Writer) error {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			_, _ = fmt.Fprintln(w, `{"type":"result","subtype":"success","result":"ok"}`)
			return nil
		}
	}

	seq := SequenceHandler(makeHandler(1), makeHandler(2), makeHandler(3))
	eb := &EchoBackend{handler: seq}

	for i := 0; i < 5; i++ {
		if err := eb.InvokeInteractive("/tmp", fmt.Sprintf("p%d", i), "a"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// First 3 calls should cycle through 1, 2, 3; remaining calls reuse 3.
	expected := []int{1, 2, 3, 3, 3}
	if len(order) != len(expected) {
		t.Fatalf("got %d handler calls, want %d", len(order), len(expected))
	}
	for i, want := range expected {
		if order[i] != want {
			t.Errorf("call %d: handler %d invoked, want %d", i, order[i], want)
		}
	}
}

func TestEchoBackend_CountingHandler(t *testing.T) {
	var counter atomic.Int32
	eb := &EchoBackend{handler: CountingHandler(&counter, DefaultEchoHandler)}

	for i := 0; i < 3; i++ {
		if err := eb.InvokeInteractive("/tmp", "p", "a"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if got := counter.Load(); got != 3 {
		t.Fatalf("counter = %d, want 3", got)
	}
}

func TestEchoBackend_SetHandler(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}

	called := false
	eb.SetHandler(func(_ EchoInvocation, w io.Writer) error {
		called = true
		_, _ = fmt.Fprintln(w, `{"type":"result","subtype":"success","result":"custom"}`)
		return nil
	})

	if err := eb.InvokeInteractive("/tmp", "p", "a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("custom handler was not called after SetHandler")
	}
}

func TestEchoBackend_UsageHandler(t *testing.T) {
	eb := &EchoBackend{handler: UsageHandler(500, 100)}
	collector := usage.NewCollector("echo", "test-agent")

	shutdown := make(chan struct{})
	if err := eb.InvokeNonInteractive("/tmp", "p", "a", shutdown, collector); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := time.Now()
	su := collector.Finalize("task-1", "", now, now, 0)
	if su.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", su.InputTokens)
	}
	if su.OutputTokens != 100 {
		t.Errorf("OutputTokens = %d, want 100", su.OutputTokens)
	}
}

func TestEchoBackend_Meta(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}
	meta := eb.Meta()
	if meta.DisplayName != "Echo" {
		t.Errorf("Meta().DisplayName = %q, want %q", meta.DisplayName, "Echo")
	}
}

func TestEchoBackend_ConcurrentInvocations(t *testing.T) {
	eb := &EchoBackend{handler: DefaultEchoHandler}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			_ = eb.InvokeInteractive("/tmp", fmt.Sprintf("prompt-%d", n), fmt.Sprintf("agent-%d", n))
		}(i)
	}

	wg.Wait()

	invs := eb.Invocations()
	if len(invs) != goroutines {
		t.Fatalf("got %d invocations, want %d", len(invs), goroutines)
	}
}
