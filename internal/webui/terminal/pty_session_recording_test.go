package terminal

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAttachSnapshotIsAtomicWithReplayAndRecorderCoordinate(t *testing.T) {
	for iteration := 0; iteration < 12; iteration++ {
		root := t.TempDir()
		key := SessionKey{Workspace: "ws", Name: "atomic-attach"}
		store := NewRecordingStore(root, nil)
		recorder, err := store.StartRecording(key, 8, 2)
		if err != nil {
			t.Fatalf("iteration %d StartRecording: %v", iteration, err)
		}
		session := newPtySession(key, nil, nil, recorder)
		session.publishOutput([]byte("one\r\ntwo"), nil)
		_ = recorder.FirstScreenLine()

		start := make(chan struct{})
		attached := make(chan *localAttachment, 1)
		published := make(chan struct{})
		go func() {
			<-start
			attached <- session.attachNew("conn")
		}()
		go func() {
			<-start
			snapshot := session.publishOutput([]byte("\r\nthree"), nil)
			for _, attachment := range snapshot {
				attachment.send([]byte("\r\nthree"))
			}
			close(published)
		}()
		close(start)
		attachment := <-attached
		<-published
		if attachment == nil {
			t.Fatalf("iteration %d attach returned nil", iteration)
		}

		combined := append([]byte(nil), attachment.Scrollback()...)
		select {
		case live := <-attachment.Output():
			combined = append(combined, live...)
		default:
		}
		if got := bytes.Count(combined, []byte("three")); got != 1 {
			t.Fatalf("iteration %d replay+live occurrence count = %d, want 1; bytes=%q", iteration, got, combined)
		}
		if bytes.Contains(attachment.Scrollback(), []byte("three")) {
			if got := attachment.FirstScreenLine(); got != 1 {
				t.Fatalf("iteration %d replay contains new chunk but firstScreenLine=%d, want 1", iteration, got)
			}
		} else if got := attachment.FirstScreenLine(); got != 0 {
			t.Fatalf("iteration %d replay excludes new chunk but firstScreenLine=%d, want 0", iteration, got)
		}

		if err := recorder.Close(); err != nil {
			t.Fatalf("iteration %d Close: %v", iteration, err)
		}
		history, err := store.History(context.Background(), key, 0, 10)
		if err != nil {
			t.Fatalf("iteration %d History: %v", iteration, err)
		}
		if history.TotalLines != 3 {
			t.Fatalf("iteration %d total lines = %d, want 3", iteration, history.TotalLines)
		}
	}
}

func TestSessionRecorderAppendObservedOpenCannotBeLostToClose(t *testing.T) {
	// Zero grace: this test is about the Append/Close race, not the
	// trivial-recording discard, which would otherwise drop the generation
	// and its history before the assertion below.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 0
	defer func() { recordingStartedGrace = oldGrace }()
	root := t.TempDir()
	key := SessionKey{Workspace: "ws", Name: "append-close"}
	store := NewRecordingStore(root, nil)
	recorder, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}

	observedOpen := make(chan struct{})
	releaseAppend := make(chan struct{})
	recorder.beforeEnqueue = func() {
		close(observedOpen)
		<-releaseAppend
	}
	appendDone := make(chan struct{})
	go func() {
		recorder.Append([]byte("racing chunk"))
		close(appendDone)
	}()
	<-observedOpen
	closeDone := make(chan error, 1)
	go func() { closeDone <- recorder.Close() }()
	close(releaseAppend)
	<-appendDone
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}

	history, err := store.History(context.Background(), key, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if history.TotalLines != 1 || len(history.Lines) != 1 || lineText(history.Lines[0]) != "racing chunk" {
		t.Fatalf("racing chunk was neither recorded nor represented as a gap: history=%#v", history)
	}
}

func TestAttachDoesNotHoldAttachLockWhileWaitingForRecorderWorker(t *testing.T) {
	store := NewRecordingStore(t.TempDir(), nil)
	key := SessionKey{Workspace: "ws", Name: "attach-worker-wait"}
	recorder, err := store.StartRecording(key, 8, 2)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	session := newPtySession(key, nil, nil, recorder)

	blockedResponse := make(chan recorderSnapshot)
	queryAccepted := make(chan struct{})
	go func() {
		recorder.query <- recorderQuery{response: blockedResponse}
		close(queryAccepted)
	}()
	<-queryAccepted

	attached := make(chan *localAttachment, 1)
	go func() { attached <- session.attachNew("conn") }()
	deadline := time.Now().Add(time.Second)
	for {
		if !session.attachMu.TryLock() {
			break
		}
		_, registered := session.attaches["conn"]
		session.attachMu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("attach did not reach its recorder wait")
		}
		runtime.Gosched()
	}

	published := make(chan struct{})
	go func() {
		session.publishOutput([]byte("still-draining"), nil)
		close(published)
	}()
	publishedBeforeWorkerRelease := false
	select {
	case <-published:
		publishedBeforeWorkerRelease = true
	case <-time.After(100 * time.Millisecond):
	}
	<-blockedResponse
	attachment := <-attached
	if !publishedBeforeWorkerRelease {
		<-published
		t.Error("PTY output blocked behind attachMu while attach waited for recorder worker")
	}
	if attachment == nil {
		t.Fatal("attach returned nil")
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRecorderRebaselinesFromReplayRingAfterDroppedChunks(t *testing.T) {
	store := NewRecordingStore(t.TempDir(), nil)
	store.SetQueueSizeForTest(1)
	key := SessionKey{Workspace: "ws", Name: "gap-rebaseline"}
	recorder, err := store.StartRecording(key, 12, 2)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	session := newPtySession(key, nil, nil, recorder)

	blockedResponse := make(chan recorderSnapshot)
	queryAccepted := make(chan struct{})
	go func() {
		recorder.query <- recorderQuery{response: blockedResponse}
		close(queryAccepted)
	}()
	<-queryAccepted
	session.publishOutput([]byte("one\r\n"), nil)
	session.publishOutput([]byte("two\r\n"), nil)
	session.publishOutput([]byte("three\r\n"), nil)
	<-blockedResponse
	_ = recorder.FirstScreenLine()

	session.publishOutput([]byte("four\r\nfive"), nil)
	_ = recorder.FirstScreenLine()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	history, err := store.History(t.Context(), key, 0, 100)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	text := recordingLinesText(history.Lines)
	if !strings.Contains(text, "terminal history gap") {
		t.Fatalf("history has no gap marker: %q", text)
	}
	if !strings.Contains(text, "five") {
		t.Fatalf("history never resumed after gap: %q", text)
	}
	meta, _, _, err := store.Meta(t.Context(), key)
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if meta.Gaps == 0 || !meta.HistoryLimited {
		t.Fatalf("gap metadata = %#v", meta)
	}
	if got := strings.Count(text, "terminal history gap"); got != 1 {
		t.Fatalf("gap markers before recovery = %d, want 1: %q", got, text)
	}

	markRecordingOpenForRecoveryTest(t, recorder.dir)
	restarted := NewRecordingStore(store.root, nil)
	recovered, err := restarted.History(t.Context(), key, 0, 100)
	if err != nil {
		t.Fatalf("recover History: %v", err)
	}
	recoveredText := recordingLinesText(recovered.Lines)
	if got := strings.Count(recoveredText, "terminal history gap"); got != 1 {
		t.Fatalf("gap markers after recovery = %d, want 1: %q", got, recoveredText)
	}
}
