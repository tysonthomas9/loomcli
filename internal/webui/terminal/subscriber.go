package terminal

import "sync"

const subscriberQueueBytes = 256 * 1024

type subscriber struct {
	connID     string
	cols       uint16
	rows       uint16
	attachedAt uint64
	output     chan TerminalEvent
	wake       chan struct{}
	closedCh   chan struct{}
	stopCh     chan struct{}
	closedOnce sync.Once
	stopOnce   sync.Once

	mu          sync.Mutex
	queue       []TerminalEvent
	queuedBytes int
	closed      bool
	closing     bool
	closeReason CloseReason
}

func newSubscriber(connID string, cols, rows uint16, attachedAt uint64) *subscriber {
	s := &subscriber{
		connID:     connID,
		cols:       cols,
		rows:       rows,
		attachedAt: attachedAt,
		output:     make(chan TerminalEvent),
		wake:       make(chan struct{}, 1),
		closedCh:   make(chan struct{}),
		stopCh:     make(chan struct{}),
	}
	go s.pump()
	return s
}

// enqueue accounts only event payload bytes, matching the transport contract.
// The public output channel is unbuffered and the pump retains the head event
// in queue until the handler receives it, so queuedBytes is decremented at the
// actual dequeue boundary rather than when the pump merely begins a send.
func (s *subscriber) enqueue(event TerminalEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.queuedBytes+len(event.Data) > subscriberQueueBytes {
		return false
	}
	s.queue = append(s.queue, event)
	s.queuedBytes += len(event.Data)
	s.signal()
	return true
}

func (s *subscriber) closeImmediate(reason CloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.closeReason = reason
	s.closedOnce.Do(func() { close(s.closedCh) })
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.signal()
}

func (s *subscriber) closeAfterQueue(reason CloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.closing {
		return
	}
	s.closing = true
	s.closeReason = reason
	if len(s.queue) == 0 {
		s.closedOnce.Do(func() { close(s.closedCh) })
	}
	s.signal()
}

func (s *subscriber) reason() CloseReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeReason
}

func (s *subscriber) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *subscriber) pump() {
	defer func() {
		close(s.output)
		s.closedOnce.Do(func() { close(s.closedCh) })
	}()
	for {
		event, ready, stop := s.head()
		if stop {
			return
		}
		if !ready {
			select {
			case <-s.wake:
				continue
			case <-s.closedCh:
				return
			}
		}

		select {
		case s.output <- event:
			s.consumeHead()
		case <-s.closedCh:
			return
		}
	}
}

func (s *subscriber) head() (TerminalEvent, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (s.closing && len(s.queue) == 0) {
		return TerminalEvent{}, false, true
	}
	if len(s.queue) == 0 {
		return TerminalEvent{}, false, false
	}
	return s.queue[0], true, false
}

func (s *subscriber) consumeHead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return
	}
	s.queuedBytes -= len(s.queue[0].Data)
	s.queue[0] = TerminalEvent{}
	s.queue = s.queue[1:]
	if s.closing && len(s.queue) == 0 {
		s.closedOnce.Do(func() { close(s.closedCh) })
	}
}
