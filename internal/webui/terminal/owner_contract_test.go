package terminal

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

type fakePTYOperation struct {
	kind       string
	data       []byte
	cols, rows uint16
}

type pipeBackedPTY struct {
	reader *os.File
	output *os.File
	ops    chan fakePTYOperation
	close  sync.Once
}

func newPipeBackedPTY(t *testing.T) *pipeBackedPTY {
	t.Helper()
	reader, output, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	return &pipeBackedPTY{
		reader: reader,
		output: output,
		ops:    make(chan fakePTYOperation, 64),
	}
}

func (p *pipeBackedPTY) Read(data []byte) (int, error) { return p.reader.Read(data) }
func (p *pipeBackedPTY) Write(data []byte) (int, error) {
	copyData := append([]byte(nil), data...)
	p.ops <- fakePTYOperation{kind: "input", data: copyData}
	return len(data), nil
}
func (p *pipeBackedPTY) SetSize(cols, rows uint16) error {
	p.ops <- fakePTYOperation{kind: "resize", cols: cols, rows: rows}
	return nil
}
func (p *pipeBackedPTY) Close() error {
	var err error
	p.close.Do(func() { err = p.reader.Close() })
	return err
}
func (p *pipeBackedPTY) writeOutput(data []byte) error {
	_, err := p.output.Write(data)
	return err
}
func (p *pipeBackedPTY) closeOutput() { _ = p.output.Close() }

func newPipeSession(t *testing.T) (*ptySession, *pipeBackedPTY) {
	t.Helper()
	device := newPipeBackedPTY(t)
	session, err := newPtySession(SessionKey{Workspace: "test", Name: t.Name()}, device, nil, 80, 24)
	if err != nil {
		t.Fatalf("newPtySession: %v", err)
	}
	session.start(nil)
	return session, device
}

// TestAttachmentContract exercises 1,000 attach cuts while a pipe-backed PTY
// is streaming. The interim reset prefix is terminal state setup, so the
// stream comparison removes it before combining state bytes with live output.
func TestAttachmentContract(t *testing.T) {
	random := rand.New(rand.NewSource(0x5eed)) //nolint:gosec // deterministic scheduling coverage
	const (
		iterations = 1000
		chunks     = 16
	)

	for iteration := 0; iteration < iterations; iteration++ {
		session, device := newPipeSession(t)
		parts := make([][]byte, chunks)
		var exact []byte
		for index := range parts {
			parts[index] = []byte(fmt.Sprintf("%04d/%02d|", iteration, index))
			exact = append(exact, parts[index]...)
		}

		progress := make(chan int, chunks)
		writeErr := make(chan error, 1)
		go func() {
			for index, part := range parts {
				if err := device.writeOutput(part); err != nil {
					writeErr <- err
					return
				}
				progress <- index + 1
				runtime.Gosched()
			}
			device.closeOutput()
			writeErr <- nil
		}()

		attachAfter := random.Intn(chunks + 1)
		for written := 0; written < attachAfter; written++ {
			<-progress
		}
		for yields := random.Intn(4); yields > 0; yields-- {
			runtime.Gosched()
		}
		att := session.attachNew("contract", 80, 24)
		if att == nil {
			t.Fatalf("iteration %d: attach returned nil", iteration)
		}
		if err := <-writeErr; err != nil {
			t.Fatalf("iteration %d: write output: %v", iteration, err)
		}
		<-session.readerDone
		if err := att.Focus(); err != nil { // owner barrier after the final read
			t.Fatalf("iteration %d: owner barrier: %v", iteration, err)
		}

		initial := att.InitialState()
		reconstructed := append([]byte(nil), initial.Data...)
		if len(reconstructed) > 0 {
			if !bytes.HasPrefix(reconstructed, screenResetSeq) {
				t.Fatalf("iteration %d: interim state missing reset prefix", iteration)
			}
			reconstructed = reconstructed[len(screenResetSeq):]
		}
		sequence := initial.Sequence
		receivedLive := false
		deadline := time.After(2 * time.Second)
		for !receivedLive || len(reconstructed) < len(exact) {
			select {
			case event, ok := <-att.Output():
				if !ok {
					t.Fatalf("iteration %d: output closed at sequence %d", iteration, sequence)
				}
				receivedLive = true
				if event.Sequence != sequence+1 {
					t.Fatalf("iteration %d: sequence = %d, want %d", iteration, event.Sequence, sequence+1)
				}
				sequence = event.Sequence
				if event.Kind == EventOutput {
					reconstructed = append(reconstructed, event.Data...)
				}
			case <-deadline:
				t.Fatalf("iteration %d: timed out at %d/%d bytes", iteration, len(reconstructed), len(exact))
			}
		}
		if !bytes.Equal(reconstructed, exact) {
			t.Fatalf("iteration %d: reconstructed %q, want %q", iteration, reconstructed, exact)
		}

		session.detach(att.ConnID())
		if err := session.close(ExitReasonShutdown); err != nil {
			t.Fatalf("iteration %d: close session: %v", iteration, err)
		}
	}
}

func TestSlowConsumerIsClosedWithoutAffectingFastSubscriber(t *testing.T) {
	session, device := newPipeSession(t)
	slow := session.attachNew("slow", 80, 24)
	fast := session.attachNew("fast", 80, 24)
	if slow == nil || fast == nil {
		t.Fatal("attach returned nil")
	}

	const chunks = subscriberQueueBytes/terminalReadBufferSize + 8
	exact := bytes.Repeat([]byte{'x'}, chunks*terminalReadBufferSize)
	fastData := make(chan []byte, 1)
	go func() {
		var got []byte
		for event := range fast.Output() {
			if event.Kind == EventOutput {
				got = append(got, event.Data...)
			}
			if len(got) == len(exact) {
				fastData <- got
				return
			}
		}
		fastData <- got
	}()

	writeDone := make(chan error, 1)
	go func() {
		for offset := 0; offset < len(exact); offset += terminalReadBufferSize {
			if err := device.writeOutput(exact[offset : offset+terminalReadBufferSize]); err != nil {
				writeDone <- err
				return
			}
		}
		device.closeOutput()
		writeDone <- nil
	}()
	if err := <-writeDone; err != nil {
		t.Fatalf("write output: %v", err)
	}
	<-session.readerDone
	if err := fast.Focus(); err != nil {
		t.Fatalf("owner barrier: %v", err)
	}

	select {
	case got := <-fastData:
		if !bytes.Equal(got, exact) {
			t.Fatalf("fast subscriber got %d bytes, want %d", len(got), len(exact))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fast subscriber stalled behind slow subscriber")
	}
	for range slow.Output() {
	}
	if got := slow.CloseReason(); got != CloseSlowConsumer {
		t.Fatalf("slow CloseReason = %q, want %q", got, CloseSlowConsumer)
	}
	if got := session.attachmentCount(); got != 1 {
		t.Fatalf("attachment count = %d, want fast subscriber only", got)
	}

	session.detach(fast.ConnID())
	if err := session.close(ExitReasonShutdown); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func TestControllerFocusGeometryAndWriterFIFO(t *testing.T) {
	session, device := newPipeSession(t)
	first := session.attachNew("first", 80, 24)
	second := session.attachNew("second", 100, 30)
	if first == nil || second == nil {
		t.Fatal("attach returned nil")
	}
	assertPTYOperation(t, device, fakePTYOperation{kind: "resize", cols: 80, rows: 24})

	if err := second.RequestResize(110, 40); err != nil {
		t.Fatalf("non-controller resize request: %v", err)
	}
	if n, err := second.WriteInput([]byte("dropped")); err != nil || n != 0 {
		t.Fatalf("non-controller WriteInput = (%d, %v), want (0, nil)", n, err)
	}
	assertNoPTYOperation(t, device)

	if n, err := first.WriteInput([]byte("first-input")); err != nil || n != len("first-input") {
		t.Fatalf("controller WriteInput = (%d, %v)", n, err)
	}
	if err := second.Focus(); err != nil {
		t.Fatalf("Focus second: %v", err)
	}
	assertPTYOperation(t, device, fakePTYOperation{kind: "input", data: []byte("first-input")})
	assertPTYOperation(t, device, fakePTYOperation{kind: "resize", cols: 110, rows: 40})

	if n, err := first.WriteInput([]byte("also-dropped")); err != nil || n != 0 {
		t.Fatalf("old controller WriteInput = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := second.WriteInput([]byte("second-input")); err != nil || n != len("second-input") {
		t.Fatalf("focused controller WriteInput = (%d, %v)", n, err)
	}
	assertPTYOperation(t, device, fakePTYOperation{kind: "input", data: []byte("second-input")})

	if err := first.RequestResize(90, 20); err != nil {
		t.Fatalf("stored resize first: %v", err)
	}
	assertNoPTYOperation(t, device)
	if empty := session.detach(second.ConnID()); empty {
		t.Fatal("detach controller reported empty with first still attached")
	}
	assertPTYOperation(t, device, fakePTYOperation{kind: "resize", cols: 90, rows: 20})

	session.detach(first.ConnID())
	device.closeOutput()
	if err := session.close(ExitReasonShutdown); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func TestWriterFIFOOverflowDropsInputAndEmitsNotice(t *testing.T) {
	device := newPipeBackedPTY(t)
	session, err := newPtySession(
		SessionKey{Workspace: "test", Name: t.Name()},
		device,
		nil,
		80,
		24,
	)
	if err != nil {
		t.Fatalf("newPtySession: %v", err)
	}
	// Keep the writer stopped so the test can deterministically fill its FIFO.
	go session.runOwner()
	att := session.attachNew("controller", 80, 24)
	if att == nil {
		t.Fatal("attach returned nil")
	}

	fill := bytes.Repeat([]byte{'i'}, writerQueueBytes-2*resizeItemBytes)
	if n, writeErr := att.WriteInput(fill); writeErr != nil || n != len(fill) {
		t.Fatalf("fill WriteInput = (%d, %v), want (%d, nil)", n, writeErr, len(fill))
	}
	if n, writeErr := att.WriteInput([]byte("overflow")); writeErr != nil || n != 0 {
		t.Fatalf("overflow WriteInput = (%d, %v), want (0, nil)", n, writeErr)
	}

	firstEvent := <-att.Output()
	if firstEvent.Kind != EventResize {
		t.Fatalf("first event kind = %d, want resize", firstEvent.Kind)
	}
	notice := <-att.Output()
	if notice.Sequence != firstEvent.Sequence+1 || notice.Kind != EventNotice {
		t.Fatalf("notice = %#v, want sequence %d notice", notice, firstEvent.Sequence+1)
	}
	if got, want := string(notice.Data), `{"code":"input_dropped"}`; got != want {
		t.Fatalf("notice data = %q, want %q", got, want)
	}

	session.detach(att.ConnID())
	device.closeOutput()
	if err := session.close(ExitReasonShutdown); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func assertPTYOperation(t *testing.T, device *pipeBackedPTY, want fakePTYOperation) {
	t.Helper()
	select {
	case got := <-device.ops:
		if got.kind != want.kind || got.cols != want.cols || got.rows != want.rows || !bytes.Equal(got.data, want.data) {
			t.Fatalf("PTY operation = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for PTY operation %#v", want)
	}
}

func assertNoPTYOperation(t *testing.T, device *pipeBackedPTY) {
	t.Helper()
	select {
	case got := <-device.ops:
		t.Fatalf("unexpected PTY operation: %#v", got)
	case <-time.After(20 * time.Millisecond):
	}
}
