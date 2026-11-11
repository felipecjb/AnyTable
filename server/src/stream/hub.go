// Package stream distributes captured frames to clients with a lossy,
// freshest-frame-only policy and encodes them as JPEG.
package stream

import (
	"context"
	"sync"

	"anytable/server/src/capture"
)

// Slot is a one-frame mailbox. A new frame replaces any unread one, so a slow
// consumer (saturated socket) always gets the freshest frame and never a backlog.
type Slot struct {
	mu      sync.Mutex
	frame   *capture.Frame
	closed  bool
	dropped uint64
	ready   chan struct{}
	done    chan struct{}
}

func newSlot() *Slot {
	return &Slot{ready: make(chan struct{}, 1), done: make(chan struct{})}
}

func (s *Slot) put(f *capture.Frame) {
	f.Retain()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		f.Release()
		return
	}
	old := s.frame
	s.frame = f
	if old != nil {
		s.dropped++
	}
	s.mu.Unlock()
	if old != nil {
		old.Release()
	}
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

// Take blocks until a frame is available (caller must Release it) or the slot is closed.
func (s *Slot) Take() (*capture.Frame, bool) {
	for {
		s.mu.Lock()
		f := s.frame
		s.frame = nil
		s.mu.Unlock()
		if f != nil {
			return f, true
		}
		select {
		case <-s.ready:
		case <-s.done:
			return nil, false
		}
	}
}

// Dropped returns how many frames were discarded unread.
func (s *Slot) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

func (s *Slot) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	f := s.frame
	s.frame = nil
	s.mu.Unlock()
	if f != nil {
		f.Release()
	}
	close(s.done)
}

// Hub fans captured frames out to subscriber slots.
type Hub struct {
	mu     sync.RWMutex
	slots  map[*Slot]struct{}
	notify chan struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{slots: make(map[*Slot]struct{}), notify: make(chan struct{}, 1)}
}

// Subscribe registers a new consumer.
func (h *Hub) Subscribe() *Slot {
	s := newSlot()
	h.mu.Lock()
	h.slots[s] = struct{}{}
	h.mu.Unlock()
	select {
	case h.notify <- struct{}{}:
	default:
	}
	return s
}

// Unsubscribe removes and closes the slot, releasing any pending frame.
func (h *Hub) Unsubscribe(s *Slot) {
	h.mu.Lock()
	delete(h.slots, s)
	h.mu.Unlock()
	s.close()
}

// Count returns the number of subscribers.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.slots)
}

// Publish hands the frame to every subscriber (each takes its own reference).
func (h *Hub) Publish(f *capture.Frame) {
	h.mu.RLock()
	for s := range h.slots {
		s.put(f)
	}
	h.mu.RUnlock()
}

// WaitSubscribers blocks until at least one subscriber exists or ctx ends.
func (h *Hub) WaitSubscribers(ctx context.Context) error {
	for h.Count() == 0 {
		select {
		case <-h.notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
