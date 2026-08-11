package terminal

import (
	"bytes"
	"context"
	"testing"
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
