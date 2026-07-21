package daemon

import (
	"context"
	"io"
	"sync"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

const ringCapacity = 2000

// logBuffer keeps the last N log entries in memory and fans new entries out
// to followers. The on-disk log file is written separately for durability.
type logBuffer struct {
	mu      sync.Mutex
	entries []rt.LogEntry
	start   int
	count   int
	subs    map[int]chan rt.LogEntry
	nextSub int
}

func newLogBuffer() *logBuffer {
	return &logBuffer{
		entries: make([]rt.LogEntry, ringCapacity),
		subs:    make(map[int]chan rt.LogEntry),
	}
}

func (b *logBuffer) append(source rt.LogSource, line []byte) {
	entry := rt.LogEntry{
		Timestamp: time.Now(),
		Source:    source,
		Line:      append([]byte{}, line...),
	}
	b.mu.Lock()
	idx := (b.start + b.count) % ringCapacity
	if b.count == ringCapacity {
		b.start = (b.start + 1) % ringCapacity
	} else {
		b.count++
	}
	b.entries[idx] = entry
	for _, ch := range b.subs {
		select {
		case ch <- entry:
		default:
		}
	}
	b.mu.Unlock()
}

func (b *logBuffer) tail(n int) []rt.LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > b.count {
		n = b.count
	}
	out := make([]rt.LogEntry, 0, n)
	for i := b.count - n; i < b.count; i++ {
		out = append(out, b.entries[(b.start+i)%ringCapacity])
	}
	return out
}

func (b *logBuffer) subscribe() (int, chan rt.LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextSub
	b.nextSub++
	ch := make(chan rt.LogEntry, 256)
	b.subs[id] = ch
	return id, ch
}

func (b *logBuffer) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, id)
}

// stream implements rt.LogStream over the ring plus optional follow.
type stream struct {
	backlog []rt.LogEntry
	follow  chan rt.LogEntry
	done    func()
}

func (b *logBuffer) stream(opts rt.LogOptions) rt.LogStream {
	tail := opts.Tail
	if tail == 0 {
		tail = 200
	}
	s := &stream{backlog: b.tail(tail), done: func() {}}
	if opts.Follow {
		id, ch := b.subscribe()
		s.follow = ch
		s.done = func() { b.unsubscribe(id) }
	}
	return s
}

func (s *stream) Next(ctx context.Context) (rt.LogEntry, error) {
	if len(s.backlog) > 0 {
		entry := s.backlog[0]
		s.backlog = s.backlog[1:]
		return entry, nil
	}
	if s.follow == nil {
		return rt.LogEntry{}, io.EOF
	}
	select {
	case entry := <-s.follow:
		return entry, nil
	case <-ctx.Done():
		return rt.LogEntry{}, ctx.Err()
	}
}

func (s *stream) Close() error {
	s.done()
	return nil
}
