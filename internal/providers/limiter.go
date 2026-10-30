package providers

// At most `limit` tasks at once, the rest started in the order they asked.

import (
	"errors"
	"sync"
)

// ErrConcurrency is the TypeScript's RangeError for a bad limit.
var ErrConcurrency = errors.New("concurrency must be a positive integer")

type Limiter struct {
	mu     sync.Mutex
	limit  int
	active int
	queue  []chan struct{}
}

func CreateLimiter(limit int) (*Limiter, error) {
	if limit < 1 {
		return nil, ErrConcurrency
	}
	return &Limiter{limit: limit}, nil
}

func (l *Limiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

func (l *Limiter) Waiting() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue)
}

func (l *Limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	if len(l.queue) > 0 {
		next := l.queue[0]
		l.queue = l.queue[1:]
		l.active++
		close(next)
	}
}

// Run runs task when a slot is free, and returns what it returns. The slot
// is released however the task ends, a panic included.
func (l *Limiter) Run(task func() error) error {
	l.mu.Lock()
	if l.active < l.limit {
		l.active++
		l.mu.Unlock()
	} else {
		ready := make(chan struct{})
		l.queue = append(l.queue, ready)
		l.mu.Unlock()
		<-ready
	}
	defer l.release()
	return task()
}
