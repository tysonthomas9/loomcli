package terminal

// Session-history lifecycle for a recording: the started hook is deferred
// until the recording proves non-trivial, and recordings that end before
// proving useful are discarded without ever firing a hook (see
// recordingStartedGrace).

func (r *SessionRecorder) prepareLifecycle() {
	r.lifecycleMu.Lock()
	if r.lifecycleStarted == nil {
		r.lifecycleStarted = make(chan struct{})
	}
	r.lifecycleMu.Unlock()
}

func (r *SessionRecorder) startLifecycle() {
	r.lifecycleMu.Lock()
	started := r.lifecycleStarted
	fired := r.lifecycleFired
	r.lifecycleFired = started != nil
	r.lifecycleMu.Unlock()
	if started == nil || fired {
		return
	}
	go func() {
		r.store.recordingStarted(r.key, r.dir, r.startMeta)
		close(started)
	}()
}

// maybeStartLifecycle fires the started hook once the recording has proven
// non-trivial: it has committed at least one durable line, or the session
// has outlived recordingStartedGrace. Deferring the hook keeps stillborn
// sessions (crash loops, instant exits) out of the session-history store
// entirely. Runs on the recorder worker.
func (r *SessionRecorder) maybeStartLifecycle() {
	if r.meta.LineCount == 0 && unixMilliNow()-r.meta.StartedAt < recordingStartedGrace.Milliseconds() {
		return
	}
	r.startLifecycle()
}

// trivialRecording reports whether this recording ended before proving
// useful: the started hook never fired, nothing was committed, no issue is
// associated, and the session died within recordingStartedGrace.
func (r *SessionRecorder) trivialRecording() bool {
	r.lifecycleMu.Lock()
	fired := r.lifecycleFired
	r.lifecycleMu.Unlock()
	if fired {
		return false
	}
	return r.meta.LineCount == 0 && r.meta.IssueID == "" &&
		unixMilliNow()-r.meta.StartedAt < recordingStartedGrace.Milliseconds()
}

func (r *SessionRecorder) completeLifecycle() {
	r.lifecycleMu.Lock()
	started := r.lifecycleStarted
	r.lifecycleMu.Unlock()
	go func() {
		if started != nil {
			<-started
		}
		meta, err := readRecordingMeta(r.dir)
		if err != nil {
			meta = r.meta
		}
		r.store.recordingCompleted(r.key, r.dir, meta)
	}()
}

func (r *SessionRecorder) setIssueID(issueID string) error {
	if r == nil || issueID == "" {
		return nil
	}
	if r.closed.Load() {
		<-r.done
		return persistRecordingIssueID(r.dir, issueID)
	}
	response := make(chan error, 1)
	select {
	case r.update <- recorderUpdate{issueID: issueID, response: response}:
		return <-response
	case <-r.done:
		return persistRecordingIssueID(r.dir, issueID)
	}
}
