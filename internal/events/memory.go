package events

import (
	"context"
	"sync"
	"sync/atomic"
)

const subscriberBuffer = 64

type subscription struct {
	ch     chan Event
	types  map[string]struct{}
	mu     sync.Mutex
	closed bool
}

func (s *subscription) wants(eventType string) bool {
	if len(s.types) == 0 {
		return true
	}
	_, ok := s.types[eventType]
	return ok
}

func (s *subscription) deliver(event Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- event:
		return true
	default:
		return false
	}
}

// MemoryBus is a process-local pub/sub bus.
// A full subscriber buffer drops the event instead of blocking dispatch.
type MemoryBus struct {
	mu      sync.Mutex
	next    int
	subs    map[int]*subscription
	dropped atomic.Uint64
}

// NewMemoryBus returns an empty bus.
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{subs: map[int]*subscription{}}
}

// Publish implements Publisher.
func (b *MemoryBus) Publish(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	subs := make([]*subscription, 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.Unlock()

	for _, sub := range subs {
		if !sub.wants(event.Type) {
			continue
		}
		if !sub.deliver(event) {
			b.dropped.Add(1)
		}
	}
	return nil
}

// Subscribe registers a receiver. types limits delivery; no types means all events.
// Cancel removes the subscription. Further publishes will not send on the channel.
func (b *MemoryBus) Subscribe(types ...string) (<-chan Event, func()) {
	filter := make(map[string]struct{}, len(types))
	for _, t := range types {
		if t != "" {
			filter[t] = struct{}{}
		}
	}
	sub := &subscription{ch: make(chan Event, subscriberBuffer), types: filter}

	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = sub
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
		sub.mu.Lock()
		sub.closed = true
		sub.mu.Unlock()
	}
	return sub.ch, cancel
}

// Dropped returns how many deliveries were skipped because a buffer was full.
func (b *MemoryBus) Dropped() uint64 {
	return b.dropped.Load()
}
