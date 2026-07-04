package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/alim596/meridian/internal/engine"
)

// The market-data feed follows the snapshot + sequenced deltas pattern used
// by real venues: on subscribe a client gets a full L2 snapshot stamped with
// the engine sequence number, then incremental updates. Clients buffer
// deltas until the snapshot lands, discard anything with seq <= snapshot
// seq, and resubscribe if they ever observe a gap.
//
// Backpressure policy: every client has a bounded send queue. A client that
// can't keep up is disconnected rather than allowed to stall the exchange —
// slow consumers get dropped, the matching path never blocks on I/O.

const clientQueueSize = 512

type wsMsg struct {
	Type       string          `json:"type"`
	Instrument string          `json:"instrument,omitempty"`
	Seq        uint64          `json:"seq,omitempty"`
	TS         int64           `json:"ts,omitempty"`
	Bids       [][2]int64      `json:"bids,omitempty"`
	Asks       [][2]int64      `json:"asks,omitempty"`
	Last       int64           `json:"last,omitempty"`
	Side       string          `json:"side,omitempty"`
	Price      int64           `json:"price,omitempty"`
	Qty        int64           `json:"qty,omitempty"`
	TakerSide  string          `json:"takerSide,omitempty"`
	Stats      json.RawMessage `json:"stats,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type client struct {
	conn *websocket.Conn
	send chan wsMsg
	subs map[string]bool
	mu   sync.Mutex
}

func (c *client) subscribed(instrument string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs[instrument]
}

func (c *client) setSub(instrument string, on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on {
		c.subs[instrument] = true
	} else {
		delete(c.subs, instrument)
	}
}

// enqueue tries to queue a message; returns false if the client is too slow.
func (c *client) enqueue(m wsMsg) bool {
	select {
	case c.send <- m:
		return true
	default:
		return false
	}
}

type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	engines map[string]*engine.Engine
}

func NewHub(engines map[string]*engine.Engine) *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
		engines: engines,
	}
}

// OnEvent fans public events out to subscribed clients. Called on the bus
// goroutine — must never block, hence enqueue-or-drop.
func (h *Hub) OnEvent(ev engine.Event) {
	var m wsMsg
	switch ev.Kind {
	case engine.EvL2:
		m = wsMsg{
			Type: "l2", Instrument: ev.Instrument, Seq: ev.Seq, TS: ev.TS,
			Side: ev.Level.Side, Price: ev.Level.Price, Qty: ev.Level.Qty,
		}
	case engine.EvTrade:
		m = wsMsg{
			Type: "trade", Instrument: ev.Instrument, Seq: ev.Seq, TS: ev.TS,
			Price: ev.Trade.Price, Qty: ev.Trade.Qty, TakerSide: ev.Trade.TakerSide,
		}
	default:
		return // order lifecycle events are private; not broadcast
	}

	h.mu.Lock()
	var drop []*client
	for c := range h.clients {
		if c.subscribed(ev.Instrument) && !c.enqueue(m) {
			drop = append(drop, c)
		}
	}
	for _, c := range drop {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// BroadcastStats pushes the periodic stats ticker to all clients.
func (h *Hub) BroadcastStats(stats json.RawMessage) {
	m := wsMsg{Type: "stats", Stats: stats, TS: time.Now().UnixNano()}
	h.mu.Lock()
	var drop []*client
	for c := range h.clients {
		if !c.enqueue(m) {
			drop = append(drop, c)
		}
	}
	for _, c := range drop {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// Demo exchange: market data is public, any origin may subscribe.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{
		conn: conn,
		send: make(chan wsMsg, clientQueueSize),
		subs: make(map[string]bool),
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	go h.writePump(c)
	h.readPump(c)
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// readPump processes subscribe/unsubscribe requests from one client.
func (h *Hub) readPump(c *client) {
	defer func() {
		h.remove(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(4096)
	for {
		var req struct {
			Op         string `json:"op"`
			Instrument string `json:"instrument"`
		}
		if err := c.conn.ReadJSON(&req); err != nil {
			return
		}
		switch req.Op {
		case "subscribe":
			eng, ok := h.engines[req.Instrument]
			if !ok {
				c.enqueue(wsMsg{Type: "error", Error: "unknown instrument: " + req.Instrument})
				continue
			}
			c.setSub(req.Instrument, true)
			// Snapshot AFTER registering the sub: any deltas that race in
			// carry seq <= snapshot.Seq and are discarded client-side.
			snap := eng.Snapshot(depthLevels)
			c.enqueue(wsMsg{
				Type: "snapshot", Instrument: req.Instrument, Seq: snap.Seq,
				Bids: packLevels(snap.Bids), Asks: packLevels(snap.Asks),
				Last: snap.LastPrice, TS: time.Now().UnixNano(),
			})
		case "unsubscribe":
			c.setSub(req.Instrument, false)
		}
	}
}

func (h *Hub) writePump(c *client) {
	ping := time.NewTicker(30 * time.Second)
	defer func() {
		ping.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case m, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "slow consumer"))
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.conn.WriteJSON(m); err != nil {
				return
			}
		case <-ping.C:
			c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// RunStatsTicker broadcasts stats for all instruments once per second.
func (h *Hub) RunStatsTicker(ctx context.Context, statsFn func() any) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b, err := json.Marshal(statsFn())
			if err == nil {
				h.BroadcastStats(b)
			}
		}
	}
}
