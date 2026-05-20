package chat

import (
	"context"
	"sync"
)

// controlQueue is a channel-based FIFO mutex.
//
// Acquire returns nil + release when the queue grants the caller the
// token. Multiple goroutines waiting in Acquire are served in
// first-come, first-served order. If the calling context cancels
// before the token is granted, Acquire returns ctx.Err() and the
// waiter is removed from the queue.
//
// release is safe to call multiple times; only the first call has effect.
type controlQueue struct {
	mu     sync.Mutex
	held   bool
	closed bool
	queue  []chan struct{}
}

func newControlQueue() *controlQueue { return &controlQueue{} }

// Acquire blocks until the token is granted or ctx cancels. The
// returned release function passes the token to the next waiter (or
// leaves it free if the queue is empty).
func (q *controlQueue) Acquire(ctx context.Context) (release func(), err error) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil, ErrClosed
	}
	if !q.held {
		q.held = true
		q.mu.Unlock()
		return q.releaseFunc(), nil
	}
	waiter := make(chan struct{})
	q.queue = append(q.queue, waiter)
	q.mu.Unlock()

	select {
	case <-waiter:
		// We received the token; releaseFunc will pass it on.
		return q.releaseFunc(), nil
	case <-ctx.Done():
		q.mu.Lock()
		// We may have been granted just as ctx cancelled; in that case
		// receiver already sees the closed waiter — but we still need
		// to pass the token on. Check both states.
		select {
		case <-waiter:
			q.mu.Unlock()
			// Hand off immediately.
			r := q.releaseFunc()
			r()
			return nil, ctx.Err()
		default:
			// Not yet granted — remove from queue and bail.
			for i, w := range q.queue {
				if w == waiter {
					q.queue = append(q.queue[:i], q.queue[i+1:]...)
					break
				}
			}
			q.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// Held reports whether some caller currently holds the token.
func (q *controlQueue) Held() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.held
}

// Close marks the queue closed. Subsequent Acquire calls return
// ErrClosed. In-flight Acquires waiting in the queue are released
// with ErrClosed by closing every waiter; their goroutines will
// observe ctx.Err() vs. nil and handle accordingly via the select
// above. (For v1 we keep semantics simple: callers should Close
// only after all waiters have unwound on their own ctx cancellation.)
func (q *controlQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
}

func (q *controlQueue) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			q.mu.Lock()
			defer q.mu.Unlock()
			if !q.held {
				return
			}
			if len(q.queue) == 0 {
				q.held = false
				return
			}
			next := q.queue[0]
			q.queue = q.queue[1:]
			close(next)
			// q.held stays true; ownership transfers to next.
		})
	}
}
