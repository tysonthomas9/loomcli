package terminal

import (
	"context"
	"sync"
	"testing"
	"time"

	sdktypes "github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

// hangingHandle parks in SendInput the way a half-open websocket does: the
// SDK sets no write deadline, so the write never returns.
type hangingHandle struct {
	data    chan []byte
	entered chan struct{}
	release chan struct{}
	kills   int
	onceEnt sync.Once
}

func (h *hangingHandle) DataChan() <-chan []byte { return h.data }
func (h *hangingHandle) SendInput([]byte) error {
	h.onceEnt.Do(func() { close(h.entered) })
	<-h.release
	return nil
}
func (h *hangingHandle) Resize(context.Context, int, int) (*sdktypes.PtySessionInfo, error) {
	return nil, nil
}
func (h *hangingHandle) Disconnect() error                       { close(h.data); return nil }
func (h *hangingHandle) WaitForConnection(context.Context) error { return nil }

// Close must not block behind an in-flight Write. If it does, PTYManager
// shutdown wedges and serve never exits.
func TestCloseDoesNotBlockBehindHungWrite(t *testing.T) {
	h := &hangingHandle{
		data:    make(chan []byte),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	u := newDaytonaPTYUpstream(h)
	_ = u.Output()

	go func() { _, _ = u.Write([]byte("keystroke")) }()
	<-h.entered // the write is now parked, exactly as a stalled socket would be

	done := make(chan struct{})
	go func() { _ = u.Close(); close(done) }()

	select {
	case <-done:
		// Close escaped the hung write, as it must.
	case <-time.After(3 * time.Second):
		t.Fatal("DEADLOCK: Close() blocked behind an in-flight Write; serve shutdown would wedge")
	}
	close(h.release)
}
