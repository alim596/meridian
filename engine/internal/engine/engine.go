// Package engine runs one single-threaded matching loop per instrument.
//
// Design (LMAX-style single writer): all mutations of an instrument's book
// flow through one goroutine via a command channel. No locks in the matching
// path, total ordering for free, and deterministic replay of the event
// stream. Cross-instrument concerns (accounts, market data, fan-out) hang
// off the event stream instead of sharing state with the engine.
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/alim596/meridian/internal/metrics"
	"github.com/alim596/meridian/internal/orderbook"
)

// Instrument is static reference data for a tradable symbol. Prices are in
// ticks; PriceScale converts ticks to display currency (e.g. 100 = cents).
type Instrument struct {
	Symbol     string `json:"symbol"`
	Name       string `json:"name"`
	PriceScale int64  `json:"priceScale"` // ticks per currency unit
	InitPrice  int64  `json:"initPrice"`  // ticks, seed for the simulation
}

// MaxOrderQty bounds a single order; a crude but honest sanity limit.
const MaxOrderQty = 100_000

// Volatility circuit breaker (LULD-style, simplified): if a trade prints
// more than haltMovePct away from where the instrument traded haltRefWindow
// ago, continuous trading pauses for haltDuration. Resting orders stay on
// the book and cancels are still accepted during the halt.
const (
	haltMovePct   = 4.0
	haltRefWindow = 30 * time.Second
	haltDuration  = 25 * time.Second
)

type refPoint struct {
	ts    time.Time
	price int64
}

// RiskChecker validates an order before it reaches the book. Implemented by
// the account manager; nil means no checks (used in tests and replay).
type RiskChecker interface {
	CheckOrder(account string, instrument string, side orderbook.Side, typ orderbook.OrderType, price, qty int64, refPrice int64) error
}

// SubmitCmd asks the engine to match a new order.
type SubmitCmd struct {
	Account    string
	Side       orderbook.Side
	Type       orderbook.OrderType
	TIF        orderbook.TimeInForce
	Price      int64
	Qty        int64
	EnqueuedAt time.Time
	Resp       chan SubmitResp // buffered(1) or nil
}

type SubmitResp struct {
	OrderID   uint64 `json:"orderId"`
	Status    string `json:"status"` // accepted | rejected
	Reason    string `json:"reason,omitempty"`
	FilledQty int64  `json:"filledQty"`
	Notional  int64  `json:"notional"` // ticks*lots filled, for avg price
	Resting   bool   `json:"resting"`
}

// CancelCmd asks the engine to pull a resting order.
type CancelCmd struct {
	OrderID uint64
	Account string
	Resp    chan CancelResp
}

type CancelResp struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// SnapshotCmd reads a consistent L2 snapshot off the engine goroutine.
type SnapshotCmd struct {
	Depth int
	Resp  chan SnapshotResp
}

type SnapshotResp struct {
	Seq         uint64                 `json:"seq"`
	Bids        []orderbook.PriceLevel `json:"bids"`
	Asks        []orderbook.PriceLevel `json:"asks"`
	LastPrice   int64                  `json:"lastPrice"`
	HaltedUntil int64                  `json:"haltedUntil,omitempty"` // unix ms, 0 = trading
}

// Engine owns one instrument's book.
type Engine struct {
	Inst Instrument

	book        *orderbook.Book
	cmds        chan any
	emit        func(Event)
	risk        RiskChecker
	seq         uint64
	nextOrderID uint64
	lastPrice   int64

	halted      bool
	haltedUntil time.Time
	refPrices   []refPoint

	Latency *metrics.Histogram
}

// New creates an engine. emit is called on the engine goroutine for every
// event, in sequence order; it must be fast (hand off to a channel).
func New(inst Instrument, risk RiskChecker, emit func(Event)) *Engine {
	return &Engine{
		Inst:      inst,
		book:      orderbook.New(),
		cmds:      make(chan any, 4096),
		emit:      emit,
		risk:      risk,
		lastPrice: inst.InitPrice,
		Latency:   metrics.NewHistogram(),
	}
}

// Submit enqueues a command; blocks only if the engine is saturated
// (bounded queue = natural backpressure).
func (e *Engine) Submit(cmd SubmitCmd) {
	cmd.EnqueuedAt = time.Now()
	e.cmds <- cmd
}

func (e *Engine) Cancel(cmd CancelCmd) { e.cmds <- cmd }

// Snapshot returns a sequenced L2 snapshot, consistent with the event
// stream: every event with Seq > resp.Seq applies cleanly on top of it.
func (e *Engine) Snapshot(depth int) SnapshotResp {
	resp := make(chan SnapshotResp, 1)
	e.cmds <- SnapshotCmd{Depth: depth, Resp: resp}
	return <-resp
}

func (e *Engine) Run(ctx context.Context) {
	// The ticker only exists to emit the resume event promptly when a halt
	// expires; the matching path itself never blocks on it.
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.maybeResume()
		case cmd := <-e.cmds:
			switch c := cmd.(type) {
			case SubmitCmd:
				e.handleSubmit(c)
			case CancelCmd:
				e.handleCancel(c)
			case SnapshotCmd:
				bids, asks := e.book.Snapshot(c.Depth)
				resp := SnapshotResp{Seq: e.seq, Bids: bids, Asks: asks, LastPrice: e.lastPrice}
				if e.halted {
					resp.HaltedUntil = e.haltedUntil.UnixMilli()
				}
				c.Resp <- resp
			}
		}
	}
}

func (e *Engine) maybeResume() {
	if e.halted && time.Now().After(e.haltedUntil) {
		e.halted = false
		e.refPrices = e.refPrices[:0] // fresh volatility window post-halt
		e.emit(e.event(EvResume))
	}
}

// checkHalt runs after each trade: compare the print against the oldest
// reference price still inside the lookback window.
func (e *Engine) checkHalt(price int64, now time.Time) {
	for len(e.refPrices) >= 2 && e.refPrices[1].ts.Before(now.Add(-haltRefWindow)) {
		e.refPrices = e.refPrices[1:]
	}
	if len(e.refPrices) > 0 {
		ref := e.refPrices[0].price
		if ref > 0 {
			move := 100 * float64(price-ref) / float64(ref)
			if move < 0 {
				move = -move
			}
			if move >= haltMovePct {
				e.halted = true
				e.haltedUntil = now.Add(haltDuration)
				e.refPrices = e.refPrices[:0]
				ev := e.event(EvHalt)
				ev.Halt = &HaltEvent{
					Until: e.haltedUntil.UnixMilli(), RefPrice: ref,
					TradePrice: price, MovePct: move,
				}
				e.emit(ev)
				e.refPrices = append(e.refPrices, refPoint{ts: now, price: price})
				return
			}
		}
	}
	e.refPrices = append(e.refPrices, refPoint{ts: now, price: price})
}

func (e *Engine) next() uint64 {
	e.seq++
	return e.seq
}

func (e *Engine) event(kind string) Event {
	return Event{Seq: e.next(), Instrument: e.Inst.Symbol, TS: time.Now().UnixNano(), Kind: kind}
}

func reply[T any](ch chan T, v T) {
	if ch != nil {
		ch <- v
	}
}

func (e *Engine) reject(c SubmitCmd, reason string) {
	ev := e.event(EvRejected)
	ev.Order = &OrderEvent{
		Account: c.Account, Side: c.Side.String(), Type: c.Type.String(),
		TIF: c.TIF.String(), Price: c.Price, Qty: c.Qty, Remaining: c.Qty, Reason: reason,
	}
	e.emit(ev)
	reply(c.Resp, SubmitResp{Status: "rejected", Reason: reason})
}

func (e *Engine) validate(c SubmitCmd) error {
	if c.Qty <= 0 {
		return fmt.Errorf("quantity must be positive")
	}
	if c.Qty > MaxOrderQty {
		return fmt.Errorf("quantity exceeds max %d", MaxOrderQty)
	}
	if c.Type == orderbook.Limit && c.Price <= 0 {
		return fmt.Errorf("limit price must be positive")
	}
	if c.Type == orderbook.Market && c.TIF != orderbook.IOC {
		return fmt.Errorf("market orders must be IOC")
	}
	return nil
}

func (e *Engine) handleSubmit(c SubmitCmd) {
	defer func() { e.Latency.Observe(time.Since(c.EnqueuedAt)) }()

	if err := e.validate(c); err != nil {
		e.reject(c, err.Error())
		return
	}
	if e.halted {
		e.reject(c, "trading halted: volatility circuit breaker")
		return
	}
	if e.risk != nil {
		if err := e.risk.CheckOrder(c.Account, e.Inst.Symbol, c.Side, c.Type, c.Price, c.Qty, e.lastPrice); err != nil {
			e.reject(c, err.Error())
			return
		}
	}

	e.nextOrderID++
	o := &orderbook.Order{
		ID: e.nextOrderID, Account: c.Account, Side: c.Side,
		Type: c.Type, TIF: c.TIF, Price: c.Price, Qty: c.Qty,
	}
	res := e.book.Submit(o)

	var filled, notional int64
	for _, tr := range res.Trades {
		filled += tr.Qty
		notional += tr.Qty * tr.Price
	}

	ev := e.event(EvAccepted)
	ev.Order = &OrderEvent{
		ID: o.ID, Account: c.Account, Side: c.Side.String(), Type: c.Type.String(),
		TIF: c.TIF.String(), Price: c.Price, Qty: c.Qty, Filled: filled, Remaining: o.Remaining,
	}
	e.emit(ev)

	now := time.Now()
	for _, tr := range res.Trades {
		e.lastPrice = tr.Price
		tev := e.event(EvTrade)
		tev.Trade = &TradeEvent{
			Price: tr.Price, Qty: tr.Qty, TakerSide: tr.TakerSide.String(),
			TakerOrderID: tr.TakerOrderID, MakerOrderID: tr.MakerOrderID,
			TakerAccount: tr.TakerAccount, MakerAccount: tr.MakerAccount,
		}
		e.emit(tev)
		if !e.halted {
			e.checkHalt(tr.Price, now)
		}
	}
	for _, u := range res.Updates {
		lev := e.event(EvL2)
		lev.Level = &LevelEvent{Side: u.Side.String(), Price: u.Price, Qty: u.Qty}
		e.emit(lev)
	}
	if res.CanceledQty > 0 {
		cev := e.event(EvCanceled)
		cev.Order = &OrderEvent{
			ID: o.ID, Account: c.Account, Side: c.Side.String(), Type: c.Type.String(),
			TIF: c.TIF.String(), Price: c.Price, Qty: c.Qty, Filled: filled,
			Remaining: res.CanceledQty, Reason: "unfilled remainder",
		}
		e.emit(cev)
	}

	reply(c.Resp, SubmitResp{
		OrderID: o.ID, Status: "accepted", FilledQty: filled,
		Notional: notional, Resting: res.Resting,
	})
}

func (e *Engine) handleCancel(c CancelCmd) {
	o, ok := e.book.Order(c.OrderID)
	if !ok {
		reply(c.Resp, CancelResp{OK: false, Reason: "order not found"})
		return
	}
	if o.Account != c.Account {
		// Do not leak whether another account's order exists.
		reply(c.Resp, CancelResp{OK: false, Reason: "order not found"})
		return
	}
	o, upd, _ := e.book.Cancel(c.OrderID)

	ev := e.event(EvCanceled)
	ev.Order = &OrderEvent{
		ID: o.ID, Account: o.Account, Side: o.Side.String(), Type: o.Type.String(),
		TIF: o.TIF.String(), Price: o.Price, Qty: o.Qty,
		Filled: o.Qty - o.Remaining, Remaining: o.Remaining, Reason: "user cancel",
	}
	e.emit(ev)
	lev := e.event(EvL2)
	lev.Level = &LevelEvent{Side: upd.Side.String(), Price: upd.Price, Qty: upd.Qty}
	e.emit(lev)

	reply(c.Resp, CancelResp{OK: true})
}
