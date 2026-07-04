// Package bus fans the engines' event streams out to consumers (journal,
// account manager, market data, WebSocket hub).
//
// Publish blocks when the buffer is full: consumers registered here are
// trusted internal components that must keep up, and blocking gives the
// engines natural backpressure instead of silent loss. Untrusted consumers
// (WebSocket clients) get a drop policy in the hub instead.
package bus

import (
	"context"
	"sync"

	"github.com/alim596/meridian/internal/engine"
)

type Handler func(engine.Event)

type Bus struct {
	mu       sync.Mutex
	handlers []Handler
	ch       chan engine.Event
	started  bool
}

func New(buffer int) *Bus {
	return &Bus{ch: make(chan engine.Event, buffer)}
}

// Subscribe registers a handler. Must be called before Run.
func (b *Bus) Subscribe(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		panic("bus: Subscribe after Run")
	}
	b.handlers = append(b.handlers, h)
}

// Publish enqueues an event; safe from any goroutine.
func (b *Bus) Publish(e engine.Event) { b.ch <- e }

// Run dispatches events to all handlers in order, on a single goroutine, so
// every consumer observes the same total order per instrument.
func (b *Bus) Run(ctx context.Context) {
	b.mu.Lock()
	b.started = true
	handlers := b.handlers
	b.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case e := <-b.ch:
			for _, h := range handlers {
				h(e)
			}
		}
	}
}
