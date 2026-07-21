package bus

import (
	"sync"
	"time"
)

// Event is one message on the in-process event bus: agent presence changes,
// heartbeats, runtime state changes. Fan-out to WebSocket clients and
// dashboard aggregation subscribe here. A Redis-backed bus can replace this
// behind the same interface when multi-instance control planes land.
type Event struct {
	Topic    string
	ServerID string
	At       time.Time
	Payload  any
	// Origin identifies the control-plane instance that raised the event.
	// It is empty for locally raised events and set on ones mirrored in
	// from Redis, which is how the bridge avoids republishing echoes.
	Origin string
}

type Subscription struct {
	C      chan Event
	cancel func()
}

func (s *Subscription) Close() { s.cancel() }

type Bus struct {
	mu   sync.RWMutex
	subs map[int]*subscriber
	next int
}

type subscriber struct {
	ch     chan Event
	topics map[string]struct{} // empty = all topics
}

func New() *Bus {
	return &Bus{subs: make(map[int]*subscriber)}
}

// Subscribe delivers events for the given topics (all topics when none are
// given). Slow subscribers drop events rather than block publishers.
func (b *Bus) Subscribe(topics ...string) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.next
	b.next++
	sub := &subscriber{ch: make(chan Event, 64)}
	if len(topics) > 0 {
		sub.topics = make(map[string]struct{}, len(topics))
		for _, t := range topics {
			sub.topics[t] = struct{}{}
		}
	}
	b.subs[id] = sub

	return &Subscription{
		C: sub.ch,
		cancel: func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if s, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(s.ch)
			}
		},
	}
}

func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		if s.topics != nil {
			if _, ok := s.topics[e.Topic]; !ok {
				continue
			}
		}
		select {
		case s.ch <- e:
		default: // drop for slow consumers
		}
	}
}
