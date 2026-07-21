// Package dashboard aggregates fleet-wide state for the overview screen:
// presence, latest heartbeats and a rolling event feed.
package dashboard

import (
	"context"
	"sync"
	"time"

	"github.com/runix/runix/internal/platform/bus"
	"github.com/runix/runix/internal/protocol"
)

const eventFeedSize = 50

type Event struct {
	Topic    string    `json:"topic"`
	ServerID string    `json:"serverId,omitempty"`
	At       time.Time `json:"at"`
}

type Service struct {
	bus *bus.Bus

	mu         sync.RWMutex
	heartbeats map[string]protocol.Heartbeat
	events     []Event
}

func NewService(eventBus *bus.Bus) *Service {
	return &Service{
		bus:        eventBus,
		heartbeats: make(map[string]protocol.Heartbeat),
	}
}

// Run consumes bus events until ctx ends; started by the app.
func (s *Service) Run(ctx context.Context) {
	sub := s.bus.Subscribe("agent.heartbeat", "agent.online", "agent.offline")
	defer sub.Close()
	for {
		select {
		case event, ok := <-sub.C:
			if !ok {
				return
			}
			s.consume(event)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) consume(event bus.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch event.Topic {
	case "agent.heartbeat":
		if hb, ok := event.Payload.(protocol.Heartbeat); ok {
			s.heartbeats[event.ServerID] = hb
		}
	case "agent.online", "agent.offline":
		if event.Topic == "agent.offline" {
			delete(s.heartbeats, event.ServerID)
		}
		s.events = append(s.events, Event{Topic: event.Topic, ServerID: event.ServerID, At: event.At})
		if len(s.events) > eventFeedSize {
			s.events = s.events[len(s.events)-eventFeedSize:]
		}
	}
}

// RuntimeSummary aggregates runtime states across the connected fleet.
func (s *Service) RuntimeSummary() map[string]map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]map[string]int{}
	for _, hb := range s.heartbeats {
		for _, rc := range hb.Runtimes {
			byState, ok := out[rc.Type]
			if !ok {
				byState = map[string]int{}
				out[rc.Type] = byState
			}
			for state, n := range rc.States {
				byState[state] += n
			}
		}
	}
	return out
}

func (s *Service) RecentEvents() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}
