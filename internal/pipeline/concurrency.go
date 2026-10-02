package pipeline

import "sync"

// semaphore is a bounded-count semaphore.
type semaphore struct {
	ch chan struct{}
}

func newSemaphore(n int) *semaphore {
	if n < 1 {
		n = 1
	}
	return &semaphore{ch: make(chan struct{}, n)}
}

func (s *semaphore) acquire() { s.ch <- struct{}{} }
func (s *semaphore) release() { <-s.ch }

// byteSemaphore bounds in-flight bytes. A limit of 0 means unlimited.
type byteSemaphore struct {
	mu     sync.Mutex
	cond   *sync.Cond
	limit  int64
	inUse  int64
}

func newByteSemaphore(limit int64) *byteSemaphore {
	b := &byteSemaphore{limit: limit}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *byteSemaphore) acquire(n int64) {
	if b.limit <= 0 {
		return
	}
	b.mu.Lock()
	for b.inUse+n > b.limit {
		b.cond.Wait()
	}
	b.inUse += n
	b.mu.Unlock()
}

func (b *byteSemaphore) release(n int64) {
	if b.limit <= 0 {
		return
	}
	b.mu.Lock()
	b.inUse -= n
	if b.inUse < 0 {
		b.inUse = 0
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}
